package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestNavigationStackAndWrapping(t *testing.T) {
	m := New(nil)
	m.move(-1)
	if got := m.Cursor(); got != 4 {
		t.Fatalf("wrap up = %d, want 4", got)
	}
	m.activate() // Exit is cursor 4, reset to Config exercise.
	m.cursors[mainScreen] = 1
	m.activate()
	if got := m.ScreenName(); got != "CONFIG" {
		t.Fatalf("screen = %q", got)
	}
	m.cursors[configScreen] = 0
	m.activate()
	if got := m.ScreenName(); got != "CONFIG · LOCAL" {
		t.Fatalf("screen = %q", got)
	}
	m.back()
	if got := m.ScreenName(); got != "CONFIG" {
		t.Fatalf("back = %q", got)
	}
	m.back()
	if got := m.ScreenName(); got != "BOOT" {
		t.Fatalf("second back = %q", got)
	}
}

func TestResizeAndLongPathsNeverOverflow(t *testing.T) {
	m := New(nil)
	m.width, m.height = 41, 12
	m.screen = modelsScreen
	m.models = []ModelSummary{{Name: strings.Repeat("very-long-model-name-", 10), Detail: strings.Repeat("detail/", 30), Path: "/models/" + strings.Repeat("nested/", 20) + "weights.gguf"}}
	v := m.View()
	for _, line := range strings.Split(v.Content, "\n") {
		if n := visibleWidth(line); n > m.width {
			t.Fatalf("line width %d exceeds %d: %q", n, m.width, line)
		}
	}
}

func TestWindowSizeMessageIsStateOnly(t *testing.T) {
	m := New(nil)
	got, cmd := m.Update(tea.WindowSizeMsg{Width: 73, Height: 22})
	if cmd != nil {
		t.Fatal("resize must not start I/O")
	}
	updated := got.(Model)
	if updated.width != 73 || updated.height != 22 {
		t.Fatalf("got %dx%d", updated.width, updated.height)
	}
}

func TestResizeBurstCommitsOnlyLatestDimensions(t *testing.T) {
	m := New(nil)
	m.width, m.height = 80, 24
	first, firstCmd := m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
	second, secondCmd := first.(Model).Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	if firstCmd == nil || secondCmd == nil {
		t.Fatal("subsequent resize events should be debounced")
	}
	updated := second.(Model)
	stale, _ := updated.Update(resizeCommitMsg{generation: updated.resizeGeneration - 1, width: 70, height: 20})
	if stale.(Model).width != 80 {
		t.Fatal("stale resize changed dimensions")
	}
	latest, _ := stale.(Model).Update(resizeCommitMsg{generation: updated.resizeGeneration, width: 90, height: 30})
	if latest.(Model).width != 90 || latest.(Model).height != 30 {
		t.Fatal("latest resize was not committed")
	}
}

func TestRootCancellationReachesActiveOperation(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	m := NewWithContext(nil, root)
	_, operation := m.begin()
	cancel()
	select {
	case <-operation.Done():
	default:
		t.Fatal("active operation did not inherit root cancellation")
	}
}

func TestViewIsPureAndStaleEffectsAreIgnored(t *testing.T) {
	m := New(nil)
	m.width = 80
	first, second := m.View().Content, m.View().Content
	if first != second {
		t.Fatal("View changed output without a message")
	}
	m.generation = 2
	got, _ := m.Update(statusRefreshedMsg{generation: 1, status: Status{Workspace: "stale"}})
	if got.(Model).status.Workspace != "" {
		t.Fatal("stale asynchronous result changed the UI")
	}
}

func TestLoadedModelIndicatorAndUnloadBinding(t *testing.T) {
	m := New(nil)
	m.screen = modelsScreen
	m.models = []ModelSummary{{Path: "/models/active.gguf", Name: "active"}, {Path: "/models/other.gguf", Name: "other"}}
	m.status = Status{LocalActive: true, LocalLoaded: true, LoadedModel: "/models/active.gguf", Route: Route{LocalModel: "/models/active.gguf", LocalRuntime: "llama"}}

	items := m.items()
	if items[0].State != "● loaded" || items[1].State != "installed" {
		t.Fatalf("unexpected model indicators: %#v", items)
	}
	if !strings.Contains(m.help(), "d unload") {
		t.Fatalf("model help does not advertise unload: %q", m.help())
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "d", Code: 'd'})
	if cmd == nil || !updated.(Model).busy {
		t.Fatal("d did not begin the unload operation")
	}
	finished, _ := updated.(Model).Update(localUnloadedMsg{generation: updated.(Model).generation, status: Status{Route: Route{LocalModel: "/models/active.gguf", LocalRuntime: "llama"}}})
	result := finished.(Model)
	if result.busy || result.status.LocalLoaded || !strings.Contains(result.notice, "selection is unchanged") {
		t.Fatalf("unexpected unload result: busy=%v loaded=%v notice=%q", result.busy, result.status.LocalLoaded, result.notice)
	}
	if got := result.items()[0].State; got != "selected" {
		t.Fatalf("unloaded model state = %q, want selected", got)
	}
}

