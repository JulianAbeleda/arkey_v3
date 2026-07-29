package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
	return Config{Server: "/bin/llama", Model: "/m/a.gguf", Vendor: "nvidia", Port: 8080, ContextSize: 32768, LogPath: "/tmp/log"}
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
