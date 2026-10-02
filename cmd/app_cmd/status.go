package app_cmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"glesha/backup"
	"glesha/database/model"
	"glesha/file_io"
	L "glesha/logger"
	"glesha/pricing"
)

type statusData struct {
	Set                      model.Set             `json:"set"`
	Latest                   string                `json:"latest"`
	Pending                  bool                  `json:"pending"`
	Locations                []model.Location      `json:"locations"`
	CatalogAvailable         bool                  `json:"catalog_available"`
	CatalogSynced            bool                  `json:"catalog_synced"`
	LocalCatalogBytes        int64                 `json:"local_catalog_bytes"`
	Metadata                 backup.MetadataStatus `json:"metadata"`
	Snapshots                int64                 `json:"snapshots"`
	ArchiveBytes             int64                 `json:"archive_bytes"`
	LastUpload               *time.Time            `json:"last_upload,omitempty"`
	LastBackup               *time.Time            `json:"last_backup,omitempty"`
	ArchiveAnnualUSD         float64               `json:"archive_annual_usd"`
	MetadataAnnualUSD        float64               `json:"metadata_annual_usd"`
	EstimateComplete         bool                  `json:"estimate_complete"`
	ArchiveEstimateComplete  bool                  `json:"archive_estimate_complete"`
	MetadataEstimateComplete bool                  `json:"metadata_estimate_complete"`
	PricingDate              string                `json:"pricing_date"`
	Providers                map[string]string     `json:"provider_names,omitempty"`
}

var overviewColumnWidths = []int{20, 9, 9, 5, 12, 12, 10, 16}
var detailColumnWidths = []int{27, 60}

func status(r *runtime, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("cli: status accepts zero or one set")
	}
	var output strings.Builder
	if !r.env.JSON && len(args) == 0 {
		output.WriteString(asciiTableHeader([]string{"SET", "STATE", "CATALOG", "SNAPS", "ARCHIVES", "META", "EST USD/YR", "UPLOADED (UTC)"}, overviewColumnWidths))
	}
	show := func(set model.Set) error {
		data, err := collectStatus(r, set)
		if err != nil {
			return err
		}
		if r.env.JSON {
			return r.emit("status", data, "")
		}
		if len(args) == 1 {
			output.WriteString(statusDetails(data))
		} else {
			catalog := "missing"
			if data.CatalogAvailable {
				catalog = "local"
			}
			if data.CatalogSynced {
				catalog = "cached"
			}
			state := "ready"
			if data.Latest == "" {
				state = "new"
			}
			if data.Pending {
				state = "pending"
			}
			output.WriteString(asciiTableRow([]string{set.Name, state, catalog, fmt.Sprint(data.Snapshots), L.Bytes(data.ArchiveBytes), metadataSize(data.Metadata), totalCost(data), statusTime(data.LastUpload)}, overviewColumnWidths))
		}
		return nil
	}
	if len(args) == 1 {
		set, err := r.registry.Set(r.ctx, args[0])
		if err != nil {
			return err
		}
		if err = show(set); err != nil {
			return err
		}
	} else if err := r.registry.Each(r.ctx, show); err != nil {
		return err
	}
	if r.env.JSON {
		return nil
	}
	if len(args) == 0 {
		output.WriteString(asciiTableBorder(overviewColumnWidths))
	}
	return r.emit("status_summary", nil, strings.TrimSuffix(output.String(), "\n"))
}

