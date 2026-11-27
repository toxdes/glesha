package cli

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"glesha/internal/backup"
	"glesha/internal/database/model"
	"glesha/internal/logger"
)

func status(r *runtime, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("cli: status accepts zero or one set")
	}
	format := r.env.Format
	if format == "" {
		format = "plain"
	}
	switch format {
	case "plain", "table", "csv", "json":
	default:
		return fmt.Errorf("cli: format must be plain, table, csv or json")
	}
	var refreshProgress *logger.Progress
	if r.env.Refresh {
		var err error
		refreshProgress, err = r.startActivity("Refreshing archive availability")
		if err != nil {
			return err
		}
		defer refreshProgress.Stop()
	}
	copies, err := newCopySet(r.ctx, r.root)
	if err != nil {
		return err
	}
	defer copies.Close()
	totals := &statusTotals{copies: copies}
	rows, failures, err := collectStatusRows(r, args, totals)
	if err != nil {
		return err
	}
	refreshProgress.Stop()
	if format == "json" {
		for _, d := range rows {
			if err := r.emit("status", d, ""); err != nil {
				return err
			}
		}
		return errors.Join(failures...)
	}
	if format == "csv" {
		return writeStatusCSV(r, rows, failures)
	}
	if len(rows) == 0 {
		return errors.Join(errors.Join(failures...), r.emptyInventory())
	}
	all := len(args) == 0
	sum := summarizeStatus(rows, totals, all)
	cells := statusTable(rows, sum, all)
	color := logger.ColorEnabled(term.IsTerminal(int(os.Stdout.Fd())))
	_, err = fmt.Fprint(r.out, renderColumns(cells, format, color))
	if err != nil {
		return err
	}
	if len(args) == 1 && len(rows) == 1 {
		details := rows[0]
		details.Providers = make(map[string]string, len(details.Set.To))
		for _, id := range details.Set.To {
			details.Providers[id] = r.remoteLabel(id)
		}
		if details.CatalogAvailable {
			if details.SharedWith, err = r.service.SharedArchives(r.ctx); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprint(r.out, statusDetails(details)); err != nil {
			return err
		}
	}
	if err = renderRetrievals(r.out, rows, len(args) == 1, format, color); err != nil {
		return err
	}
	if all && sum.unavailable > 0 {
		fmt.Fprintf(r.out, "\nTotals include %d of %d sets; the others have no readable catalog.\n", len(rows)-sum.unavailable, len(rows))
	}
	if all && sum.locked > 0 {
		fmt.Fprintf(r.out, "%d sets have a locked catalog.\n  Open a cached catalog: glesha catalog unlock SET\n  Fetch remote metadata: glesha catalog sync\n", sum.locked)
	}
	if err := r.renderAttention(rows, len(args) == 1, color); err != nil {
		return err
	}
	partial := strings.HasSuffix(totalCost(sum.total), "*")
	for _, d := range rows {
		partial = partial || strings.HasSuffix(totalCost(d), "*")
	}
	if partial {
		fmt.Fprintln(r.out, "\n* Partial estimate; unknown values are excluded.")
	}
	return reportedError("status", errors.Join(failures...))
}

func collectStatusRows(r *runtime, args []string, totals *statusTotals) ([]statusData, []error, error) {
	rows := []statusData{}
	var failures []error
	if len(args) == 0 {
		if err := r.discoveryStatus(); err != nil {
			logger.Warn(err.Error())
			failures = append(failures, err)
		}
	}
	collect := func(set model.Set) error {
		d, err := collectStatus(r, set, totals)
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
			return nil, nil, setError(args[0], err)
		}
		if err = collect(set); err != nil {
			return nil, nil, err
		}
	} else if err := r.registry.Each(r.ctx, collect); err != nil {
		return nil, nil, err
	}
	return rows, failures, nil
}

