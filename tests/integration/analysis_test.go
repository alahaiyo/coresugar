package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"coresugar/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPAnalysis 验证真实分析调用链：日志及堆栈证据、异常位置反汇编、对应源码搜索读取，
// 所有关键输出有证据编号；Linux 本机运行还验证操作日志的实际调用顺序和开始/结束配对。
// 可显式保存观察结果供 Agent 复核，不自动声称根因已找到。
func TestMCPAnalysis(t *testing.T) {
	binary := os.Getenv("CORESUGAR_MCP_BINARY")
	path := os.Getenv("CORESUGAR_TEST_CONFIG")
	if binary == "" || path == "" {
		t.Skip("configure real MCP binary and inputs")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("configuration: %v", err)
		return
	}
	var config struct {
		core.Inputs
		Directory    string `json:"directory"`
		AnalysisPath string `json:"analysis_path"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Errorf("configuration: %v", err)
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
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "coresugar-analysis-check", Version: "0.1.0"}, nil)
	connection, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Errorf("connection: %v", err)
		return
	}
	defer connection.Close()
	var observation struct {
		Session           core.SessionInfo  `json:"session"`
		System            core.ExecResult   `json:"system"`
		Log               core.ExecResult   `json:"log"`
		LogTail           core.EvidencePage `json:"log_tail"`
		Backtrace         core.ExecResult   `json:"backtrace"`
		Disassembly       core.ExecResult   `json:"disassembly"`
		SourceDisassembly core.ExecResult   `json:"source_disassembly"`
		Search            core.SearchResult `json:"search"`
		Source            core.SourceResult `json:"source"`
	}
	if !callTool(t, ctx, connection, "open_dump", config.Inputs, &observation.Session) {
		return
	}
	id := observation.Session.ID
	for _, item := range []struct {
		command string
		result  *core.ExecResult
	}{{"sys", &observation.System}, {"log", &observation.Log}, {"bt", &observation.Backtrace}} {
		if !callTool(t, ctx, connection, "crash_exec", core.ExecRequest{SessionID: id, Command: item.command, TimeoutMS: 120000}, item.result) {
			return
		}
		if item.result.EvidenceID == "" {
			t.Errorf("%s lacks evidence", item.command)
			return
		}
	}
	if !callTool(t, ctx, connection, "get_evidence", core.EvidenceRequest{SessionID: id, EvidenceID: observation.Log.EvidenceID, Limit: 1}, &observation.LogTail) {
		return
	}
	offset := observation.LogTail.Bytes - 65536
	if offset < 0 {
		offset = 0
	}
	if !callTool(t, ctx, connection, "get_evidence", core.EvidenceRequest{SessionID: id, EvidenceID: observation.Log.EvidenceID, Offset: offset}, &observation.LogTail) {
		return
	}
	pattern := regexp.MustCompile(`\[exception RIP: ([A-Za-z_][A-Za-z_0-9]*)(?:\+([0-9]+))?\]`)
	match := pattern.FindStringSubmatch(observation.Backtrace.Output)
	if match == nil {
		t.Errorf("case has no supported exception symbol")
		return
	}
	address := match[1]
	if match[2] != "" {
		offset, err := strconv.ParseUint(match[2], 10, 64)
		if err != nil {
			t.Errorf("exception offset: %v", err)
			return
		}
		address += fmt.Sprintf("+0x%x", offset)
	}
	if !callTool(t, ctx, connection, "crash_exec", core.ExecRequest{SessionID: id, Command: "dis " + address + " 8"}, &observation.Disassembly) {
		return
	}
	if !callTool(t, ctx, connection, "crash_exec", core.ExecRequest{SessionID: id, Command: "dis -l " + address + " 8"}, &observation.SourceDisassembly) {
		return
	}
	if !strings.Contains(observation.Disassembly.Output, match[1]) {
		t.Errorf("disassembly does not identify exception function")
	}
	if !callTool(t, ctx, connection, "search_source", core.SearchRequest{SessionID: id, Pattern: match[1], Path: config.AnalysisPath, MaxResults: 10}, &observation.Search) {
		return
	}
	if len(observation.Search.Matches) == 0 {
		t.Errorf("exception symbol absent from supplied source")
		return
	}
	source := observation.Search.Matches[0]
	if !callTool(t, ctx, connection, "read_source", core.SourceRequest{SessionID: id, File: source.File, StartLine: source.Line, EndLine: source.Line + 200}, &observation.Source) {
		return
	}
	if observation.Source.EvidenceID == "" || observation.Disassembly.EvidenceID == "" {
		t.Errorf("source/disassembly evidence absent")
	}
	var summary core.AnalysisStepResult
	if !callTool(t, ctx, connection, "record_analysis_step", core.AnalysisStepRequest{
		SessionID: id, Title: "读取异常现场并对照源码",
		Summary:     fmt.Sprintf("执行 sys、log、bt，读取日志尾部；定位到异常函数 %s，执行反汇编和源码搜索读取。本轮验证了证据获取和保存，尚未分析相关内存对象或变量形成过程。", match[1]),
		EvidenceIDs: []string{observation.System.EvidenceID, observation.Log.EvidenceID, observation.Backtrace.EvidenceID, observation.Disassembly.EvidenceID, observation.Source.EvidenceID},
	}, &summary) {
		return
	}
	if artifact := os.Getenv("CORESUGAR_CASE_ARTIFACT"); artifact != "" {
		data, err := json.MarshalIndent(observation, "", "  ")
		if err == nil {
			err = os.WriteFile(artifact, append(data, '\n'), 0o600)
		}
		if err != nil {
			t.Errorf("write explicit observation artifact: %v", err)
		}
	}
	var closed core.SessionInfo
	if !callTool(t, ctx, connection, "close_dump", map[string]string{"session_id": id}, &closed) {
		return
	}
	if !callTool(t, ctx, connection, "record_analysis_step", core.AnalysisStepRequest{
		SessionID: id, Title: "结束本轮工程验证",
		Summary: "已完成测试预设的工具调用并关闭会话。本轮停止由固定测试流程决定，不表示 Agent 已自主调查完毕，也不表示根因已经查清。",
	}, &summary) {
		return
	}
	if summary.Step != 2 {
		t.Errorf("summary steps: %+v", summary)
	}
	// Local Linux integration can inspect the server's persisted log directly.
	if os.Getenv("CORESUGAR_MCP_SSH") == "" {
		data, err := os.ReadFile(filepath.Join(config.Directory, "coresugar-work/evidence", id, "operations.jsonl"))
		if err != nil {
			t.Errorf("operation log: %v", err)
			return
		}
		summaryData, err := os.ReadFile(summary.Path)
		if err != nil || !strings.Contains(string(summaryData), match[1]) || !strings.Contains(string(summaryData), "2. **结束本轮工程验证**") || !strings.Contains(string(summaryData), observation.Backtrace.EvidenceID) {
			t.Errorf("readable summary: %s, %v", summaryData, err)
		}
		expected := []string{"open_dump", "crash_exec", "crash_exec", "crash_exec", "get_evidence", "get_evidence", "crash_exec", "crash_exec", "search_source", "read_source", "record_analysis_step", "close_dump", "record_analysis_step"}
		var finished []string
		starts := make(map[string]bool)
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
			var record core.OperationRecord
			if err := json.Unmarshal(line, &record); err != nil {
				t.Errorf("operation log JSON: %v", err)
				return
			}
			if record.SessionID != id || record.Client != "coresugar-analysis-check" {
				t.Errorf("operation identity: %+v", record)
			}
			if record.Phase == "started" {
				starts[record.CallID] = true
			} else if record.Phase == "finished" {
				if record.Status != "OK" || (record.Tool != "open_dump" && !starts[record.CallID]) {
					t.Errorf("unpaired or failed operation: %+v", record)
				}
				delete(starts, record.CallID)
				finished = append(finished, record.Tool)
			} else {
				t.Errorf("unknown log phase: %s", record.Phase)
			}
		}
		if strings.Join(finished, ",") != strings.Join(expected, ",") || len(starts) != 0 {
			t.Errorf("operation sequence: %v, unfinished: %v", finished, starts)
		}
	}
}
