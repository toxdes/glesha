package tui

import (
	"glesha/database/model"
	"glesha/tui/components"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m modelTui) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	sidebarWidth := 30
	// account for both panels' borders (2+2)
	contentWidth := m.width - sidebarWidth - 4
	// account for borders (2 lines) and footer (1 line)
	mainHeight := m.height - 3

	sidebarContent := components.RenderTaskList(
		m.tasks,
		m.sidebarCursor,
		m.focus == focusSidebar,
		sidebarWidth,
	)

	sidebarTitle := "[1] Tasks"
	sidebarBorderStyle := boxStyle
	sidebarTitleStyle := panelTitleStyle
	if m.focus == focusSidebar {
		sidebarBorderStyle = activeBoxStyle
		sidebarTitleStyle = activePanelTitleStyle
	}
	sidebarBox := renderBoxWithTitle(sidebarTitle, sidebarContent, sidebarWidth, mainHeight, sidebarBorderStyle, sidebarTitleStyle, true)

	var cb strings.Builder

	statusLabel := "Status"
	filesLabel := "Files"
	var statusTab, filesTab string

	if m.focus == focusContent {
		statusLabel = "[3] Status"
		filesLabel = "[4] Files"
		if m.activeTab == tabStatus {
			statusTab = activeTabStyle.Render(statusLabel)
			filesTab = tabStyle.Foreground(components.ColorGrey).Render(filesLabel)
		} else {
			statusTab = tabStyle.Foreground(components.ColorGrey).Render(statusLabel)
			filesTab = activeTabStyle.Render(filesLabel)
		}
	} else {
		statusTab = tabStyle.Foreground(components.ColorGrey).Render(statusLabel)
		filesTab = tabStyle.Foreground(components.ColorGrey).Render(filesLabel)
	}
	cb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, statusTab, filesTab) + "\n")

	var currentTask components.TaskInfo
	for _, t := range m.tasks {
		if t.Task.Id == m.selectedTaskId {
			currentTask = t
			break
		}
	}

	if m.activeTab == tabStatus {
		statusContent := components.RenderStatusView(
			m.ctx,
			currentTask,
			contentWidth,
		)
		cb.WriteString(statusContent)
	} else {
		var currentTaskModel *model.Task
		if currentTask.Task != nil {
			currentTaskModel = currentTask.Task
		}

		filesContent := components.RenderFilesView(
			m.files,
			m.currentDir,
			m.contentCursor,
			m.contentOffset,
			m.focus == focusContent,
			contentWidth,
			m.height,
			currentTaskModel,
		)
		cb.WriteString(filesContent)
	}

	statusBar := components.RenderStatusBar(
		currentTask,
		contentWidth,
	)
	cb.WriteString("\n" + statusBar)

	contentTitle := "[2] Content"
	contentBorderStyle := boxStyle
	contentTitleStyle := panelTitleStyle
	if m.focus == focusContent {
		contentBorderStyle = activeBoxStyle
		contentTitleStyle = activePanelTitleStyle
	}
	contentBox := renderBoxWithTitle(contentTitle, cb.String(), contentWidth, mainHeight, contentBorderStyle, contentTitleStyle, true)

	footer := components.HelpStyle.Width(m.width).Align(lipgloss.Center).Render("1:Tasks | 2:Content | Tab:Toggle | 3/s:Status | 4/f:Files | j/k/arrow keys:Navigate | q:Quit")

	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, sidebarBox, contentBox),
		footer,
	)
}
