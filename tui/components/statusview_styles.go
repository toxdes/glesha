package components

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	tableStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(ColorGrey)

	tableTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorBlue)
)
