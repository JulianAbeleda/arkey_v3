package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testLogDir is a canonical (symlink-free) private directory for llama log
// paths. os.TempDir() itself is a symlink on macOS (/var -> /private/var), which
// the runtime's own symlink guard rejects, so it is resolved once here.
var testLogDir = func() string {
	base, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		panic(err)
	}
	dir, err := os.MkdirTemp(base, "arkey-runtime-test-")
	if err != nil {
		panic(err)
	}
	return dir
}()

type memStore struct {
	s     State
	err   error
	saved bool
}

func (m *memStore) Load(context.Context) (State, error) { return m.s, m.err }
func (m *memStore) Save(_ context.Context, s State) error {
	m.s = s
	m.err = nil
	m.saved = true
	return nil
}
func (m *memStore) Clear(context.Context) error { m.s = State{}; m.err = ErrNoState; return nil }

type fakeInspect struct {
	p      map[int]Process
	models map[int]string
	port   int
}

func (f *fakeInspect) Process(_ context.Context, p int) (Process, error) {
	v, ok := f.p[p]
	if !ok {
		return Process{}, errors.New("gone")
	}
	return v, nil
}
func (f *fakeInspect) PortOwner(context.Context, int) (int, error) { return f.port, nil }
func (f *fakeInspect) LlamaProcess(ctx context.Context, pid int, _ string, _ int) (Process, string, error) {
	process, err := f.Process(ctx, pid)
	if err != nil {
		return Process{}, "", err
	}
	model, ok := f.models[pid]
	if !ok {
		return Process{}, "", errors.New("not an Arkey llama process")
	}
	return process, model, nil
}

type fakeLaunch struct {
	pid     int
	started [][]string
	stopped []int
}

func (f *fakeLaunch) StartDirect(_ context.Context, a []string, _ string) (int, error) {
	f.started = append(f.started, a)
	return f.pid, nil
}
func (f *fakeLaunch) Terminate(_ context.Context, p int) error {
	f.stopped = append(f.stopped, p)
	return nil
}

type fakeHealth struct{ answers []bool }

func (f *fakeHealth) LlamaHealthy(context.Context, int) (bool, error) {
	if len(f.answers) == 0 {
		return false, nil
	}
	x := f.answers[0]
	f.answers = f.answers[1:]
	return x, nil
}

type fakeMoon struct{ err error }

func (f fakeMoon) EnsureLocalRoute(context.Context) error { return f.err }

type fakeBackend struct{ aligned, accelerated bool }

func (f fakeBackend) Aligned(context.Context, string, string) (bool, error) { return f.aligned, nil }
func (f fakeBackend) Accelerated(context.Context, string, string) (bool, error) {
	return f.accelerated, nil
}

type fakeLock struct{}

func (fakeLock) Lock(context.Context) (func() error, error) { return func() error { return nil }, nil }

type fakeSystemd struct {
	pid     int
	stopped bool
}

func (*fakeSystemd) Available(context.Context) bool { return true }
func (s *fakeSystemd) Start(context.Context, string, []string, string) (int, error) {
	return s.pid, nil
}
func (s *fakeSystemd) Stop(context.Context, string) error { s.stopped = true; return nil }
func (s *fakeSystemd) MainPID(context.Context, string) (int, error) {
	return s.pid, nil
}

type instant struct{}

func (instant) After(time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	c <- time.Now()
	return c
}
func setup() (*Controller, *memStore, *fakeInspect, *fakeLaunch) {
	st := &memStore{err: ErrNoState}
	in := &fakeInspect{p: map[int]Process{7: {PID: 7, Executable: "/bin/llama", ArgsFingerprint: "x", StartTime: 9}}}
	la := &fakeLaunch{pid: 7}
	c := &Controller{Store: st, Inspector: in, Launcher: la, Health: &fakeHealth{answers: []bool{false, true}}, MoonBridge: fakeMoon{}, Backend: fakeBackend{true, true}, Lock: fakeLock{}, Clock: instant{}, Attempts: 2}
	return c, st, in, la
}
func cfg() Config {
	return Config{Server: "/bin/llama", Model: "/m/a.gguf", Vendor: "nvidia", Port: 8080, ContextSize: 32768, LogPath: filepath.Join(testLogDir, "log")}
}
func TestStartUsesExplicitLoopbackArgumentsAndCommitsAfterChecks(t *testing.T) {
	c, st, _, la := setup()
	s, rb, e := c.Start(context.Background(), cfg())
	if e != nil || rb != nil {
		t.Fatalf("start: %v rollback %v", e, rb)
	}
	if !st.saved || len(la.started) != 1 {
		t.Fatal("expected committed direct start")
	}
	a := la.started[0]
	if a[0] != "/bin/llama" || a[6] != "127.0.0.1" {
		t.Fatalf("unexpected argv %#v", a)
	}
	if s.StartTime != 9 {
		t.Fatal("start time was not persisted")
	}
}

// A running server cannot change its context window, so a config with a
// different ContextSize must not be satisfied by the healthy running process.
func TestContextSizeChangeForcesRestart(t *testing.T) {
	c, st, in, la := setup()
	running := State{PID: 7, Executable: "/bin/llama", ArgsFingerprint: "x", StartTime: 9,
		Model: "/m/a.gguf", Port: 8080, ContextSize: 32768, Server: "/bin/llama"}
	st.s, st.err = running, nil
	in.p[7] = Process{PID: 7, Executable: "/bin/llama", ArgsFingerprint: "x", StartTime: 9}
	c.Health = &fakeHealth{answers: []bool{true, false, true}}

	same := cfg()
	if !c.matchesHealthy(context.Background(), running, same) {
		t.Fatal("identical context size should reuse the running server")
	}
	grown := cfg()
	grown.ContextSize = 262144
	if c.matchesHealthy(context.Background(), running, grown) {
		t.Fatal("changed context size must not reuse the running server")
	}
	_ = la
}

