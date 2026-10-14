package runtime

import (
	"context"
	"errors"
	"testing"
)

func addTestEngine(c *Controller, service Service, health Health) {
	c.Engines = map[string]Engine{"other": {Validate: func(Config) error { return nil }, Args: func(cfg Config) []string { return []string{cfg.Server, "--checkpoint", cfg.Model} }, Backend: fakeBackend{true, true}, Service: service, Health: func(Config) Health { return health }, Unit: "arkey-other"}}
}
func TestSharedControllerSwitchesEnginesAndUnloadsCurrent(t *testing.T) {
	c, st, in, launcher := setup()
	c.Health = &fakeHealth{answers: []bool{true}}
	old := cfg()
	old.ChatTemplate = "old-template"
	if _, _, err := c.Start(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	in.p[8] = Process{PID: 8, Executable: "/bin/python", ArgsFingerprint: "other", StartTime: 10}
	service := &fakeSystemd{pid: 8}
	addTestEngine(c, service, &fakeHealth{answers: []bool{true}})
	next := cfg()
	next.Engine = "other"
	next.Server = "/bin/python"
	next.Model = "/models/checkpoint"
	next.Port = 8000
	if _, _, err := c.Start(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(launcher.stopped) != 1 || launcher.stopped[0] != 7 || stateEngine(st.s) != "other" {
		t.Fatalf("switch did not release previous engine: %+v %+v", launcher.stopped, st.s)
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !service.stopped || st.err != ErrNoState {
		t.Fatal("unload did not use current engine's service")
	}
}
func TestSharedControllerRestoresPreviousEngineAfterHealthFailure(t *testing.T) {
	c, st, in, launcher := setup()
	c.Health = &fakeHealth{answers: []bool{true, true}}
	old := cfg()
	old.ChatTemplate = "old-template"
	if _, _, err := c.Start(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	in.p[8] = Process{PID: 8, Executable: "/bin/python", ArgsFingerprint: "other", StartTime: 10}
	service := &fakeSystemd{pid: 8}
	addTestEngine(c, service, &fakeHealth{answers: []bool{false, false}})
	next := cfg()
	next.Engine = "other"
	next.Server = "/bin/python"
	next.Model = "/bad/checkpoint"
	next.Port = 8000
	_, rollback, err := c.Start(context.Background(), next)
	if !errors.Is(err, ErrHealth) || rollback != nil {
		t.Fatalf("failure=%v rollback=%v", err, rollback)
	}
	if !service.stopped || stateEngine(st.s) != "" || st.s.Port != old.Port || st.s.LaunchConfig.ChatTemplate != old.ChatTemplate {
		t.Fatalf("wrong rollback %+v", st.s)
	}
	if len(launcher.started) != 2 {
		t.Fatal("previous engine was not restarted")
	}
}
func TestSwitchRefusesUnmanagedTargetPortBeforeStoppingPrevious(t *testing.T) {
	c, _, in, launcher := setup()
	c.Health = &fakeHealth{answers: []bool{true}}
	if _, _, err := c.Start(context.Background(), cfg()); err != nil {
		t.Fatal(err)
	}
	addTestEngine(c, &fakeSystemd{pid: 8}, &fakeHealth{})
	in.port = 99
	next := cfg()
	next.Engine = "other"
	next.Port = 8000
	if _, _, err := c.Start(context.Background(), next); !errors.Is(err, ErrUnmanagedPort) {
		t.Fatal(err)
	}
	if len(launcher.stopped) != 0 {
		t.Fatal("stopped previous engine despite unmanaged target port")
	}
}
func TestLaunchConfigTreatsEmptyCollectionsEqually(t *testing.T) {
	a := cfg()
	b := cfg()
	b.Environment = map[string]string{}
	b.ExtraArgs = []string{}
	if !sameLaunchConfig(a, b) {
		t.Fatal("serialization of empty collections causes unnecessary restart")
	}
	b.ExtraArgs = []string{"--enforce-eager"}
	if sameLaunchConfig(a, b) {
		t.Fatal("argument changes must restart")
	}
}

func TestRequiredServiceNeverFallsBackToDirect(t *testing.T) {
	c, _, _, launcher := setup()
	addTestEngine(c, nil, &fakeHealth{})
	e := c.Engines["other"]
	e.RequireService = true
	c.Engines["other"] = e
	next := cfg()
	next.Engine = "other"
	if _, _, err := c.Start(context.Background(), next); err == nil {
		t.Fatal("uncontained start accepted")
	}
	if len(launcher.started) > 0 {
		t.Fatal("fell back to direct launch")
	}
}

type sizedHealth struct{ window int }

func (h sizedHealth) Healthy(context.Context, int) (bool, error)      { return true, nil }
func (h sizedHealth) ContextWindow(context.Context, int) (int, error) { return h.window, nil }

func TestAutoContextPersistsResolvedWindowAndReusesProcess(t *testing.T) {
	c, store, _, launcher := setup()
	addTestEngine(c, nil, sizedHealth{65536})
	next := cfg()
	next.Engine = "other"
	next.ContextSize = 0
	state, _, err := c.Start(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if state.ContextSize != 65536 || store.s.LaunchConfig.ContextSize != 0 {
		t.Fatalf("lost request/resolved distinction: %+v", state)
	}
	if _, _, err := c.Start(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(launcher.started) != 1 {
		t.Fatal("auto-sized server restarted despite unchanged request")
	}
}

func TestAutoContextRefusesMissingWindowAndCleansUp(t *testing.T) {
	c, _, _, launcher := setup()
	addTestEngine(c, nil, sizedHealth{0})
	next := cfg()
	next.Engine = "other"
	next.ContextSize = 0
	if _, _, err := c.Start(context.Background(), next); err == nil {
		t.Fatal("missing context accepted")
	}
	if len(launcher.stopped) != 1 {
		t.Fatal("failed auto-sized process was not released")
	}
}