func writeStatusCSV(r *runtime, rows []statusData, failures []error) error {
	writer := csv.NewWriter(r.out)
	writer.Write([]string{"set", "state", "archive_bytes", "snapshots", "storage_class", "estimated_annual_usd", "estimate_complete", "retrievals"})
	for _, d := range rows {
		retrievals, err := json.Marshal(d.Retrievals)
		if err != nil {
			return err
		}
		writer.Write([]string{d.Set.Name, d.Condition.State.String(), strconv.FormatInt(d.ArchiveBytes, 10), strconv.FormatInt(d.Snapshots, 10), dominantClass(d.Classes), strconv.FormatFloat(d.ArchiveAnnualUSD+d.MetadataAnnualUSD, 'f', 6, 64), strconv.FormatBool(d.EstimateComplete), string(retrievals)})
	}
	writer.Flush()
	return errors.Join(writer.Error(), errors.Join(failures...))
}

// statusSummary totals the rows status shows. With every set shown, the archive
// size and cost come from totals, which counts each stored copy once.
type statusSummary struct {
	total               statusData
	locked, unavailable int
}

func summarizeStatus(rows []statusData, totals *statusTotals, all bool) statusSummary {
	sum := statusSummary{total: statusData{EstimateComplete: true}}
	for _, d := range rows {
		if d.Condition.State == backup.STATE_LOCKED {
			sum.locked++
		}
		if !d.CatalogAvailable {
			sum.unavailable++
		}
		sum.total.ArchiveBytes += d.ArchiveBytes
		sum.total.Snapshots += d.Snapshots
		sum.total.ArchiveAnnualUSD += d.ArchiveAnnualUSD
		sum.total.MetadataAnnualUSD += d.MetadataAnnualUSD
		sum.total.EstimateComplete = sum.total.EstimateComplete && d.EstimateComplete
	}
	if all {
		sum.total.ArchiveBytes = totals.bytes
		sum.total.ArchiveAnnualUSD = totals.annualUSD
	}
	return sum
}

