package components

import (
	"github.com/charmbracelet/lipgloss"
)

// Task list item styles
var (
	sidebarItemStyle  = lipgloss.NewStyle()
	selectedItemStyle = lipgloss.NewStyle().
				Background(ColorGreen).
				Foreground(lipgloss.AdaptiveColor{Light: "15", Dark: "0"})
)
