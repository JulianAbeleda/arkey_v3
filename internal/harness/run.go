package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/dsh"
	"github.com/JulianAbeleda/arkey_v3/internal/gateway"
)

// stderrTail is how much of a failed run's stderr reaches the client. In
// default mode dsh writes reasoning there, so only the end is kept, for the
// error that ended the run.
const stderrTail = 1500

// event is the part of a `dsh headless --json` line the runner reads.
type event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
	Message   string `json:"message"`
}

// DSH runs each task as `dsh headless` from the checkout at Dir, in the
// working directory the client started this server in.
type DSH struct {
	Dir, Patch, Workspace string
	Env                   []string
	// Gateway is the address the patch's provider points at; a run is not
	// started while nothing answers there.
	Gateway string
}

// Run answers one task.
func (d DSH) Run(ctx context.Context, task Task) (Answer, error) {
	if !answers(ctx, d.gateway()) {
		return Answer{}, fmt.Errorf("the local model gateway is not answering on %s. Launch this client from Arkey with a local model loaded, or run `arkey --ensure-local-runtime` and `arkey gateway`", d.gateway())
	}
	plan, err := dsh.Headless(d.Dir, d.Patch, task.SessionID, d.Env)
	if err != nil {
		return Answer{}, err
	}
	cmd := exec.CommandContext(ctx, plan.Binary, plan.Args...)
	cmd.Env = plan.Env
	cmd.Dir = d.Workspace
	cmd.Stdin = strings.NewReader(task.Task)
	// dsh owns child processes of its own (tools, shells): end the group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	var stderr tail
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Answer{}, err
	}
	if err = cmd.Start(); err != nil {
		return Answer{}, err
	}
	answer, failure := read(stdout)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return Answer{}, ctx.Err()
	}
	if failure != "" {
		return Answer{SessionID: answer.SessionID}, errors.New(failure)
	}
	if waitErr != nil || answer.Text == "" {
		msg := "the run ended without an answer"
		if waitErr != nil {
			msg = "the run failed: " + waitErr.Error()
		}
		if t := strings.TrimSpace(stderr.String()); t != "" {
			msg += "\n" + t
		}
		return Answer{SessionID: answer.SessionID}, errors.New(msg)
	}
	return answer, nil
}

func (d DSH) gateway() string {
	if d.Gateway != "" {
		return d.Gateway
	}
	return gateway.DefaultListen
}

// read follows the event stream to its end: the session id, the final
// answer, or the error that ended the run.
func read(r io.Reader) (Answer, string) {
	var answer Answer
	failure := ""
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var e event
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "session":
			answer.SessionID = e.SessionID
		case "final":
			answer.Text = e.Text
		case "error":
			failure = e.Message
		}
	}
	_, _ = io.Copy(io.Discard, r)
	return answer, failure
}

func answers(ctx context.Context, addr string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/v1/models", nil)
	if err != nil {
		return false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// tail keeps the last stderrTail bytes written to it.
type tail struct{ b []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > stderrTail {
		t.b = t.b[len(t.b)-stderrTail:]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(bytes.ToValidUTF8(t.b, nil)) }

// Main is `arkey harness-mcp --dsh DIR --patch FILE`: serve MCP on stdio
// until the client closes it.
func Main(args []string, version string, stderr io.Writer) int {
	d := DSH{Env: os.Environ()}
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			fmt.Fprintln(stderr, "Arkey harness: usage: arkey harness-mcp --dsh DIR --patch FILE")
			return 2
		}
		switch args[i] {
		case "--dsh":
			d.Dir = args[i+1]
		case "--patch":
			d.Patch = args[i+1]
		default:
			fmt.Fprintln(stderr, "Arkey harness: unknown argument", args[i])
			return 2
		}
		i++
	}
	if d.Dir == "" || d.Patch == "" {
		fmt.Fprintln(stderr, "Arkey harness: usage: arkey harness-mcp --dsh DIR --patch FILE")
		return 2
	}
	var err error
	if d.Workspace, err = os.Getwd(); err != nil {
		fmt.Fprintln(stderr, "Arkey harness:", err)
		return 1
	}
	if err = Serve(context.Background(), os.Stdin, os.Stdout, version, d.Run); err != nil {
		fmt.Fprintln(stderr, "Arkey harness:", err)
		return 1
	}
	return 0
}
