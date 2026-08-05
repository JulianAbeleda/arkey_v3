//go:build darwin

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// fakeRunner returns canned output keyed by a substring of the argv, and records
// the exact commands it was asked to run so tests can assert no shell is used.
type fakeRunner struct {
	responses map[string][]byte
	errs      map[string]error
	calls     [][]string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	joined := name + " " + strings.Join(args, " ")
	for key, err := range f.errs {
		if strings.Contains(joined, key) {
			return f.responses[key], err
		}
	}
	for key, out := range f.responses {
		if strings.Contains(joined, key) {
			return out, nil
		}
	}
	return nil, errors.New("unexpected command: " + joined)
}

func TestDarwinInspectorProcessParsesPsFields(t *testing.T) {
	command := "/opt/llama/llama-server --model /models/q.gguf --port 8080"
	runner := &fakeRunner{responses: map[string][]byte{
		"comm=":    []byte("/opt/llama/llama-server\n"),
		"command=": []byte(command + "\n"),
		"lstart=":  []byte("Wed Aug  5 12:16:48 2026   \n"),
	}}
	got, err := DarwinInspector{Runner: runner}.Process(context.Background(), 4321)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.PID != 4321 {
		t.Errorf("PID = %d, want 4321", got.PID)
	}
	if got.Executable != "/opt/llama/llama-server" {
		t.Errorf("Executable = %q", got.Executable)
	}
	sum := sha256.Sum256([]byte(command))
	if got.ArgsFingerprint != hex.EncodeToString(sum[:]) {
		t.Errorf("ArgsFingerprint = %q, want hash of the trimmed command line", got.ArgsFingerprint)
	}
	// Wed Aug 5 2026 12:16:48 UTC-derived Unix seconds; StartTime must be stable
	// and non-zero so the ownership check can distinguish a reused PID.
	if got.StartTime == 0 {
		t.Errorf("StartTime = 0, want the parsed process start time")
	}
	// The identity must be reproducible: a second read yields the same triple.
	again, err := DarwinInspector{Runner: runner}.Process(context.Background(), 4321)
	if err != nil || again != got {
		t.Errorf("Process is not reproducible: %+v vs %+v (err %v)", again, got, err)
	}
}

func TestDarwinInspectorProcessRejectsDeadPID(t *testing.T) {
	// ps prints nothing and exits non-zero for a dead pid; an empty comm field
	// must surface as an error, never a zero-value identity that could match.
	runner := &fakeRunner{
		responses: map[string][]byte{"comm=": []byte("\n")},
		errs:      map[string]error{"comm=": errors.New("exit status 1")},
	}
	if _, err := (DarwinInspector{Runner: runner}).Process(context.Background(), 999999); err == nil {
		t.Fatal("expected error for a nonexistent process")
	}
}

func TestDarwinInspectorProcessRejectsUnparsableStart(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{
		"comm=":    []byte("/bin/x\n"),
		"command=": []byte("/bin/x --flag\n"),
		"lstart=":  []byte("not a date\n"),
	}}
	if _, err := (DarwinInspector{Runner: runner}).Process(context.Background(), 5); err == nil {
		t.Fatal("expected error for an unparsable start time")
	}
}

func TestDarwinInspectorPortOwner(t *testing.T) {
	runner := &fakeRunner{responses: map[string][]byte{"lsof": []byte("2468\n")}}
	pid, err := DarwinInspector{Runner: runner}.PortOwner(context.Background(), 8080)
	if err != nil {
		t.Fatalf("PortOwner: %v", err)
	}
	if pid != 2468 {
		t.Errorf("PortOwner = %d, want 2468", pid)
	}
	// The argv must target the requested port with a listen filter and never
	// invoke a shell.
	last := runner.calls[len(runner.calls)-1]
	if last[0] != "lsof" {
		t.Errorf("expected lsof, ran %v", last)
	}
	if !containsArg(last, "-iTCP:8080") || !containsArg(last, "-sTCP:LISTEN") {
		t.Errorf("lsof argv missing port/listen filter: %v", last)
	}
}

func TestDarwinInspectorPortOwnerNoMatchIsZero(t *testing.T) {
	// lsof exits 1 with empty output when nothing listens; that is "no owner"
	// (0, nil), matching the Linux backend, not an error.
	runner := &fakeRunner{
		responses: map[string][]byte{"lsof": nil},
		errs:      map[string]error{"lsof": errors.New("exit status 1")},
	}
	pid, err := DarwinInspector{Runner: runner}.PortOwner(context.Background(), 9999)
	if err != nil || pid != 0 {
		t.Errorf("PortOwner = (%d, %v), want (0, nil)", pid, err)
	}
}

func TestDirectOnlyServiceIsUnavailable(t *testing.T) {
	svc := DirectOnlyService{}
	if svc.Available(context.Background()) {
		t.Error("DirectOnlyService.Available must be false on macOS")
	}
	if _, err := svc.Start(context.Background(), "unit", nil, "log"); err == nil {
		t.Error("DirectOnlyService.Start must report unavailability")
	}
	if err := svc.Stop(context.Background(), "unit"); err != nil {
		t.Errorf("DirectOnlyService.Stop should be a no-op, got %v", err)
	}
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}
