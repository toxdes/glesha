package components

import (
	"fmt"
	"glesha/database/model"
	"strings"
)

type TaskInfo struct {
	Task   *model.Task
	Upload *model.Upload
}

func RenderTaskList(
	tasks []TaskInfo,
	sidebarCursor int,
	focusOnSidebar bool,
	width int,
) string {
	var sb strings.Builder

	// align with tabs on the content side (1 line to match tab position)
	sb.WriteString("\n")

	for i, info := range tasks {
		t := info.Task
		status := ""
		switch t.Status {
		case model.TASK_STATUS_QUEUED:
			status = "QUEUED"
		case model.TASK_STATUS_ARCHIVE_RUNNING:
			status = "ARCHIVING"
		case model.TASK_STATUS_ARCHIVE_PAUSED, model.TASK_STATUS_UPLOAD_PAUSED:
			status = "PAUSED"
		case model.TASK_STATUS_ARCHIVE_ABORTED, model.TASK_STATUS_UPLOAD_ABORTED:
			status = "ABORTED"
		case model.TASK_STATUS_ARCHIVE_COMPLETED:
			status = "ARCHIVED"
		case model.TASK_STATUS_UPLOAD_RUNNING:
			status = "UPLOADING"
		case model.TASK_STATUS_UPLOAD_COMPLETED:
			status = "DONE"
		}

		msg := fmt.Sprintf("task #%d\n• %s", t.Id, status)

		style := sidebarItemStyle

		if i == sidebarCursor {
			if focusOnSidebar {
				style = selectedItemStyle
			} else {
				style = sidebarItemStyle.Background(ColorGrey)
			}
		}

		// width accounts for borders (2) only, padding is handled by box style
		sb.WriteString(style.Width(width-2).Render(msg) + "\n")
	}

	return sb.String()
}
