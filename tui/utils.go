package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renders a box with a "title" legend positioned on the top border
// "rounded" parameter controls corner style but only affects the top border for now
func renderBoxWithTitle(title string, content string, width int, height int, borderStyle lipgloss.Style, titleStyle lipgloss.Style, rounded bool) string {
	boxContent := borderStyle.Width(width).Height(height).Render(content)
	lines := strings.Split(boxContent, "\n")

	if len(lines) == 0 {
		return boxContent
	}

	styledTitle := titleStyle.Render(" " + title + " ")
	titleWidth := lipgloss.Width(styledTitle)

	topBorder := lines[0]

	// detect corners from actual rendered border, by default the border is normal
	// FIXME: update this when rounded borders are supported for boxes
	leftCorner := "┌"
	rightCorner := "┐"

	cornerIdx := strings.Index(topBorder, leftCorner)
	if cornerIdx == -1 {
		return boxContent
	}

	// extract ANSI codes surrounding border characters
	openingANSI := topBorder[:cornerIdx]

	closingANSI := ""
	closingIdx := strings.LastIndex(topBorder, "\x1b[0m")
	if closingIdx != -1 {
		closingANSI = topBorder[closingIdx:]
	}

	dashSize := len("─")
	// corner + 2 dashes padding
	startPos := cornerIdx + len(leftCorner) + (dashSize * 2)

	endCornerIdx := strings.Index(topBorder, rightCorner)
	if endCornerIdx == -1 {
		return boxContent
	}

	remainingDashes := ""
	numRemainingDashes := (endCornerIdx - startPos - (titleWidth * dashSize)) / dashSize
	for range numRemainingDashes {
		remainingDashes += "─"
	}

	// use rounded border characters for top left/right corners
	if rounded {
		leftCorner = "╭"
		rightCorner = "╮"
	}

	lines[0] = openingANSI + leftCorner + "──" + closingANSI + styledTitle + openingANSI + remainingDashes + rightCorner + closingANSI

	return strings.Join(lines, "\n")
}
