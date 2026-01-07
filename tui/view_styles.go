package tui

import (
	"glesha/tui/components"

	"github.com/charmbracelet/lipgloss"
)

var (
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(components.ColorGrey).
			Padding(0, 1)

	activeBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(components.ColorGreen).
			Padding(0, 1)
)

var (
	tabStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Border(lipgloss.NormalBorder(), true, true, true, true).
			BorderForeground(components.ColorGrey)

	activeTabStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(components.ColorGreen).
			Border(lipgloss.NormalBorder(), true, true, true, true).
			BorderForeground(components.ColorGreen).
			Bold(true)
)

var (
	panelTitleStyle = lipgloss.NewStyle().
			Foreground(components.ColorGrey)

	activePanelTitleStyle = lipgloss.NewStyle().
				Foreground(components.ColorGreen).
				Bold(true)
)