func collectStatus(r *runtime, set model.Set) (statusData, error) {
	d := statusData{Set: set, PricingDate: pricing.Date(), EstimateComplete: true, ArchiveEstimateComplete: true, MetadataEstimateComplete: true, Providers: map[string]string{}}
	if err := r.openSet(set.ID); err != nil {
		return d, err
	}
	d.Pending = r.service.Pending()
	info, err := os.Stat(filepath.Join(r.service.Directory, "catalog.db"))
	if err != nil && !os.IsNotExist(err) {
		return d, err
	}
	d.CatalogAvailable = err == nil && info.Size() > 0
	if d.CatalogAvailable {
		d.LocalCatalogBytes = info.Size()
	}
	if r.env.Refresh {
		from, err := r.remoteIDs(r.env.From)
		if err != nil {
			return d, err
		}
		if len(from) == 0 {
			from = append([]string{}, set.To...)
		}
		if set.CatalogTo != "" && set.CatalogTo != "local" {
			from = append(from, set.CatalogTo)
		}
		if err = r.stores(from); err != nil {
			return d, err
		}
		if d.CatalogAvailable {
			if err = r.service.Refresh(r.ctx); err != nil {
				return d, err
			}
		}
		if set.CatalogTo != "" && set.CatalogTo != "local" {
			if err = r.service.RefreshMetadataStatus(r.ctx); err != nil {
				return d, err
			}
		}
	}
	d.Metadata, err = r.service.MetadataStatus()
	if err != nil {
		return d, err
	}
	d.LastUpload = d.Metadata.LastUpload
	if !d.CatalogAvailable {
		d.EstimateComplete = false
		d.ArchiveEstimateComplete = false
		d.MetadataEstimateComplete = false
		return d, nil
	}
	c, err := r.service.Catalog(r.ctx)
	if err != nil {
		return d, err
	}
	latest, err := c.Latest(r.ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		c.Close()
		return d, err
	}
	d.Latest = latest.ID
	if latest.ID != "" && !latest.Imported {
		t := latest.Created
		d.LastBackup = &t
	}
	if latest.Imported {
		d.LastBackup = latest.BackupDate
	}
	revision, err := c.GetMeta(r.ctx, "remote_revision")
	c.Close()
	if err != nil {
		return d, err
	}
	d.CatalogSynced = revision != ""
	tracker, err := file_io.Temp(r.service.Directory)
	if err != nil {
		return d, err
	}
	defer os.Remove(tracker.Name())
	if err = tracker.Close(); err != nil {
		return d, err
	}
	seen, err := sql.Open("sqlite", tracker.Name())
	if err != nil {
		return d, err
	}
	defer seen.Close()
	if _, err = seen.ExecContext(r.ctx, "PRAGMA cache_size=-256; CREATE TABLE copies(provider TEXT,key TEXT,version TEXT,PRIMARY KEY(provider,key,version))"); err != nil {
		return d, err
	}
	if err = r.service.History(r.ctx, func(v model.Snapshot, locations []model.Location) error {
		d.Snapshots++
		if v.ID == d.Latest {
			d.Locations = locations
		}
		for _, location := range locations {
			if location.Status != model.STATUS_COMPLETED {
				continue
			}
			if location.UploadedAt != nil && (d.LastUpload == nil || location.UploadedAt.After(*d.LastUpload)) {
				d.LastUpload = location.UploadedAt
			}
			result, err := seen.ExecContext(r.ctx, "INSERT OR IGNORE INTO copies VALUES(?,?,?)", location.Provider, location.Key, location.Version)
			if err != nil {
				return err
			}
			added, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if added == 0 {
				continue
			}
			d.ArchiveBytes += v.Size
			remote, err := r.registry.Remote(r.ctx, location.Provider)
			if err != nil {
				return err
			}
			d.Providers[remote.ID] = remote.Name
			class := location.CurrentClass
			if location.ObservedClass != "" {
				class = location.ObservedClass
			}
			if class == "" {
				class = location.InitialClass
			}
			if class == "INTELLIGENT_TIERING" {
				if location.ArchiveStatus == "ARCHIVE_ACCESS" {
					class = "INTELLIGENT_TIERING_AA"
				}
				if location.ArchiveStatus == "DEEP_ARCHIVE_ACCESS" {
					class = "INTELLIGENT_TIERING_DAA"
				}
			}
			cost, known := pricing.Monthly(remote.Kind, remote.Region, class, v.Size)
			d.ArchiveAnnualUSD += pricing.Annual(cost)
			d.EstimateComplete = d.EstimateComplete && known
			d.ArchiveEstimateComplete = d.ArchiveEstimateComplete && known
		}
		return nil
	}); err != nil {
		return d, err
	}
	if set.CatalogTo != "local" && set.CatalogTo != "" {
		if d.Metadata.Version == 0 || !d.Metadata.Complete {
			d.EstimateComplete = false
			d.MetadataEstimateComplete = false
		}
		remote, err := r.registry.Remote(r.ctx, set.CatalogTo)
		if err != nil {
			return d, err
		}
		for class, group := range d.Metadata.Classes {
			cost, known := pricing.MonthlyObjects(remote.Kind, remote.Region, class, group.BillableBytes, group.Objects, group.MonitoredObjects)
			d.MetadataAnnualUSD += pricing.Annual(cost)
			d.EstimateComplete = d.EstimateComplete && known
			d.MetadataEstimateComplete = d.MetadataEstimateComplete && known
		}
	}
	return d, nil
}

func statusTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04")
}
func metadataSize(v backup.MetadataStatus) string {
	if v.Version == 0 {
		return "-"
	}
	if !v.Complete {
		return L.Bytes(v.Bytes) + "*"
	}
	return L.Bytes(v.Bytes)
}
func usd(value float64) string {
	if value > 0 && value < 0.01 {
		return "<$0.01"
	}
	return fmt.Sprintf("$%.2f", value)
}
func totalCost(d statusData) string {
	return estimatedCost(d.ArchiveAnnualUSD+d.MetadataAnnualUSD, d.EstimateComplete)
}
func estimatedCost(cost float64, complete bool) string {
	if !complete && cost == 0 {
		return "-"
	}
	value := usd(cost)
	if !complete {
		value += "*"
	}
	return value
}
func asciiTableHeader(headers []string, widths []int) string {
	return asciiTableBorder(widths) + asciiTableRow(headers, widths) + asciiTableBorder(widths)
}
func asciiTableBorder(widths []int) string {
	var cells []string
	for _, width := range widths {
		cells = append(cells, strings.Repeat("-", width+2))
	}
	return "+" + strings.Join(cells, "+") + "+\n"
}
func asciiTableRow(cells []string, widths []int) string {
	values := make([]string, len(cells))
	for i, cell := range cells {
		if cell == "" {
			cell = "-"
		}
		quoted := strconv.QuoteToASCII(cell)
		values[i] = strings.ReplaceAll(quoted[1:len(quoted)-1], "|", `\x7c`)
	}
	var out strings.Builder
	for {
		more := false
		out.WriteString("|")
		for i, cell := range values {
			if len(cell) > widths[i] {
				cell, values[i] = cell[:widths[i]], cell[widths[i]:]
				more = true
			} else {
				values[i] = ""
			}
			fmt.Fprintf(&out, " %-*s |", widths[i], cell)
		}
		out.WriteString("\n")
		if !more {
			break
		}
	}
	return out.String()
}
func statusDetails(d statusData) string {
	metadataCost := estimatedCost(d.MetadataAnnualUSD, d.MetadataEstimateComplete)
	localSize := "-"
	if d.CatalogAvailable {
		localSize = L.Bytes(d.LocalCatalogBytes)
	}
	rows := [][]string{{"Set", d.Set.Name}, {"ID", d.Set.ID}, {"Latest snapshot", d.Latest}, {"Last backup (UTC)", statusTime(d.LastBackup)}, {"Last upload (UTC)", statusTime(d.LastUpload)}, {"Snapshots", fmt.Sprint(d.Snapshots)}, {"Pending work", fmt.Sprint(d.Pending)}, {"Catalog available locally", fmt.Sprint(d.CatalogAvailable)}, {"Catalog synced previously", fmt.Sprint(d.CatalogSynced)}, {"Local catalog size", localSize}, {"Stored archive copies", L.Bytes(d.ArchiveBytes)}, {"Remote metadata size", metadataSize(d.Metadata)}, {"Metadata size as of (UTC)", statusTime(&d.Metadata.AsOf)}, {"Est. archive USD/year", estimatedCost(d.ArchiveAnnualUSD, d.ArchiveEstimateComplete)}, {"Est. metadata USD/year", metadataCost}, {"Est. total USD/year", totalCost(d)}}
	var out strings.Builder
	out.WriteString(asciiTableHeader([]string{"PROPERTY", "VALUE"}, detailColumnWidths))
	for _, row := range rows {
		out.WriteString(asciiTableRow(row, detailColumnWidths))
	}
	for _, root := range d.Set.Roots {
		out.WriteString(asciiTableRow([]string{"Source " + root.Name, root.Path}, detailColumnWidths))
	}
	for _, location := range d.Locations {
		name := d.Providers[location.Provider]
		if name == "" {
			name = location.Provider
		}
		out.WriteString(asciiTableRow([]string{"Latest copy: " + name, string(location.Status) + "; " + location.CurrentClass + "; verified " + location.Verification}, detailColumnWidths))
		if location.Cold {
			out.WriteString(asciiTableRow([]string{"Retrieval: " + name, "cold restore required"}, detailColumnWidths))
		}
	}
	out.WriteString(asciiTableBorder(detailColumnWidths))
	return out.String()
}
