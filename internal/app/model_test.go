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

// This is intentionally conservative: rendered ANSI escapes are absent in the
// plain fallback test environment, and the production renderer measures them.
func visibleWidth(s string) int { return lipgloss.Width(s) }
