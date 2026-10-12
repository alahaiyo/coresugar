package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"coresugar/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func callTool(t *testing.T, ctx context.Context, client *mcp.ClientSession, name string, input any, output any) bool {
	t.Helper()
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
	if err != nil {
		t.Errorf("%s protocol: %v", name, err)
		return false
	}
	if result.IsError {
		t.Errorf("%s failed: %+v", name, result.Content)
		return false
	}
	data, err := json.Marshal(result.StructuredContent)
	if err == nil {
		err = json.Unmarshal(data, output)
	}
	if err != nil {
		t.Errorf("%s response: %v", name, err)
		return false
	}
	return true
}

// TestMCPChain 验证真实 stdio MCP 进程：SSH 或本机 Linux 连接、四个调试 Tool、
// 十条命令及有界响应、证据分页和重启读取、源码搜索读取、会话状态、重复关闭及断连清理。
// 必须显式设置配置与二进制路径；未配置的本地测试跳过。
func TestMCPChain(t *testing.T) {
	binary := os.Getenv("CORESUGAR_MCP_BINARY")
	path := os.Getenv("CORESUGAR_TEST_CONFIG")
	if binary == "" || path == "" {
		t.Skip("set CORESUGAR_MCP_BINARY and CORESUGAR_TEST_CONFIG")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("configuration: %v", err)
		return
	}
	var config struct {
		core.Inputs
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Errorf("configuration JSON: %v", err)
		return
	}
	args := []string{binary, "serve", "--allowed-root", config.Directory, "--evidence-dir", config.Directory + "/coresugar-work/evidence"}
	var command *exec.Cmd
	if host := os.Getenv("CORESUGAR_MCP_SSH"); host != "" {
		command = exec.Command("ssh", append([]string{"-T", "-o", "BatchMode=yes", host}, args...)...)
	} else {
		command = exec.Command(args[0], args[1:]...)
	}
	command.Stderr = os.Stderr
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "coresugar-integration", Version: "0.1.0"}, nil)
	connection, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Errorf("MCP connect: %v", err)
		return
	}
	defer connection.Close()
	var savedSession, savedEvidence string
	var info core.SessionInfo
	if !callTool(t, ctx, connection, "open_dump", config.Inputs, &info) {
		return
	}
	id := info.ID
	if id == "" || info.Status != "READY" {
		t.Errorf("open result: %+v", info)
		return
	}
	commands := []string{"sys", "log", "bt", "ps -p", "kmem -i", "sys", "log", "bt", "ps -p", "kmem -i"}
	for _, command := range commands {
		var result core.ExecResult
		if !callTool(t, ctx, connection, "crash_exec", core.ExecRequest{SessionID: id, Command: command, TimeoutMS: 120000, MaxOutputBytes: 1024}, &result) {
			return
		}
		if result.EvidenceID == "" {
			t.Errorf("command evidence absent")
			return
		}
		if command == "log" && savedEvidence == "" {
			savedSession = id
			savedEvidence = result.EvidenceID
			var offset int64
			var assembled []byte
			for {
				var page core.EvidencePage
				if !callTool(t, ctx, connection, "get_evidence", core.EvidenceRequest{SessionID: id, EvidenceID: result.EvidenceID, Offset: offset, Limit: 65536}, &page) {
					return
				}
				data := []byte(page.Output)
				if page.OutputBase64 != "" {
					var err error
					data, err = base64.StdEncoding.DecodeString(page.OutputBase64)
					if err != nil {
						t.Errorf("page decode: %v", err)
						return
					}
				}
				assembled = append(assembled, data...)
				if page.Complete {
					hash := sha256.Sum256(assembled)
					if hex.EncodeToString(hash[:]) != page.SHA256 {
						t.Errorf("reassembled evidence hash differs")
					}
					if !strings.HasPrefix(string(assembled), result.Output) {
						t.Errorf("response differs from saved evidence")
					}
					break
				}
				if page.NextOffset == nil {
					t.Errorf("pagination ended early")
					return
				}
				offset = *page.NextOffset
			}
		}
		if result.SessionID != id || result.Output == "" || len(result.Output) > 1024 {
			t.Errorf("command result: %+v", result)
		}
	}
	var search core.SearchResult
	if !callTool(t, ctx, connection, "search_source", core.SearchRequest{SessionID: id, Pattern: "void __sched io_schedule", Path: "kernel/sched", MaxResults: 10}, &search) {
		return
	}
	if len(search.Matches) == 0 {
		t.Errorf("real source function not found")
		return
	}
	match := search.Matches[0]
	var source core.SourceResult
	if !callTool(t, ctx, connection, "read_source", core.SourceRequest{SessionID: id, File: match.File, StartLine: match.Line, EndLine: match.Line + 12}, &source) {
		return
	}
	if !strings.Contains(source.Content, "io_schedule") || source.EvidenceID == "" {
		t.Errorf("source/evidence incomplete: %+v", source)
	}
	if !callTool(t, ctx, connection, "get_session", map[string]any{"session_id": id}, &info) {
		return
	}
	if info.Status != "READY" {
		t.Errorf("status = %s", info.Status)
	}
	for range 2 {
		if !callTool(t, ctx, connection, "close_dump", map[string]any{"session_id": id}, &info) {
			return
		}
		if info.Status != "CLOSED" {
			t.Errorf("close status = %s", info.Status)
		}
	}
	if err := connection.Close(); err != nil {
		t.Errorf("client close: %v", err)
	}
	// Start another server process to verify persisted evidence survives restart.
	var restart *exec.Cmd
	if host := os.Getenv("CORESUGAR_MCP_SSH"); host != "" {
		restart = exec.Command("ssh", append([]string{"-T", "-o", "BatchMode=yes", host}, args...)...)
	} else {
		restart = exec.Command(args[0], args[1:]...)
	}
	restart.Stderr = os.Stderr
	reopened, err := client.Connect(ctx, &mcp.CommandTransport{Command: restart}, nil)
	if err != nil {
		t.Errorf("restart: %v", err)
		return
	}
	defer reopened.Close()
	var page core.EvidencePage
	callTool(t, ctx, reopened, "get_evidence", core.EvidenceRequest{SessionID: savedSession, EvidenceID: savedEvidence, Limit: 1024}, &page)
}
