package mcpserver

import (
	"context"

	"coresugar/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SessionRequest struct {
	SessionID string `json:"session_id"`
}

func New(runtime *core.Runtime) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "coresugar", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "open_dump", Description: "Validate explicit input paths and open a persistent crash session."}, func(ctx context.Context, _ *mcp.CallToolRequest, input core.Inputs) (*mcp.CallToolResult, core.SessionInfo, error) {
		result, err := runtime.Open(ctx, input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "crash_exec", Description: "Execute an allowed read-only crash command with bounded time and output."}, func(ctx context.Context, _ *mcp.CallToolRequest, input core.ExecRequest) (*mcp.CallToolResult, core.ExecResult, error) {
		result, err := runtime.Exec(ctx, input)
		if err != nil {
			return nil, result, err
		}
		return &mcp.CallToolResult{IsError: result.Status == "ERROR"}, result, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_session", Description: "Get the state and input validation of a crash session."}, func(_ context.Context, _ *mcp.CallToolRequest, input SessionRequest) (*mcp.CallToolResult, core.SessionInfo, error) {
		result, err := runtime.Get(input.SessionID)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "close_dump", Description: "Close a crash session and release its child process."}, func(_ context.Context, _ *mcp.CallToolRequest, input SessionRequest) (*mcp.CallToolResult, core.SessionInfo, error) {
		result, err := runtime.CloseSession(input.SessionID)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_evidence", Description: "Read a persisted evidence output page by byte offset; available after session closure or server restart."}, func(_ context.Context, _ *mcp.CallToolRequest, input core.EvidenceRequest) (*mcp.CallToolResult, core.EvidencePage, error) {
		result, err := runtime.GetEvidence(input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "read_source", Description: "Read 1..300 numbered source lines under the session source root, recording a file identity and evidence."}, func(_ context.Context, _ *mcp.CallToolRequest, input core.SourceRequest) (*mcp.CallToolResult, core.SourceResult, error) {
		result, err := runtime.ReadSource(input)
		return nil, result, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "search_source", Description: "Search literal text under the session source root with result, scan and time limits."}, func(ctx context.Context, _ *mcp.CallToolRequest, input core.SearchRequest) (*mcp.CallToolResult, core.SearchResult, error) {
		result, err := runtime.SearchSource(ctx, input)
		return nil, result, err
	})
	return server
}

func Run(ctx context.Context, runtime *core.Runtime) error {
	defer runtime.Close()
	return New(runtime).Run(ctx, &mcp.StdioTransport{})
}
