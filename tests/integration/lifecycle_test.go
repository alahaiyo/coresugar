package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"coresugar/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPLifecycle 验证真实双会话：第三个请求被数量限制拒绝、输出隔离，
// 一个会话关闭后另一个继续可用，以及带活动 crash 会话的 MCP 客户端断开可正常退出。
func TestMCPLifecycle(t *testing.T) {
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
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Errorf("configuration: %v", err)
		return
	}
	args := []string{binary, "serve", "--allowed-root", config.Directory, "--evidence-dir", config.Directory + "/coresugar-work/evidence-lifecycle", "--max-sessions", "2"}
	var command *exec.Cmd
	if host := os.Getenv("CORESUGAR_MCP_SSH"); host != "" {
		command = exec.Command("ssh", append([]string{"-T", "-o", "BatchMode=yes", host}, args...)...)
	} else {
		command = exec.Command(args[0], args[1:]...)
	}
	command.Stderr = os.Stderr
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "coresugar-lifecycle", Version: "0.1.0"}, nil)
	connection, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Errorf("connection: %v", err)
		return
	}
	defer connection.Close()
	var alpha, beta core.SessionInfo
	if !callTool(t, ctx, connection, "open_dump", config.Inputs, &alpha) || !callTool(t, ctx, connection, "open_dump", config.Inputs, &beta) {
		return
	}
	if alpha.ID == beta.ID {
		t.Errorf("sessions share identity")
	}
	third, err := connection.CallTool(ctx, &mcp.CallToolParams{Name: "open_dump", Arguments: config.Inputs})
	if err != nil || !third.IsError {
		t.Errorf("third session limit: %+v %v", third, err)
	}
	for _, id := range []string{alpha.ID, beta.ID} {
		var result core.ExecResult
		if !callTool(t, ctx, connection, "crash_exec", core.ExecRequest{SessionID: id, Command: "sys"}, &result) {
			return
		}
		if result.SessionID != id || result.EvidenceID == "" {
			t.Errorf("session attribution: %+v", result)
		}
	}
	var closed core.SessionInfo
	if !callTool(t, ctx, connection, "close_dump", map[string]string{"session_id": alpha.ID}, &closed) {
		return
	}
	var result core.ExecResult
	if !callTool(t, ctx, connection, "crash_exec", core.ExecRequest{SessionID: beta.ID, Command: "bt"}, &result) {
		return
	}
	// Leave beta open deliberately: server shutdown must clean it up.
	if err := connection.Close(); err != nil {
		t.Errorf("disconnect with active session: %v", err)
	}
}

// TestMCPIdle 验证真实空闲回收：500ms 超时关闭 crash 并过期会话状态，服务仍可响应 Tool 列表。
func TestMCPIdle(t *testing.T) {
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
		Directory string `json:"directory"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Errorf("configuration: %v", err)
		return
	}
	args := []string{binary, "serve", "--allowed-root", config.Directory, "--evidence-dir", config.Directory + "/coresugar-work/evidence-idle", "--idle-timeout", "500ms"}
	var command *exec.Cmd
	if host := os.Getenv("CORESUGAR_MCP_SSH"); host != "" {
		command = exec.Command("ssh", append([]string{"-T", "-o", "BatchMode=yes", host}, args...)...)
	} else {
		command = exec.Command(args[0], args[1:]...)
	}
	command.Stderr = os.Stderr
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "coresugar-idle", Version: "0.1.0"}, nil)
	connection, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Errorf("connection: %v", err)
		return
	}
	defer connection.Close()
	var info core.SessionInfo
	if !callTool(t, ctx, connection, "open_dump", config.Inputs, &info) {
		return
	}
	time.Sleep(2 * time.Second)
	result, err := connection.CallTool(ctx, &mcp.CallToolParams{Name: "get_session", Arguments: map[string]string{"session_id": info.ID}})
	if err != nil || !result.IsError {
		t.Errorf("idle state not expired: %+v %v", result, err)
	}
	if _, err := connection.ListTools(ctx, nil); err != nil {
		t.Errorf("reaping broke service: %v", err)
	}
}
