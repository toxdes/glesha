package components

import (
	"fmt"
	"glesha/database/model"
	"strings"

	L "glesha/logger"
)

func RenderStatusBar(
	info TaskInfo,
	width int,
) string {
	if info.Task == nil {
		return ""
	}
	t := info.Task
	var sb strings.Builder

	// handle task statuses
	switch t.Status {
	case model.TASK_STATUS_QUEUED:
		sb.WriteString(DimStyle.Render("Status | IN QUEUE") + "\n")

	case model.TASK_STATUS_ARCHIVE_RUNNING:
		archPct := float64(t.ArchivedFileCount) * 100.0 / float64(t.TotalFileCount)
		sb.WriteString(fmt.Sprintf("Status | ARCHIVING %s %3.2f%%\n", L.ProgressBar(archPct, width-30), archPct))

	case model.TASK_STATUS_ARCHIVE_PAUSED:
		sb.WriteString(YellowStyle.Render("Status | ARCHIVE PAUSED") + "\n")

	case model.TASK_STATUS_ARCHIVE_ABORTED:
		sb.WriteString(RedStyle.Render("Status | ✗  ARCHIVE ABORTED") + "\n")

	case model.TASK_STATUS_ARCHIVE_COMPLETED:
		sb.WriteString(GreenStyle.Render("Status | ✓  ARCHIVE COMPLETE") + "\n")

	case model.TASK_STATUS_UPLOAD_RUNNING:
		if info.Upload != nil {
			upPct := float64(info.Upload.UploadedBytes) * 100.0 / float64(info.Upload.FileSize)
			sb.WriteString(fmt.Sprintf("Status | UPLOADING %s %3.2f%%", L.ProgressBar(upPct, width-30), upPct))
		}

	case model.TASK_STATUS_UPLOAD_PAUSED:
		sb.WriteString(YellowStyle.Render("Status | UPLOAD PAUSED"))

	case model.TASK_STATUS_UPLOAD_ABORTED:
		sb.WriteString(RedStyle.Render("Status | ✗  UPLOAD ABORTED"))

	case model.TASK_STATUS_UPLOAD_COMPLETED:
		if info.Upload != nil {
			completedTime := info.Upload.CompletedAt.Format("2006-01-02 15:04:05")
			sb.WriteString(GreenStyle.Render(fmt.Sprintf("Status | ✓  UPLOAD COMPLETE (completed at %s)", completedTime)))
		}
	}

	return sb.String()
}
