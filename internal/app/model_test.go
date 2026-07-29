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
	if got := m.Cursor(); got != 2 {
		t.Fatalf("wrap up = %d, want 2", got)
	}
	m.activate() // Exit is cursor 2, reset to Config exercise.
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
