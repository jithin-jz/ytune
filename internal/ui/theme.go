package ui

import "charm.land/lipgloss/v2"

// The colors. Change them here to restyle the whole app.
var (
	colorAccent   = lipgloss.Color("#FF5F87") // Focused borders, progress, key names.
	colorBorder   = lipgloss.Color("#585858") // Unfocused borders.
	colorMuted    = lipgloss.Color("#8A8A8A") // Secondary text.
	colorSelected = lipgloss.Color("#FFFFFF") // Text on the highlighted row.
	colorRow      = lipgloss.Color("#444444") // Highlighted row.
	colorRowDim   = lipgloss.Color("#303030") // Highlighted row in an unfocused list.
	colorError    = lipgloss.Color("#FF5F5F") // Error messages.
)

var (
	styleBorder        = lipgloss.NewStyle().Foreground(colorBorder)
	styleBorderFocused = lipgloss.NewStyle().Foreground(colorAccent)
	styleMuted         = lipgloss.NewStyle().Foreground(colorMuted)
	styleKey           = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleProgress      = lipgloss.NewStyle().Foreground(colorAccent)
	styleError         = lipgloss.NewStyle().Foreground(colorError)
	styleRow           = lipgloss.NewStyle().Foreground(colorSelected).Background(colorRow).Bold(true)
	styleRowDim        = lipgloss.NewStyle().Background(colorRowDim)
)
