package components

import (
	"fmt"
	"glesha/database/model"
	"strings"

	L "glesha/logger"

	"github.com/charmbracelet/lipgloss"
)

// renders minimal file browser based on file catalog metadata we store for each task
func RenderFilesView(
	files []model.FileCatalogRow,
	currentDir string,
	contentCursor int,
	contentOffset int,
	focusOnContent bool,
	width int,
	height int,
	currentTask *model.Task,
) string {
	if currentTask != nil && currentTask.Status == model.TASK_STATUS_QUEUED {
		return "\n\n  " + YellowStyle.Render("Information available after first run.")
	}

	var sb strings.Builder

	sb.WriteString(DimStyle.Render("Files in "))

	sb.WriteString(GreenStyle.Render("./"+currentDir) + "\n")
	rows := []struct {
		name, size, mod string
	}{
		{name: "../", size: "", mod: ""},
	}

	for _, f := range files {
		name := f.Name
		if f.FileType == "dir" {
			name = name + "/"
		}
		size := ""
		if f.FileType == "file" {
			size = L.HumanReadableBytes(uint64(f.SizeBytes), 1)
		}

		rows = append(rows, struct {
			name, size, mod string
		}{
			name: name,
			size: size,
			mod:  f.ModifiedAt.Format("2006-01-02 15:04"),
		})
	}

	maxVisible := max(height-18, 1)

	sizeWidth := 10
	modWidth := 16
	nameWidth := width - sizeWidth - modWidth - 5

	nameColHeaderStyle := lipgloss.NewStyle().Foreground(ColorBlue).Bold(true).Width(nameWidth)
	sizeColHeaderStyle := lipgloss.NewStyle().Foreground(ColorBlue).Bold(true).Width(sizeWidth)
	modColHeaderStyle := lipgloss.NewStyle().Foreground(ColorBlue).Bold(true).Width(modWidth)

	nameRowStyle := lipgloss.NewStyle().Width(nameWidth)
	sizeRowStyle := lipgloss.NewStyle().Width(sizeWidth)
	modRowStyle := lipgloss.NewStyle().Width(modWidth)

	headerLine := lipgloss.JoinHorizontal(lipgloss.Top,
		nameColHeaderStyle.Render("NAME"),
		sizeColHeaderStyle.Render("SIZE"),
		modColHeaderStyle.Render("MODIFIED AT"),
	)
	sb.WriteString(headerLine + "\n")

	end := contentOffset + maxVisible
	for i := contentOffset; i < len(rows) && i < end; i++ {
		row := rows[i]

		nameStr := row.name
		if len(nameStr) > nameWidth {
			nameStr = nameStr[:nameWidth-1] + "..."
		}

		line := lipgloss.JoinHorizontal(lipgloss.Top,
			nameRowStyle.Render(nameStr),
			sizeRowStyle.Render(row.size),
			modRowStyle.Render(row.mod),
		)

		if i == contentCursor && focusOnContent {
			sb.WriteString(selectedItemStyle.Width(width - 2).Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteString("\n")
	}

	if len(rows) > end {
		sb.WriteString(DimStyle.Render(fmt.Sprintf("... %d more files", len(rows)-end)))
	}

	return sb.String()
}
