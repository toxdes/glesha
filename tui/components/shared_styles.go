package components

import "github.com/charmbracelet/lipgloss"

var (
	ColorBlue   = lipgloss.Color("4")
	ColorGreen  = lipgloss.Color("2")
	ColorRed    = lipgloss.Color("1")
	ColorYellow = lipgloss.Color("3")
	ColorWhite  = lipgloss.Color("15")
	ColorGrey   = lipgloss.Color("8")
	ColorPurple = lipgloss.Color("5")
	ColorBlack  = lipgloss.Color("0")
)

var (
	DimStyle    = lipgloss.NewStyle().Foreground(ColorGrey)
	HelpStyle   = lipgloss.NewStyle().Foreground(ColorGrey)
	BlueStyle   = lipgloss.NewStyle().Foreground(ColorBlue)
	YellowStyle = lipgloss.NewStyle().Foreground(ColorYellow)
	GreenStyle  = lipgloss.NewStyle().Foreground(ColorGreen)
	RedStyle    = lipgloss.NewStyle().Foreground(ColorRed)

	InvertedGreenStyle = lipgloss.NewStyle().
				Background(ColorGreen).
				Foreground(ColorBlack)
)
