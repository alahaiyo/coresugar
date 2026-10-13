package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"coresugar/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func operationLogging(runtime *core.Runtime) mcp.Middleware {
	runID := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	var sequence atomic.Uint64
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, request)
			}
			params := request.GetParams().(*mcp.CallToolParamsRaw)
			started := time.Now()
			record := core.OperationRecord{CallID: fmt.Sprintf("%s-%d", runID, sequence.Add(1)), Timestamp: started.UTC(), Phase: "started", Tool: params.Name, Arguments: params.Arguments}
			var identity struct {
				SessionID string `json:"session_id"`
			}
			json.Unmarshal(params.Arguments, &identity)
			record.SessionID = identity.SessionID
			if session, ok := request.GetSession().(*mcp.ServerSession); ok && session.InitializeParams() != nil {
				record.Client = session.InitializeParams().ClientInfo.Name
			}
			writeOperation(runtime, record)
			result, err := next(ctx, method, request)
			record.Timestamp = time.Now().UTC()
			record.Phase = "finished"
			record.DurationMS = time.Since(started).Milliseconds()
			record.Status = "OK"
			if err != nil {
				record.Status, record.Error = "ERROR", err.Error()
			} else if response, ok := result.(*mcp.CallToolResult); ok {
				// Keep metadata and evidence IDs; the full payload is already persisted.
				data, _ := json.Marshal(response.StructuredContent)
				var summary map[string]json.RawMessage
				if json.Unmarshal(data, &summary) == nil && summary != nil {
					for _, field := range []string{"output", "output_base64", "content", "matches"} {
						delete(summary, field)
					}
					record.Result, _ = json.Marshal(summary)
					if params.Name == "open_dump" {
						json.Unmarshal(summary["session_id"], &record.SessionID)
					}
				}
				if response.IsError {
					record.Status = "ERROR"
					json.Unmarshal(summary["error"], &record.Error)
					if record.Error == "" {
						for _, content := range response.Content {
							if text, ok := content.(*mcp.TextContent); ok {
								record.Error = text.Text
								break
							}
						}
					}
				}
			}
			if len(record.Error) > 2048 {
				record.Error = strings.ToValidUTF8(record.Error[:2048], "") + " [truncated]"
			}
			writeOperation(runtime, record)
			return result, err
		}
	}
}

func writeOperation(runtime *core.Runtime, record core.OperationRecord) {
	if err := runtime.LogOperation(record); err != nil {
		// Logging must not hide a completed operation or prompt the Agent to retry it.
		fmt.Fprintf(os.Stderr, "OPERATION_LOG_WRITE_FAILED call_id=%s phase=%s: %v\n", record.CallID, record.Phase, err)
	}
}