// The chat-template override must be opt-in: overriding the template for a
// model whose own template is fine would be worse than the bug it fixes.
func TestLlamaArgsChatTemplateIsOptIn(t *testing.T) {
	if got := llamaArgs(cfg()); contains(got, "--chat-template-file") {
		t.Fatalf("template flag must be absent when unset: %#v", got)
	}
	c := cfg()
	c.ChatTemplate = "/tmp/fix.jinja"
	got := llamaArgs(c)
	for i, v := range got {
		if v == "--chat-template-file" {
			if i+1 < len(got) && got[i+1] == "/tmp/fix.jinja" {
				return
			}
			t.Fatalf("template flag has wrong value: %#v", got)
		}
	}
	t.Fatalf("template flag missing when set: %#v", got)
}
func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// llama-server's auto slot count segfaults on load for qwen35-arch models
// (Qwen3.6-27B), so Arkey must always pin a single slot.
func TestLlamaArgsPinSingleSlot(t *testing.T) {
	a := llamaArgs(cfg())
	for i, v := range a {
		if v == "--parallel" {
			if i+1 < len(a) && a[i+1] == "1" {
				return
			}
			t.Fatalf("--parallel not set to 1: %#v", a)
		}
	}
	t.Fatalf("missing --parallel in argv %#v", a)
}
func TestStartRefusesUnmanagedPort(t *testing.T) {
	c, _, in, _ := setup()
	in.port = 99
	_, _, e := c.Start(context.Background(), cfg())
	if !errors.Is(e, ErrUnmanagedPort) {
		t.Fatalf("got %v", e)
	}
}
func TestFailedAccelerationStopsNewProcessAndReportsFailure(t *testing.T) {
	c, st, _, la := setup()
	c.Backend = fakeBackend{true, false}
	_, _, e := c.Start(context.Background(), cfg())
	if !errors.Is(e, ErrAcceleration) {
		t.Fatalf("got %v", e)
	}
	if len(la.stopped) != 1 || st.saved {
		t.Fatalf("must stop failed start and not save: %#v saved=%v", la.stopped, st.saved)
	}
}

func TestInspectorFailureCleansUpNewDirectProcess(t *testing.T) {
	c, _, in, launch := setup()
	delete(in.p, 7)
	_, _, err := c.Start(context.Background(), cfg())
	if err == nil {
		t.Fatal("expected process inspection failure")
	}
	if len(launch.stopped) != 1 || launch.stopped[0] != 7 {
		t.Fatalf("new process was not cleaned up: %#v", launch.stopped)
	}
}

func TestStopRecoversRestartedSystemdPID(t *testing.T) {
	store := &memStore{s: State{PID: 7, Executable: "/bin/llama", ArgsFingerprint: "old", StartTime: 9, Model: "/models/old.gguf", Server: "/bin/llama", Port: 8080, Manager: "systemd"}}
	inspector := &fakeInspect{
		p:      map[int]Process{8: {PID: 8, Executable: "/bin/llama", ArgsFingerprint: "new", StartTime: 10}},
		models: map[int]string{8: "/models/active.gguf"},
	}
	service := &fakeSystemd{pid: 8}
	controller := &Controller{Store: store, Inspector: inspector, Service: service, Lock: fakeLock{}}
	if err := controller.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !service.stopped || !errors.Is(store.err, ErrNoState) {
		t.Fatalf("restarted systemd unit was not stopped and cleared: stopped=%v err=%v", service.stopped, store.err)
	}
}

func TestLoadedReportsActualModelAfterSystemdRestart(t *testing.T) {
	store := &memStore{s: State{PID: 7, Executable: "/bin/llama", ArgsFingerprint: "old", StartTime: 9, Model: "/models/selected.gguf", Server: "/bin/llama", Port: 8080, Manager: "systemd"}}
	inspector := &fakeInspect{
		p:      map[int]Process{8: {PID: 8, Executable: "/bin/llama", ArgsFingerprint: "new", StartTime: 10}},
		models: map[int]string{8: "/models/actual.gguf"},
	}
	service := &fakeSystemd{pid: 8}
	controller := &Controller{Store: store, Inspector: inspector, Service: service, Health: &fakeHealth{answers: []bool{true}}}
	model, loaded, err := controller.Loaded(context.Background(), Config{Server: "/bin/llama", Port: 8080})
	if err != nil || !loaded || model != "/models/actual.gguf" {
		t.Fatalf("model=%q loaded=%v err=%v", model, loaded, err)
	}
}
func TestStopRejectsPidReuse(t *testing.T) {
	c, st, in, la := setup()
	st.s = State{PID: 7, Executable: "/bin/llama", ArgsFingerprint: "old", StartTime: 9}
	st.err = nil
	in.p[7] = Process{PID: 7, Executable: "/bin/llama", ArgsFingerprint: "new", StartTime: 9}
	if e := c.Stop(context.Background()); e == nil {
		t.Fatal("expected ownership failure")
	}
	if len(la.stopped) != 0 {
		t.Fatal("must not signal reused pid")
	}
}
