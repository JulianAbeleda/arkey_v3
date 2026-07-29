package ui

import "charm.land/lipgloss/v2"

type Theme struct{ Accent, Text, Muted, Good, Warn, Bad, Panel lipgloss.Style }

func NewTheme(dark bool) Theme {
	if !dark {
		return Theme{Accent: lipgloss.NewStyle().Foreground(lipgloss.Color("#315EA8")), Text: lipgloss.NewStyle().Foreground(lipgloss.Color("#20242B")), Muted: lipgloss.NewStyle().Foreground(lipgloss.Color("#68707C")), Good: lipgloss.NewStyle().Foreground(lipgloss.Color("#147D45")), Warn: lipgloss.NewStyle().Foreground(lipgloss.Color("#936300")), Bad: lipgloss.NewStyle().Foreground(lipgloss.Color("#B42318")), Panel: lipgloss.NewStyle().Background(lipgloss.Color("#E7EDF7"))}
	}
	return Theme{Accent: lipgloss.NewStyle().Foreground(lipgloss.Color("#83B6FF")), Text: lipgloss.NewStyle().Foreground(lipgloss.Color("#E8EDF4")), Muted: lipgloss.NewStyle().Foreground(lipgloss.Color("#9AA6B6")), Good: lipgloss.NewStyle().Foreground(lipgloss.Color("#79D89A")), Warn: lipgloss.NewStyle().Foreground(lipgloss.Color("#F4CE79")), Bad: lipgloss.NewStyle().Foreground(lipgloss.Color("#FF9B9B")), Panel: lipgloss.NewStyle().Background(lipgloss.Color("#202938"))}
}
