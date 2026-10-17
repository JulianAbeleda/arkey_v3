package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// session drives Serve the way a client does: one JSON-RPC line in, the
// replies read back by id.
type session struct {
	t   *testing.T
	in  *io.PipeWriter
	out *bufio.Scanner
}

func start(t *testing.T, run Runner) *session {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() {
		_ = Serve(context.Background(), inR, outW, "test", run)
		outW.Close()
	}()
	t.Cleanup(func() { inW.Close() })
	return &session{t: t, in: inW, out: bufio.NewScanner(outR)}
}

func (s *session) send(v string) {
	if _, err := io.WriteString(s.in, v+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *session) reply() map[string]any {
	done := make(chan bool, 1)
	go func() { done <- s.out.Scan() }()
	select {
	case ok := <-done:
		if !ok {
			s.t.Fatal("server closed")
		}
	case <-time.After(5 * time.Second):
		s.t.Fatal("no reply")
	}
	var m map[string]any
	if err := json.Unmarshal(s.out.Bytes(), &m); err != nil {
		s.t.Fatal(err)
	}
	return m
}

func TestHandshakeListsTheOneTool(t *testing.T) {
	s := start(t, nil)
	s.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
	init := s.reply()["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("initialize = %v", init)
	}
	s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	s.send(`{"jsonrpc":"2.0","id":"two","method":"tools/list"}`)
	r := s.reply()
	if r["id"] != "two" {
		t.Fatalf("a notification was answered, or the id changed: %v", r)
	}
	tools := r["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != Tool {
		t.Fatalf("tools = %v", tools)
	}
	s.send(`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	if r := s.reply(); r["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("unknown method = %v", r)
	}
}

func TestCallReturnsTheAnswerAndItsSession(t *testing.T) {
	s := start(t, func(_ context.Context, task Task) (Answer, error) {
		if task.Task != "summarize" || task.SessionID != "session-1" {
			return Answer{}, errors.New("arguments did not arrive")
		}
		return Answer{Text: "done", SessionID: "session-1"}, nil
	})
	s.send(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"deepseek_harness","arguments":{"task":"summarize","session_id":"session-1"}}}`)
	result := s.reply()["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if result["isError"] == true || text != "done\n\nsession_id: session-1" {
		t.Fatalf("result = %v", result)
	}
}

func TestFailuresAreToolErrorsNotProtocolErrors(t *testing.T) {
	s := start(t, func(context.Context, Task) (Answer, error) { return Answer{}, errors.New("gateway down") })
	s.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deepseek_harness","arguments":{"task":"x"}}}`)
	result := s.reply()["result"].(map[string]any)
	if result["isError"] != true || !strings.Contains(result["content"].([]any)[0].(map[string]any)["text"].(string), "gateway down") {
		t.Fatalf("result = %v", result)
	}
	s.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deepseek_harness","arguments":{}}}`)
	if result := s.reply()["result"].(map[string]any); result["isError"] != true {
		t.Fatalf("an empty task ran: %v", result)
	}
}

// A canceled call stops its run and gets no reply; the server goes on.
func TestCancelStopsTheRun(t *testing.T) {
	stopped := make(chan struct{})
	s := start(t, func(ctx context.Context, task Task) (Answer, error) {
		if task.Task == "quick" {
			return Answer{Text: "quick"}, nil
		}
		<-ctx.Done()
		close(stopped)
		return Answer{}, ctx.Err()
	})
	s.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deepseek_harness","arguments":{"task":"slow"}}}`)
	time.Sleep(50 * time.Millisecond)
	s.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not reach the run")
	}
	s.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deepseek_harness","arguments":{"task":"quick"}}}`)
	if r := s.reply(); r["id"] != float64(2) {
		t.Fatalf("the canceled call was answered: %v", r)
	}
}

func TestReadFollowsTheEventStream(t *testing.T) {
	stream := `{"type":"session","sessionId":"session-9","cwd":"/w"}
{"type":"status","phase":"turn_start","turn":1}
not json
{"type":"text","text":"rea"}
{"type":"final","text":"ready"}
`
	answer, failure := read(strings.NewReader(stream))
	if failure != "" || answer.Text != "ready" || answer.SessionID != "session-9" {
		t.Fatalf("answer = %+v, failure = %q", answer, failure)
	}
	_, failure = read(strings.NewReader(`{"type":"error","message":"unknown session"}` + "\n"))
	if failure != "unknown session" {
		t.Fatalf("failure = %q", failure)
	}
}
