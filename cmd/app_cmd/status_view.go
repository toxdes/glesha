package app_cmd

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/term"

	"glesha/backup"
	"glesha/database/model"
	"glesha/file_io"
	L "glesha/logger"
)

func status(r *runtime, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("cli: status accepts zero or one set")
	}
	format := r.env.Format
	if format == "" {
		format = "plain"
	}
	if r.env.JSON {
		if format != "plain" && format != "json" {
			return fmt.Errorf("cli: --json conflicts with --format")
		}
		format = "json"
	}
	switch format {
	case "plain", "table", "csv", "json":
	default:
		return fmt.Errorf("cli: format must be plain, table, csv or json")
	}
	tracker, err := file_io.Temp(r.root)
	if err != nil {
		return err
	}
	defer os.Remove(tracker.Name())
	if err = tracker.Close(); err != nil {
		return err
	}
	r.statusCopies, err = sql.Open("sqlite", tracker.Name())
	if err != nil {
		return err
	}
	defer r.statusCopies.Close()
	defer func() { r.statusCopies = nil }()
	r.statusArchiveBytes, r.statusArchiveCost = 0, 0
	if _, err = r.statusCopies.ExecContext(r.ctx, "PRAGMA cache_size=-256; CREATE TABLE copies(provider TEXT,key TEXT,version TEXT,PRIMARY KEY(provider,key,version))"); err != nil {
		return err
	}
	rows := []statusData{}
	var failures []error
	collect := func(set model.Set) error {
		d, err := collectStatus(r, set)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", set.Name, err))
			d.Set = set
			d.Condition = backup.Condition{State: backup.STATE_FAILED, Reason: err.Error(), Action: "Inspect the error before retrying."}
			d.EstimateComplete = false
		}
		rows = append(rows, d)
		return nil
	}
	if len(args) == 1 {
		set, err := r.registry.Set(r.ctx, args[0])
		if err != nil {
			return err
		}
		if err = collect(set); err != nil {
			return err
		}
	} else if err := r.registry.Each(r.ctx, collect); err != nil {
		return err
	}
	if format == "json" {
		r.env.JSON = true
		for _, d := range rows {
			if err := r.emit("status", d, ""); err != nil {
				return err
			}
		}
		return errors.Join(failures...)
	}
	if format == "csv" {
		writer := csv.NewWriter(r.out)
		writer.Write([]string{"set", "state", "archive_bytes", "snapshots", "storage_class", "estimated_annual_usd", "estimate_complete"})
		for _, d := range rows {
			writer.Write([]string{d.Set.Name, d.Condition.State.String(), strconv.FormatInt(d.ArchiveBytes, 10), strconv.FormatInt(d.Snapshots, 10), dominantClass(d.Classes), strconv.FormatFloat(d.ArchiveAnnualUSD+d.MetadataAnnualUSD, 'f', 6, 64), strconv.FormatBool(d.EstimateComplete)})
		}
		writer.Flush()
		return errors.Join(writer.Error(), errors.Join(failures...))
	}
	cells := [][]string{{"SET", "STATE", "SIZE", "SNAPSHOTS", "STORAGE CLASS", "EST/YR"}}
	total := statusData{EstimateComplete: true}
	for _, d := range rows {
		size := compactSize(d.ArchiveBytes)
		if !d.CatalogAvailable {
			size = "-"
		}
		cells = append(cells, []string{d.Set.Name, d.Condition.State.String(), size, fmt.Sprint(d.Snapshots), dominantClass(d.Classes), totalCost(d)})
		total.ArchiveBytes += d.ArchiveBytes
		total.Snapshots += d.Snapshots
		total.ArchiveAnnualUSD += d.ArchiveAnnualUSD
		total.MetadataAnnualUSD += d.MetadataAnnualUSD
		total.EstimateComplete = total.EstimateComplete && d.EstimateComplete
	}
	if len(args) == 0 {
		total.ArchiveBytes = r.statusArchiveBytes
		total.ArchiveAnnualUSD = r.statusArchiveCost
		cells = append(cells, []string{"TOTAL", "", compactSize(total.ArchiveBytes), fmt.Sprint(total.Snapshots), "", totalCost(total)})
	}
	color := L.ColorEnabled(term.IsTerminal(int(os.Stdout.Fd())))
	_, err = fmt.Fprint(r.out, renderColumns(cells, format, color))
	if err != nil {
		return err
	}
	if len(args) == 1 && len(rows) == 1 {
		fmt.Fprint(r.out, statusDetails(rows[0]))
	}
	heading := ""
	for _, waiting := range []bool{false, true} {
		heading = ""
		for _, d := range rows {
			if d.Condition.Reason == "" || (d.Condition.State == backup.STATE_WAITING) != waiting {
				continue
			}
			if heading == "" {
				heading = "Needs attention:"
				if waiting {
					heading = "Waiting:"
				}
				fmt.Fprintf(r.out, "\n%s\n", heading)
			}
			fmt.Fprintf(r.out, "  %s  %s\n", L.ASCII(d.Set.Name), L.ASCII(d.Condition.Reason))
			if d.Condition.Action != "" {
				fmt.Fprintf(r.out, "    Next: %s\n", L.ASCII(d.Condition.Action))
			}
		}
	}
	partial := false
	for _, d := range rows {
		partial = partial || !d.EstimateComplete
	}
	if partial {
		fmt.Fprintln(r.out, "\n* Partial storage estimate; unavailable values excluded.")
	}
	return errors.Join(failures...)
}

func compactSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	}
	value := float64(size)
	unit := "B"
	for _, u := range []string{"K", "M", "G", "T", "P", "E"} {
		value /= 1024
		unit = u
		if value < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.2f%s", value, unit)
}
func dominantClass(classes map[string]int64) string {
	result := "-"
	var size int64 = -1
	for class, n := range classes {
		if n > size || n == size && class < result {
			result, size = class, n
		}
	}
	return result
}
func renderColumns(rows [][]string, format string, color bool) string {
	if len(rows) == 0 {
		return ""
	}
	widths := make([]int, len(rows[0]))
	safe := make([][]string, len(rows))
	for i, row := range rows {
		safe[i] = make([]string, len(row))
		for j, cell := range row {
			quoted := strconv.QuoteToASCII(cell)
			safe[i][j] = strings.ReplaceAll(quoted[1:len(quoted)-1], "|", `\x7c`)
			widths[j] = max(widths[j], len(safe[i][j]))
		}
	}
	var out strings.Builder
	if format == "table" {
		out.WriteString(asciiTableBorder(widths))
	}
	for i, row := range safe {
		if format == "table" {
			out.WriteString("| ")
		}
		for j, cell := range row {
			padded := fmt.Sprintf("%-*s", widths[j], cell)
			if color && i > 0 && j == 1 && cell != "" {
				code := ""
				switch backup.SetState(cell) {
				case backup.STATE_SYNCED:
					code = "32"
				case backup.STATE_PAUSED, backup.STATE_LOCAL, backup.STATE_WAITING, backup.STATE_STALE:
					code = "33"
				case backup.STATE_CONFLICT, backup.STATE_FAILED:
					code = "31"
				}
				if code != "" {
					padded = "\x1b[" + code + "m" + cell + "\x1b[0m" + strings.Repeat(" ", widths[j]-len(cell))
				}
			}
			out.WriteString(padded)
			if j < len(row)-1 {
				if format == "table" {
					out.WriteString(" | ")
				} else {
					out.WriteString("  ")
				}
			}
		}
		if format == "table" {
			out.WriteString(" |")
		}
		out.WriteByte('\n')
		if format == "table" && i == 0 {
			out.WriteString(asciiTableBorder(widths))
		}
	}
	if format == "table" {
		out.WriteString(asciiTableBorder(widths))
	}
	return out.String()
}
func statusDetails(d statusData) string {
	var out strings.Builder
	rows := [][]string{{"Last backup (UTC)", statusTime(d.LastBackup)}, {"Last sync (UTC)", statusTime(d.LastSync)}}
	destinations := make([]string, 0, len(d.Set.To))
	for _, id := range d.Set.To {
		name := d.Providers[id]
		if name == "" {
			name = id
		}
		destinations = append(destinations, name)
	}
	if len(destinations) > 0 {
		rows = append(rows, []string{"Destination", strings.Join(destinations, ",")})
	}
	for _, root := range d.Set.Roots {
		rows = append(rows, []string{"Source " + root.Name, root.Path})
	}
	fmt.Fprint(&out, "\n", renderColumns(rows, "plain", false))
	if len(d.Classes) > 0 {
		classes := make([]string, 0, len(d.Classes))
		for class := range d.Classes {
			classes = append(classes, class)
		}
		sort.Strings(classes)
		rows = [][]string{{"STORAGE CLASS", "SIZE", "SHARE"}}
		for _, class := range classes {
			share := 0.0
			if d.ArchiveBytes > 0 {
				share = 100 * float64(d.Classes[class]) / float64(d.ArchiveBytes)
			}
			rows = append(rows, []string{class, compactSize(d.Classes[class]), fmt.Sprintf("%.2f%%", share)})
		}
		fmt.Fprint(&out, "\n", renderColumns(rows, "plain", false))
	}
	return out.String()
}
