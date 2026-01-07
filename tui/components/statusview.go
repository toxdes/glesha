package components

import (
	"context"
	"fmt"
	"glesha/backend/aws"
	"glesha/config"
	"path/filepath"
	"strings"

	L "glesha/logger"

	"github.com/charmbracelet/lipgloss"
)

type Row struct {
	label string
	value string
}

// renders task information
func RenderStatusView(
	ctx context.Context,
	info TaskInfo,
	width int,
) string {
	if info.Task == nil {
		return "No task selected"
	}
	t := info.Task

	labelWidth := 40
	valueWidth := width - labelWidth - 6

	buildSection := func(title string, rows []Row) string {
		labelStyle := lipgloss.NewStyle().Width(labelWidth).Foreground(ColorGrey)
		valueStyle := lipgloss.NewStyle().Width(valueWidth).Foreground(ColorGreen)

		tableContent := ""
		for _, row := range rows {
			line := lipgloss.JoinHorizontal(lipgloss.Top,
				labelStyle.Render(row.label),
				valueStyle.Render(row.value),
			)
			tableContent += line + "\n"
		}

		tableContent = strings.TrimSuffix(tableContent, "\n")

		table := tableTitleStyle.Render(title) + "\n" + tableStyle.Render(tableContent)
		return table
	}

	var sb strings.Builder

	taskRows := []Row{
		{"ID:", fmt.Sprintf("%d", t.Id)},
		{"Input Path:", t.InputPath},
		{"Config:", t.ConfigPath},
		{"Status:", string(t.Status)},
		{"Provider:", t.Provider.String()},
		{"Format:", t.ArchiveFormat.String()},
		{"Total Size:", L.HumanReadableBytes(uint64(t.TotalSize), 2)},
		{"File Count:", fmt.Sprintf("%d", t.TotalFileCount)},
	}
	sb.WriteString(buildSection("TASK DETAILS", taskRows))

	if t.Provider == config.PROVIDER_AWS {
		// task-specific config needs to be loaded
		_ = config.Parse(t.ConfigPath)
		cfg := config.Get()

		if cfg.Aws != nil {
			cost, err := aws.EstimateCost(ctx, uint64(t.TotalSize), "INR")
			if err != nil {
				L.Panic("tui: could not estimate cost for task %d: %w", t.Id, err)
			}
			var costRows []Row
			costKeys := aws.GetAwsStorageClasses()
			for _, k := range costKeys {
				activeMarker := ""
				if aws.AwsStorageClass(cfg.Aws.StorageClass) == k {
					activeMarker = "✓ "
				}
				costRows = append(costRows,
					Row{
						label: activeMarker + aws.GetStorageClassLabel(k),
						value: fmt.Sprintf("%.2f %s", cost[k], "INR"),
					})
			}
			sb.WriteString("\n" + buildSection("EST. STORAGE COST PER YEAR", costRows))
		}
	}

	if info.Upload != nil {
		uploadRows := []Row{
			{"Archive:", filepath.Base(info.Upload.FilePath)},
			{"Compressed:", L.HumanReadableBytes(uint64(info.Upload.FileSize), 2)},
		}
		if info.Upload.Url != nil {
			uploadRows = append(uploadRows, Row{label: "URL:", value: *info.Upload.Url})
		}
		sb.WriteString("\n" + buildSection("UPLOAD DETAILS", uploadRows))
	}

	return sb.String()
}
