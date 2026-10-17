package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// protocolVersions are the MCP revisions this server answers in, newest
// first. A client asking for one of them gets it back; any other gets the
// newest, and decides for itself whether it can go on.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const description = "Hand a self-contained task to DeepSeek Harness, Arkey's shared agent harness, running on the local model in this working directory. " +
	"It reads files, searches and uses its own tools to answer, and refuses any action that would need an approval. " +
	"Use it for a second agent's take, or to delegate research or analysis. " +
	"It returns the agent's final answer and a session_id; pass that session_id back to continue the same conversation."

// Task is one tool call's arguments.
type Task struct {
	Task      string `json:"task"`
	SessionID string `json:"session_id,omitempty"`
}

// Answer is what a run came to.
type Answer struct {
	Text      string
	SessionID string
}

// Runner answers one task. It must return promptly once ctx is canceled.
type Runner func(ctx context.Context, task Task) (Answer, error)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Serve answers newline-delimited JSON-RPC on in until it closes. Each tool
// call runs on its own goroutine, so a cancel notification can reach it;
// replies are written whole, one per line.
func Serve(ctx context.Context, in io.Reader, out io.Writer, version string, run Runner) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		writeMu sync.Mutex
		callsMu sync.Mutex
		calls   = map[string]context.CancelFunc{}
		wg      sync.WaitGroup
	)
	enc := json.NewEncoder(out)
	reply := func(r response) {
		r.JSONRPC = "2.0"
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = enc.Encode(r)
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			reply(response{ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			continue
		}
		notification := len(req.ID) == 0
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			reply(response{ID: req.ID, Result: map[string]any{
				"protocolVersion": negotiate(p.ProtocolVersion),
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "arkey-" + DeepSeekMCP, "title": "Arkey DeepSeek MCP", "version": version},
				"instructions":    "Arkey's shared harness. " + description,
			}})
		case "ping":
			reply(response{ID: req.ID, Result: map[string]any{}})
		case "tools/list":
			reply(response{ID: req.ID, Result: map[string]any{"tools": []any{toolDefinition()}}})
		case "tools/call":
			var p struct {
				Name      string `json:"name"`
				Arguments Task   `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil || p.Name != Tool {
				reply(response{ID: req.ID, Error: &rpcError{-32602, "unknown tool or malformed arguments"}})
				continue
			}
			callCtx, callCancel := context.WithCancel(ctx)
			key := string(req.ID)
			callsMu.Lock()
			calls[key] = callCancel
			callsMu.Unlock()
			wg.Add(1)
			go func(id json.RawMessage, task Task) {
				defer wg.Done()
				defer func() {
					callsMu.Lock()
					delete(calls, key)
					callsMu.Unlock()
					callCancel()
				}()
				result := call(callCtx, run, task)
				if callCtx.Err() != nil && ctx.Err() == nil {
					return // canceled by the client: it expects no reply
				}
				reply(response{ID: id, Result: result})
			}(req.ID, p.Arguments)
		case "notifications/cancelled":
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(req.Params, &p) == nil {
				callsMu.Lock()
				if c := calls[string(p.RequestID)]; c != nil {
					c()
				}
				callsMu.Unlock()
			}
		default:
			if !notification {
				reply(response{ID: req.ID, Error: &rpcError{-32601, "method not found: " + req.Method}})
			}
		}
	}
	cancel()
	wg.Wait()
	return scanner.Err()
}

func negotiate(asked string) string {
	for _, v := range protocolVersions {
		if v == asked {
			return v
		}
	}
	return protocolVersions[0]
}

func toolDefinition() map[string]any {
	return map[string]any{
		"name":        Tool,
		"title":       "DeepSeek Harness",
		"description": description,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":       map[string]any{"type": "string", "description": "the whole task, with the context the agent needs; it does not see this conversation"},
				"session_id": map[string]any{"type": "string", "description": "a session_id an earlier call returned, to continue that conversation"},
			},
			"required": []string{"task"},
		},
		"annotations": map[string]any{"readOnlyHint": false, "openWorldHint": true},
	}
}

func call(ctx context.Context, run Runner, task Task) toolResult {
	if task.Task == "" {
		return failed("deepseek_harness needs a task")
	}
	answer, err := run(ctx, task)
	if err != nil {
		return failed("DeepSeek Harness: " + err.Error())
	}
	text := answer.Text
	if answer.SessionID != "" {
		text += fmt.Sprintf("\n\nsession_id: %s", answer.SessionID)
	}
	return toolResult{Content: []content{{Type: "text", Text: text}}}
}

func failed(text string) toolResult {
	return toolResult{Content: []content{{Type: "text", Text: text}}, IsError: true}
}
