package app_cmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"glesha/backup"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	"glesha/pricing"
)

type latestCopyStatus struct {
	Objects      int64  `json:"objects"`
	Class        string `json:"class"`
	Cold         bool   `json:"requires_cold_restore"`
	Verification string `json:"verification"`
}

type statusData struct {
	Condition                backup.Condition            `json:"condition"`
	Classes                  map[string]int64            `json:"storage_classes"`
	LastSync                 *time.Time                  `json:"last_sync,omitempty"`
	LatestObjects            map[string]latestCopyStatus `json:"latest_objects,omitempty"`
	Set                      model.Set                   `json:"set"`
	Latest                   string                      `json:"latest"`
	Pending                  bool                        `json:"pending"`
	Locations                []model.Location            `json:"locations"`
	CatalogAvailable         bool                        `json:"catalog_available"`
	CatalogSynced            bool                        `json:"catalog_synced"`
	LocalCatalogBytes        int64                       `json:"local_catalog_bytes"`
	Metadata                 backup.MetadataStatus       `json:"metadata"`
	Snapshots                int64                       `json:"snapshots"`
	ArchiveBytes             int64                       `json:"archive_bytes"`
	LastUpload               *time.Time                  `json:"last_upload,omitempty"`
	LastBackup               *time.Time                  `json:"last_backup,omitempty"`
	ArchiveAnnualUSD         float64                     `json:"archive_annual_usd"`
	MetadataAnnualUSD        float64                     `json:"metadata_annual_usd"`
	EstimateComplete         bool                        `json:"estimate_complete"`
	ArchiveEstimateComplete  bool                        `json:"archive_estimate_complete"`
	MetadataEstimateComplete bool                        `json:"metadata_estimate_complete"`
	PricingDate              string                      `json:"pricing_date"`
	Providers                map[string]string           `json:"provider_names,omitempty"`
}

func collectStatus(r *runtime, set model.Set) (statusData, error) {
	var err error
	d := statusData{Set: set, PricingDate: pricing.Date(), EstimateComplete: true, ArchiveEstimateComplete: true, MetadataEstimateComplete: true, Providers: map[string]string{}, LatestObjects: map[string]latestCopyStatus{}, Classes: map[string]int64{}}
	if err := r.openSet(set.ID); err != nil {
		return d, err
	}
	d.Pending = r.service.Pending()
	d.Condition, err = r.service.Condition(r.ctx)
	if err != nil {
		return d, err
	}
	lock := flock.New(filepath.Join(r.service.Directory, "writer.lock"))
	free, lockErr := lock.TryLock()
	if lockErr != nil {
		return d, lockErr
	}
	if free {
		lock.Unlock()
	} else {
		d.Condition = backup.Condition{State: backup.STATE_RUNNING}
		if d.Pending {
			pending, e := repository.NewCatalogRepository(r.ctx, filepath.Join(r.service.Directory, "pending.db"))
			if e == nil {
				publication, _ := pending.GetMeta(r.ctx, "publication")
				op, _ := pending.GetMeta(r.ctx, "operation")
				pending.Close()
				d.Condition.Operation = op
				if publication != "" {
					d.Condition.State = backup.STATE_PUBLISHING
				}
			}
		}
	}
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
	syncTime, err := c.GetMeta(r.ctx, "last_sync")
	if err != nil {
		c.Close()
		return d, err
	}
	if t, e := time.Parse(time.RFC3339Nano, syncTime); e == nil {
		d.LastSync = &t
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
		copies, err := backup.SnapshotCopies(v, locations)
		if err != nil {
			return err
		}
		for _, copy := range copies {
			location := copy.Location
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
			d.ArchiveBytes += copy.Size
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
			displayClass := class
			if remote.Kind == "b2" {
				displayClass = "B2"
			}
			if displayClass == "" {
				displayClass = "-"
			}
			d.Classes[displayClass] += copy.Size
			if v.ID == d.Latest {
				group := d.LatestObjects[location.Provider]
				if group.Objects == 0 {
					group.Class, group.Verification = class, location.Verification
				} else {
					if group.Class != class {
						group.Class = "mixed"
					}
					if group.Verification != location.Verification {
						group.Verification = "mixed"
					}
				}
				group.Objects++
				group.Cold = group.Cold || location.Cold
				d.LatestObjects[location.Provider] = group
			}
			cost, known := pricing.Monthly(remote.Kind, remote.Region, class, copy.Size)
			if r.statusCopies != nil {
				result, err := r.statusCopies.ExecContext(r.ctx, "INSERT OR IGNORE INTO copies VALUES(?,?,?)", location.Provider, location.Key, location.Version)
				if err != nil {
					return err
				}
				added, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if added > 0 {
					r.statusArchiveBytes += copy.Size
					r.statusArchiveCost += pricing.Annual(cost)
				}
			}
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
func asciiTableBorder(widths []int) string {
	var cells []string
	for _, width := range widths {
		cells = append(cells, strings.Repeat("-", width+2))
	}
	return "+" + strings.Join(cells, "+") + "+\n"
}
