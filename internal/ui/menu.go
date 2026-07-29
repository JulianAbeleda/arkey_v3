package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

type Item struct {
	Key, Label, Detail, State string
	Disabled                  bool
}
type Screen struct {
	Title, Subtitle, Notice, Error string
	Items                          []Item
	Cursor, Width, Height          int
	BusyLabel                      string
	Dark                           bool
}

func clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	return lipgloss.NewStyle().MaxWidth(width-1).Render(s) + "…"
}
func Render(s Screen) string {
	w := s.Width
	if w <= 0 {
		w = 80
	}
	h := s.Height
	if h <= 0 {
		h = 24
	}
	if w < 24 {
		return clip("Arkey — enlarge terminal", w)
	}
	if s.Error != "" {
		s.Notice = ""
	}
	extraRows := 0
	if s.BusyLabel != "" {
		extraRows += 2
	}
	if s.Notice != "" {
		extraRows += 2
	}
	if s.Error != "" {
		extraRows += 2
	}
	if h < 12+extraRows {
		return clip("Arkey — enlarge terminal", w)
	}
	t := NewTheme(s.Dark)
	frameWidth := w - 1 // Avoid terminals that auto-wrap after writing the last cell.
	inner := frameWidth - 6
	lines := []string{t.Accent.Bold(true).Render(clip("ARKEY · "+s.Title, inner)), t.Muted.Render(clip(s.Subtitle, inner)), ""}
	maxRows := h - 9 - extraRows
	start, end := 0, len(s.Items)
	if end > maxRows {
		itemRows := maxRows
		showRange := maxRows >= 2
		if showRange {
			itemRows--
		}
		start = s.Cursor - itemRows/2
		if start < 0 {
			start = 0
		}
		end = start + itemRows
		if end > len(s.Items) {
			end = len(s.Items)
			start = end - itemRows
		}
		if showRange {
			lines = append(lines, t.Muted.Render(fmt.Sprintf("Items %d–%d of %d", start+1, end, len(s.Items))))
		}
	}
	for i := start; i < end; i++ {
		item := s.Items[i]
		prefix := "  "
		row := t.Text
		if i == s.Cursor {
			prefix = "› "
			row = t.Panel.Bold(true)
		}
		if item.Disabled {
			row = t.Muted
		}
		state := clip(item.State, 16)
		available := inner - 5 - lipgloss.Width(state)
		label := clip(item.Label, available/2)
		detail := clip(item.Detail, available-lipgloss.Width(label)-1)
		line := prefix + item.Key + "  " + label
		if detail != "" {
			line += "  " + t.Muted.Render(detail)
		}
		if state != "" {
			pad := inner - lipgloss.Width(line) - lipgloss.Width(state)
			if pad > 0 {
				line += strings.Repeat(" ", pad)
			}
			line += t.Accent.Render(state)
		}
		if pad := inner - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		lines = append(lines, row.Render(clip(line, inner)))
	}
	if s.BusyLabel != "" {
		lines = append(lines, "", t.Warn.Render(clip(s.BusyLabel, inner)))
	}
	if s.Notice != "" {
		lines = append(lines, "", t.Good.Render(clip(s.Notice, inner)))
	}
	if s.Error != "" {
		lines = append(lines, "", t.Bad.Render(clip(s.Error, inner)))
	}
	lines = append(lines, "", t.Muted.Render(clip("↑/↓ or j/k move · Enter/→ select · ←/Esc/b back · q quit", inner)))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Accent.GetForeground()).
		Padding(1, 2).
		MaxWidth(frameWidth).
		Render(strings.Join(lines, "\n"))
}
