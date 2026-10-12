package mcpserver

import (
	"context"
	"testing"
	"time"

	"coresugar/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestServer 验证真实 MCP 客户端初始化、Tool 注册、参数错误及业务错误，失败请求不会断开协议连接。
func TestServer(t *testing.T) {
	config := core.DefaultConfig()
	config.AllowedRoots = []string{t.TempDir()}
	config.EvidenceDir = t.TempDir()
	runtime, err := core.NewRuntime(config)
	if err != nil {
		t.Errorf("runtime: %v", err)
		return
	}
	defer runtime.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := New(runtime).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Errorf("server connection: %v", err)
		return
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "coresugar-test", Version: "0.1.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Errorf("client connection: %v", err)
		return
	}
	defer clientSession.Close()
	list, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Errorf("list tools: %v", err)
		return
	}
	if len(list.Tools) != 7 {
		t.Errorf("tool count = %d", len(list.Tools))
	}
	for _, request := range []mcp.CallToolParams{{Name: "get_session", Arguments: map[string]any{"session_id": "cs-not-found"}}, {Name: "crash_exec", Arguments: map[string]any{"session_id": "cs-not-found", "command": "!id"}}} {
		result, err := clientSession.CallTool(ctx, &request)
		if err != nil {
			t.Errorf("tool call protocol error: %v", err)
			continue
		}
		if !result.IsError {
			t.Errorf("invalid request not flagged: %+v", result)
		}
	}
	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "get_session", Arguments: map[string]any{}}); err == nil {
		t.Errorf("missing required argument accepted")
	}
	if _, err := clientSession.ListTools(ctx, nil); err != nil {
		t.Errorf("business error broke connection: %v", err)
	}
}
