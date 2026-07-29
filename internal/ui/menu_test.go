package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestRenderNeverExceedsTerminalGeometry(t *testing.T) {
	items := make([]Item, 20)
	for i := range items {
		items[i] = Item{Key: "1", Label: strings.Repeat("model", 12), Detail: strings.Repeat("nested/", 12), State: strings.Repeat("state", 8)}
	}
	cases := []Screen{
		{Width: 41, Height: 12, Items: items},
		{Width: 60, Height: 14, Items: items, BusyLabel: "Working…"},
		{Width: 60, Height: 14, Items: items, Notice: "complete"},
		{Width: 60, Height: 14, Items: items, Error: "failed"},
		{Width: 60, Height: 14, Items: items, BusyLabel: "Working…", Error: "failed"},
	}
	for _, test := range cases {
		rendered := Render(test)
		if got := lipgloss.Height(rendered); got > test.Height {
			t.Fatalf("height %d exceeds terminal height %d", got, test.Height)
		}
		for _, line := range strings.Split(rendered, "\n") {
			if got := lipgloss.Width(line); got > test.Width {
				t.Fatalf("width %d exceeds terminal width %d", got, test.Width)
			}
		}
	}
}

func TestErrorSuppressesNotice(t *testing.T) {
	rendered := Render(Screen{Width: 80, Height: 24, Notice: "success", Error: "failure"})
	if strings.Contains(rendered, "success") || !strings.Contains(rendered, "failure") {
		t.Fatalf("unexpected status rendering %q", rendered)
	}
}