func TestUnloadRequiresCursorOnLoadedModel(t *testing.T) {
	m := New(nil)
	m.screen = modelsScreen
	m.cursors[modelsScreen] = 1
	m.models = []ModelSummary{{Path: "/models/active.gguf"}, {Path: "/models/other.gguf"}}
	m.status = Status{LocalActive: true, LocalLoaded: true, LoadedModel: "/models/active.gguf", Route: Route{LocalModel: "/models/active.gguf"}}
	updated, cmd := m.Update(tea.KeyPressMsg{Text: "d", Code: 'd'})
	if cmd != nil || updated.(Model).busy || !strings.Contains(updated.(Model).notice, "loaded model") {
		t.Fatalf("wrong model was allowed to unload: %#v", updated)
	}
}

func TestStartingModelIsShownWithoutClaimingItIsLoaded(t *testing.T) {
	m := New(nil)
	m.screen = modelsScreen
	m.models = []ModelSummary{{Path: "/models/selected.gguf"}, {Path: "/models/starting.gguf"}}
	m.status = Status{
		LocalActive: true,
		LoadedModel: "/models/starting.gguf",
		Route:       Route{LocalModel: "/models/selected.gguf"},
	}
	items := m.items()
	if items[0].State != "selected" || items[1].State != "◐ starting" {
		t.Fatalf("unexpected transitional indicators: %#v", items)
	}
}

func TestTUIScreenShowsFourExternalHarnesses(t *testing.T) {
	m := New(nil)
	m.screen = tuiScreen
	m.status = Status{Client: "codex", Clients: map[string]string{
		"codex": "ready", "claude": "bridge ingress pending", "kimi": "incomplete", "crush": "ready",
	}}
	items := m.items()
	if len(items) != 4 {
		t.Fatalf("TUI items = %d, want 4", len(items))
	}
	if items[0].Label != "Arkey Codex (modded harness)" || items[1].Label != "Arkey Claude (modded harness)" ||
		items[2].Label != "Arkey Kimi (modded harness)" || items[3].Label != "Arkey Crush (modded harness)" {
		t.Fatalf("unexpected TUI labels: %#v", items)
	}
	if items[0].State != "selected · ready" || !items[1].Disabled || !items[2].Disabled {
		t.Fatalf("unexpected TUI states: %#v", items)
	}
	if items[3].Disabled || items[3].State != "ready" {
		t.Fatalf("snapshotted Crush must be selectable: %#v", items[3])
	}
}

func TestRefreshModelsInPlaceAndPreservesHighlightedModel(t *testing.T) {
	m := New(nil)
	m.screen = modelsScreen
	m.stack = []screen{localScreen}
	m.cursors[modelsScreen] = 1
	m.models = []ModelSummary{
		{Path: "/models/old.gguf", Name: "old"},
		{Path: "/models/keep.gguf", Name: "keep"},
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "r", Code: 'r'})
	refreshing := updated.(Model)
	if cmd == nil || !refreshing.busy {
		t.Fatal("r did not begin model discovery")
	}
	if !strings.Contains(refreshing.help(), "r refresh") {
		t.Fatalf("model help does not advertise refresh: %q", refreshing.help())
	}

	finished, _ := refreshing.Update(modelsDiscoveredMsg{
		generation: refreshing.generation,
		refresh:    true,
		models: []ModelSummary{
			{Path: "/models/keep.gguf", Name: "keep"},
			{Path: "/models/new.gguf", Name: "new"},
		},
	})
	result := finished.(Model)
	if result.busy || result.screen != modelsScreen || len(result.stack) != 1 {
		t.Fatalf("refresh changed navigation state: busy=%v screen=%v stack=%v", result.busy, result.screen, result.stack)
	}
	if result.cursors[modelsScreen] != 0 || result.models[0].Name != "keep" {
		t.Fatalf("highlight was not preserved: cursor=%d models=%#v", result.cursors[modelsScreen], result.models)
	}
	if result.notice != "Local models refreshed." {
		t.Fatalf("refresh notice = %q", result.notice)
	}
}

// This is intentionally conservative: rendered ANSI escapes are absent in the
// plain fallback test environment, and the production renderer measures them.
func visibleWidth(s string) int { return lipgloss.Width(s) }

type vllmMenuServices struct {
	NopServices
	runtime string
}

func (s *vllmMenuServices) ActivateLocal(_ context.Context, runtime string, _ ModelSummary) (Status, error) {
	s.runtime = runtime
	return Status{}, nil
}
func TestVLLMMenuActivatesVLLM(t *testing.T) {
	s := &vllmMenuServices{}
	m := New(s)
	m.screen = localScreen
	m.cursors[localScreen] = 2
	m.activate()
	if m.modelRuntime != "vllm" {
		t.Fatal("vLLM did not select model discovery")
	}
	if m.items()[2].Label != "vLLM" {
		t.Fatal("missing vLLM entry")
	}
	// The command captures the runtime before asynchronous execution.
	cmd := m.activateLocal(context.Background(), 0, ModelSummary{})
	cmd()
	if s.runtime != "vllm" {
		t.Fatalf("activated %q", s.runtime)
	}
}

