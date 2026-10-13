package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"coresugar/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestOperationLogging 验证 MCP 调用开始时已落盘、成功打开后归入会话日志，
// 结果保留证据编号及截断状态且省略大输出；工具执行错误及日志写入失败不破坏原始响应。
func TestOperationLogging(t *testing.T) {
	config := core.DefaultConfig()
	config.AllowedRoots = []string{t.TempDir()}
	config.EvidenceDir = t.TempDir()
	runtime, err := core.NewRuntime(config)
	if err != nil {
		t.Errorf("runtime: %v", err)
		return
	}
	t.Cleanup(runtime.Close)
	id := "cs-io-cost-analysis"
	if err := os.Mkdir(filepath.Join(config.EvidenceDir, id), 0o700); err != nil {
		t.Errorf("session directory: %v", err)
		return
	}
	server := New(runtime)
	mcp.AddTool(server, &mcp.Tool{Name: "open_dump"}, func(context.Context, *mcp.CallToolRequest, core.Inputs) (*mcp.CallToolResult, core.SessionInfo, error) {
		data, err := os.ReadFile(filepath.Join(config.EvidenceDir, "operations.jsonl"))
		if err != nil || !bytes.Contains(data, []byte(`"phase":"started"`)) {
			t.Errorf("call start not persisted before execution: %s, %v", data, err)
		}
		return nil, core.SessionInfo{ID: id, Status: "READY"}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "crash_exec"}, func(context.Context, *mcp.CallToolRequest, core.ExecRequest) (*mcp.CallToolResult, core.ExecResult, error) {
		return &mcp.CallToolResult{IsError: true}, core.ExecResult{SessionID: id, Status: "ERROR", Error: "command timed out", ErrorCode: "COMMAND_TIMEOUT", EvidenceID: "ev-timeout", Output: strings.Repeat("kernel output", 5000), Truncated: true}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Errorf("server connection: %v", err)
		return
	}
	defer serverSession.Close()
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "analysis-client", Version: "0.1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Errorf("client connection: %v", err)
		return
	}
	defer clientSession.Close()
	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "open_dump", Arguments: core.Inputs{VMCore: "/case/vmcore", VMLinux: "/case/vmlinux", SourceDir: "/case/source"}}); err != nil {
		t.Errorf("open call: %v", err)
		return
	}
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "crash_exec", Arguments: core.ExecRequest{SessionID: id, Command: "bt"}})
	if err != nil || !result.IsError {
		t.Errorf("original tool error changed: %+v, %v", result, err)
		return
	}
	data, err := os.ReadFile(filepath.Join(config.EvidenceDir, id, "operations.jsonl"))
	if err != nil {
		t.Errorf("session log: %v", err)
		return
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 3 {
		t.Errorf("open result and command start/finish expected: %d", len(lines))
		return
	}
	var finished core.OperationRecord
	if err := json.Unmarshal(lines[len(lines)-1], &finished); err != nil {
		t.Errorf("log JSON: %v", err)
		return
	}
	if finished.Status != "ERROR" || finished.Error != "command timed out" || !bytes.Contains(finished.Result, []byte("ev-timeout")) || !bytes.Contains(finished.Result, []byte(`"truncated":true`)) || bytes.Contains(finished.Result, []byte(`"output"`)) {
		t.Errorf("incorrect result summary: %+v", finished)
	}
	var started core.OperationRecord
	json.Unmarshal(lines[len(lines)-2], &started)
	if started.CallID != finished.CallID || started.Timestamp.After(finished.Timestamp) || !bytes.Contains(started.Arguments, []byte(`"command":"bt"`)) {
		t.Errorf("cannot pair start/finish: %+v, %+v", started, finished)
	}
	// Simulate an unavailable log path without relying on filesystem permissions (root tests).
	path := filepath.Join(config.EvidenceDir, "operations.jsonl")
	if err := os.Remove(path); err != nil {
		t.Errorf("remove index: %v", err)
		return
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Errorf("block log path: %v", err)
		return
	}
	result, err = clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "crash_exec", Arguments: core.ExecRequest{SessionID: id, Command: "bt"}})
	if err != nil || !result.IsError || result.StructuredContent == nil {
		t.Errorf("logging failure hid tool response: %+v, %v", result, err)
	}
}