func statusTable(rows []statusData, sum statusSummary, all bool) [][]string {
	cells := [][]string{{"SET", "STATE", "SIZE", "SNAPSHOTS", "STORAGE CLASS", "EST/YR"}}
	for _, d := range rows {
		size, snapshots := compactSize(d.ArchiveBytes), fmt.Sprint(d.Snapshots)
		if !d.CatalogAvailable {
			size, snapshots = "-", "-"
		}
		cells = append(cells, []string{d.Set.Name, d.Condition.State.String(), size, snapshots, dominantClass(d.Classes), totalCost(d)})
	}
	if all {
		size, snapshots := compactSize(sum.total.ArchiveBytes), fmt.Sprint(sum.total.Snapshots)
		if sum.unavailable == len(rows) {
			size, snapshots = "-", "-"
		}
		cells = append(cells, []string{"TOTAL", "", size, snapshots, "", totalCost(sum.total)})
	}
	return cells
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
			if color && i > 0 && rows[0][j] == "STATE" && cell != "" {
				code := ""
				code = stateColor(backup.SetState(cell))
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
	catalog := "Unavailable"
	if d.CatalogAvailable {
		catalog = "Open (cached)"
	} else if d.Condition.State == backup.STATE_LOCKED {
		catalog = "Locked"
	}
	rows := [][]string{{"Set ID", d.Set.ID}, {"Catalog", catalog}, {"Last backup", statusTime(d.LastBackup)}, {"Last sync", statusTime(d.LastSync)}}
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
	others := make([]string, 0, len(d.SharedWith))
	for set := range d.SharedWith {
		others = append(others, set)
	}
	sort.Strings(others)
	for _, set := range others {
		verb := "archives are"
		if d.SharedWith[set] == 1 {
			verb = "archive is"
		}
		fmt.Fprintf(&out, "%d %s shared with set %s.\n", d.SharedWith[set], verb, logger.ASCII(set))
	}
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

func renderRetrievals(out io.Writer, sets []statusData, single bool, format string, color bool) error {
	var checked *time.Time
	unknown, count, zone, mixedZone := false, 0, "", false
	for _, set := range sets {
		for _, retrieval := range set.Retrievals {
			count++
			unknown = unknown || retrieval.CheckedAt == nil
			if retrieval.CheckedAt != nil && (checked == nil || retrieval.CheckedAt.Before(*checked)) {
				checked = retrieval.CheckedAt
			}
			if retrieval.ExpiresAt != nil {
				current := retrieval.ExpiresAt.Local().Format("MST")
				mixedZone = mixedZone || zone != "" && zone != current
				zone = current
			}
		}
	}
	if count == 0 {
		return nil
	}
	if unknown {
		checked = nil
	}
	if zone == "" {
		zone = time.Now().Local().Format("MST")
	}
	expiryHeading := "EXPIRES (" + zone + ")"
	if mixedZone {
		expiryHeading = "EXPIRES"
	}
	header := []string{"TASK", "STATE", "READY", "WAITING", expiryHeading}
	if !single {
		header = append([]string{"SET"}, header...)
	}
	rows := [][]string{header}
	for _, set := range sets {
		for _, retrieval := range set.Retrievals {
			expires := "-"
			if retrieval.ExpiresAt != nil {
				layout := "Jan 02 15:04"
				if retrieval.ExpiresAt.Local().Year() != time.Now().Local().Year() {
					layout = "2006-01-02 15:04"
				}
				if mixedZone {
					layout += " MST"
				}
				expires = retrieval.ExpiresAt.Local().Format(layout)
			}
			row := []string{retrieval.Task.String(), retrieval.State.String(), strconv.Itoa(retrieval.Ready), strconv.Itoa(retrieval.Waiting), expires}
			if !single {
				row = append([]string{set.Set.Name}, row...)
			}
			rows = append(rows, row)
		}
	}
	_, err := fmt.Fprintf(out, "\nRetrievals\nLast checked: %s\n%s", statusTime(checked), renderColumns(rows, format, color))
	return err
}

func stateColor(state backup.SetState) string {
	switch state {
	case backup.STATE_SYNCED, backup.SetState(backup.RETRIEVAL_READY):
		return "32"
	case backup.STATE_PAUSED, backup.STATE_LOCAL, backup.STATE_WAITING, backup.STATE_STALE, backup.STATE_LOCKED, backup.STATE_NEW, backup.STATE_TRASH, backup.SetState(backup.RETRIEVAL_QUEUED), backup.SetState(backup.RETRIEVAL_PARTIAL):
		return "33"
	case backup.STATE_CONFLICT, backup.STATE_FAILED, backup.STATE_DELETING, backup.SetState(backup.RETRIEVAL_EXPIRED):
		return "31"
	case backup.STATE_RUNNING, backup.STATE_PUBLISHING:
		return "36"
	default:
		return ""
	}
}

type attentionKey struct {
	reason, action string
	state          backup.SetState
}
type attentionGroup struct {
	key   attentionKey
	names []string
}

func (r *runtime) renderAttention(rows []statusData, single, color bool) error {
	for _, waiting := range []bool{false, true} {
		var groups []attentionGroup
		indices := map[attentionKey]int{}
		for _, row := range rows {
			condition := row.Condition
			if condition.State == backup.STATE_LOCKED && !single {
				continue
			}
			if len(row.Retrievals) > 0 && (condition.Operation == "import" || condition.Operation == "restore") && (condition.State == backup.STATE_WAITING || condition.State == backup.STATE_PAUSED) {
				continue
			}
			if condition.Reason == "" || (condition.State == backup.STATE_WAITING) != waiting {
				continue
			}
			key := attentionKey{condition.Reason, condition.Action, condition.State}
			index, exists := indices[key]
			if !exists {
				index = len(groups)
				indices[key] = index
				groups = append(groups, attentionGroup{key: key})
			}
			groups[index].names = append(groups[index].names, row.Set.Name)
		}
		if len(groups) == 0 {
			continue
		}
		heading := "Needs attention:"
		if waiting {
			heading = "Waiting:"
		}
		if _, err := fmt.Fprintf(r.out, "\n%s\n", heading); err != nil {
			return err
		}
		for _, group := range groups {
			label := logger.Value(group.names[0])
			if len(group.names) > 1 {
				label = fmt.Sprintf("%d sets", len(group.names))
			}
			text := label + "  " + logger.Value(group.key.reason)
			if code := stateColor(group.key.state); color && code != "" {
				text = "\x1b[" + code + "m" + text + "\x1b[0m"
			}
			if _, err := fmt.Fprintf(r.out, "  %s\n", text); err != nil {
				return err
			}
			if group.key.action != "" {
				if _, err := fmt.Fprintf(r.out, "    Next: %s\n", logger.Value(group.key.action)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