func TestConfigOffersServerAndTheServerScreenListsEntries(t *testing.T) {
	m := New(nil)
	m.status.Servers = []ServerSummary{{Label: "Ubuntu · Tailscale", Origin: "http://100.106.46.126:8080", State: "arkey-local · 256k context", Selected: true}}
	m.status.Route = Route{Mode: "server", ServerLabel: "Ubuntu · Tailscale"}
	m.cursors[mainScreen] = 1
	m.activate()
	items := m.items()
	if len(items) != 4 || items[1].Label != "Server" || items[1].State != "selected · Ubuntu · Tailscale" {
		t.Fatalf("config items = %#v", items)
	}
	m.cursors[configScreen] = 1
	m.activate()
	if got := m.ScreenName(); got != "CONFIG · SERVER" {
		t.Fatalf("screen = %q", got)
	}
	rows := m.items()
	if len(rows) != 1 || rows[0].Detail != "http://100.106.46.126:8080" || rows[0].State != "selected · arkey-local · 256k context" {
		t.Fatalf("server rows = %#v", rows)
	}
	if got := m.selectedModel(); got != "arkey-server-llama" {
		t.Fatalf("selected model = %q", got)
	}
	m.cursors[configScreen] = 2
	m.back()
	m.activate()
	if got := m.ScreenName(); got != "CONFIG · FRONTIER" {
		t.Fatalf("frontier moved: %q", got)
	}
}

// GUI and Harness sit between Config and Exit, so 1 TUI and 2 Config keep
// their keys. GUI's one row serves DeepSeek Harness once the checkout is ready.
func TestGUIScreenLaunchesDeepSeekHarnessWhenReady(t *testing.T) {
	m := New(nil)
	main := m.items()
	if len(main) != 5 || main[0].Label != "TUI" || main[1].Label != "Config" || main[2].Label != "GUI" || main[3].Label != "Harness" || main[4].Label != "Exit" {
		t.Fatalf("boot items = %+v", main)
	}
	m.cursors[mainScreen] = 2
	m.activate()
	if got := m.ScreenName(); got != "GUI" {
		t.Fatalf("screen = %q", got)
	}
	if items := m.items(); len(items) != 1 || !items[0].Disabled {
		t.Fatalf("an unready DeepSeek Harness must be disabled: %+v", items)
	}
	if m.activate(); m.LaunchPlan() != nil {
		t.Fatal("a disabled GUI was launched")
	}
	m.status.GUIs = map[string]string{"deepseek-harness": "ready"}
	if cmd := m.activate(); cmd == nil {
		t.Fatal("selecting a ready GUI must end the TUI")
	}
	if plan := m.LaunchPlan(); plan == nil || plan.GUI != "deepseek-harness" || plan.Client != "" {
		t.Fatalf("plan = %+v", plan)
	}
}

type harnessServices struct {
	NopServices
	toggled []string
}

func (h *harnessServices) ToggleHarness(_ context.Context, name string) (Status, error) {
	h.toggled = append(h.toggled, name)
	return Status{Harnesses: map[string]string{name: "off"}}, nil
}

// Harness is its own section: Enter on DeepSeek MCP switches the shared
// harness for every TUI and stays on the screen; it never launches anything.
func TestHarnessScreenTogglesDeepSeekMCP(t *testing.T) {
	services := &harnessServices{}
	m := New(services)
	m.status.Harnesses = map[string]string{"deepseek-mcp": "on"}
	if got := m.items()[3]; got.Label != "Harness" || got.State != "DeepSeek MCP" {
		t.Fatalf("main harness row = %+v", got)
	}
	m.cursors[mainScreen] = 3
	m.activate()
	if got := m.ScreenName(); got != "HARNESS" {
		t.Fatalf("screen = %q", got)
	}
	items := m.items()
	if len(items) != 1 || items[0].Label != "DeepSeek MCP" || items[0].State != "on" || !strings.Contains(items[0].Detail, "shared harness all TUIs use") {
		t.Fatalf("harness items = %+v", items)
	}
	cmd := m.activate()
	if cmd == nil || !m.busy {
		t.Fatal("Enter must start the toggle")
	}
	got, _ := m.Update(harnessToggledMsg{generation: m.generation, status: Status{Harnesses: map[string]string{"deepseek-mcp": "off"}}})
	updated := got.(Model)
	if updated.busy || updated.harnessState("deepseek-mcp") != "off" || !strings.Contains(updated.notice, "off") || updated.LaunchPlan() != nil {
		t.Fatalf("after toggle: busy=%v state=%q notice=%q", updated.busy, updated.harnessState("deepseek-mcp"), updated.notice)
	}
	if updated.items()[0].State != "off" || updated.ScreenName() != "HARNESS" {
		t.Fatal("the toggle must stay on the Harness screen")
	}
}
