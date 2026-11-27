package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"glesha/internal/backup"
	"glesha/internal/cloud"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
	"glesha/internal/fsutil"
	"glesha/internal/pricing"
)

type latestCopyStatus struct {
	Objects      int64  `json:"objects"`
	Class        string `json:"class"`
	Cold         bool   `json:"requires_cold_restore"`
	Verification string `json:"verification"`
}

type statusData struct {
	Retrievals               []backup.RetrievalStatus    `json:"retrievals,omitempty"`
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
	// SharedWith counts archives that other sets also use, in single-set status
	SharedWith map[string]int `json:"shared_with,omitempty"`
}

// collectStatus gathers the state, size and cost of one set. totals, when not nil,
// also counts each archive copy once across every set of this status run.
func collectStatus(r *runtime, set model.Set, totals *statusTotals) (statusData, error) {
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
	free, err := r.liveCondition(&d)
	if err != nil {
		return d, err
	}
	info, err := os.Stat(filepath.Join(r.service.Directory, "catalog.db"))
	if err != nil && !os.IsNotExist(err) {
		return d, err
	}
	d.CatalogAvailable = err == nil && info.Size() > 0
	if d.CatalogAvailable {
		d.LocalCatalogBytes = info.Size()
	}
	d.Retrievals, err = r.service.Retrievals(r.ctx, false)
	if err != nil {
		return d, err
	}
	if r.env.Refresh {
		if err = r.refreshStatus(set, &d); err != nil {
			return d, err
		}
	}
	if free {
		applyRetrievalCondition(&d)
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
	if err = r.readCatalogSummary(&d); err != nil {
		return d, err
	}
	if err = r.countCopies(&d, totals); err != nil {
		return d, err
	}
	if set.CatalogTo != "local" && set.CatalogTo != "" {
		if err = r.estimateMetadata(set, &d); err != nil {
			return d, err
		}
	}
	return d, nil
}

// liveCondition reports whether the set is idle; if another process holds it,
// the condition shows that operation instead of the saved one.
func (r *runtime) liveCondition(d *statusData) (free bool, err error) {
	lock := flock.New(filepath.Join(r.service.Directory, "writer.lock"))
	free, err = lock.TryLock()
	if err != nil {
		return false, err
	}
	if free {
		lock.Unlock()
		return true, nil
	}
	d.Condition = backup.Condition{State: backup.STATE_RUNNING}
	if d.Pending {
		pending, err := repository.NewCatalogRepository(r.ctx, filepath.Join(r.service.Directory, "pending.db"))
		if err == nil {
			publication, _ := pending.GetMeta(r.ctx, "publication")
			op, _ := pending.GetMeta(r.ctx, "operation")
			pending.Close()
			d.Condition.Operation = op
			if publication != "" {
				d.Condition.State = backup.STATE_PUBLISHING
			}
		}
	}
	return false, nil
}

// refreshStatus queries the remotes for current object and metadata state.
func (r *runtime) refreshStatus(set model.Set, d *statusData) error {
	from, err := r.remoteIDs(r.env.From)
	if err != nil {
		return err
	}
	if len(from) == 0 {
		from = append([]string{}, set.To...)
	}
	if set.CatalogTo != "" && set.CatalogTo != "local" {
		from = append(from, set.CatalogTo)
	}
	for _, retrieval := range d.Retrievals {
		for _, object := range retrieval.Objects {
			from = append(from, object.Provider)
		}
	}
	if err = r.stores(from); err != nil {
		return err
	}
	if d.CatalogAvailable {
		if err = r.service.Refresh(r.ctx); err != nil {
			return err
		}
	}
	if set.CatalogTo != "" && set.CatalogTo != "local" {
		if err = r.service.RefreshMetadataStatus(r.ctx); err != nil {
			return err
		}
	}
	d.Retrievals, err = r.service.Retrievals(r.ctx, true)
	return err
}

// applyRetrievalCondition shows an idle set with retrievals as waiting or ready.
func applyRetrievalCondition(d *statusData) {
	retrievalOperation := d.Condition.Operation == "" || d.Condition.Operation == "import" || d.Condition.Operation == "restore"
	state := d.Condition.State
	idle := state == backup.STATE_WAITING || state == backup.STATE_PAUSED || state == backup.STATE_NEW || state == backup.STATE_STALE && !d.CatalogAvailable
	if len(d.Retrievals) == 0 || !retrievalOperation || !idle {
		return
	}
	if d.Condition.Operation == "" {
		d.Condition.Operation = strings.ToLower(d.Retrievals[0].Task.String())
	}
	d.Condition.State = backup.STATE_PAUSED
	d.Condition.Reason = "Download ready"
	for _, retrieval := range d.Retrievals {
		if retrieval.Waiting > 0 {
			d.Condition.State = backup.STATE_WAITING
			d.Condition.Reason = "Cold retrieval pending"
			break
		}
	}
}

// readCatalogSummary reads the latest snapshot and sync state from the local catalog.
func (r *runtime) readCatalogSummary(d *statusData) error {
	c, err := r.service.Catalog(r.ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	latest, err := c.Latest(r.ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
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
		return err
	}
	if t, err := time.Parse(time.RFC3339Nano, syncTime); err == nil {
		d.LastSync = &t
	}
	revision, err := c.GetMeta(r.ctx, "remote_revision")
	if err != nil {
		return err
	}
	d.CatalogSynced = revision != ""
	return nil
}

// countCopies adds every completed archive copy of the set to d, once each.
func (r *runtime) countCopies(d *statusData, totals *statusTotals) error {
	seen, err := newCopySet(r.ctx, r.service.Directory)
	if err != nil {
		return err
	}
	defer seen.Close()
	return r.service.History(r.ctx, func(v model.Snapshot, locations []model.Location) error {
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
			added, err := seen.add(r.ctx, location)
			if err != nil {
				return err
			}
			if !added {
				continue
			}
			d.ArchiveBytes += copy.Size
			remote, err := r.registry.Remote(r.ctx, location.Provider)
			if err != nil {
				return err
			}
			d.Providers[remote.ID] = remote.Name
			class := copyClass(location)
			d.Classes[displayClass(class, remote.Kind)] += copy.Size
			if v.ID == d.Latest {
				group := d.LatestObjects[location.Provider]
				group.add(class, location)
				d.LatestObjects[location.Provider] = group
			}
			cost, known := pricing.Monthly(remote.Kind, remote.Region, class, copy.Size)
			if totals != nil {
				if err = totals.add(r.ctx, location, copy.Size, pricing.Annual(cost)); err != nil {
					return err
				}
			}
			d.ArchiveAnnualUSD += pricing.Annual(cost)
			d.EstimateComplete = d.EstimateComplete && known
			d.ArchiveEstimateComplete = d.ArchiveEstimateComplete && known
		}
		return nil
	})
}

// estimateMetadata prices the catalog objects stored on the set's catalog remote.
func (r *runtime) estimateMetadata(set model.Set, d *statusData) error {
	if d.Metadata.Version == 0 || !d.Metadata.Complete {
		d.EstimateComplete = false
		d.MetadataEstimateComplete = false
	}
	remote, err := r.registry.Remote(r.ctx, set.CatalogTo)
	if err != nil {
		return err
	}
	for class, group := range d.Metadata.Classes {
		cost, known := pricing.MonthlyObjects(remote.Kind, remote.Region, class, group.BillableBytes, group.Objects, group.MonitoredObjects)
		d.MetadataAnnualUSD += pricing.Annual(cost)
		d.EstimateComplete = d.EstimateComplete && known
		d.MetadataEstimateComplete = d.MetadataEstimateComplete && known
	}
	return nil
}

// copyClass is the storage class a copy is billed at, distinguishing the
// Intelligent-Tiering archive tiers.
func copyClass(location model.Location) string {
	class := location.CurrentClass
	if location.ObservedClass != "" {
		class = location.ObservedClass
	}
	if class == "" {
		class = location.InitialClass
	}
	if class == "INTELLIGENT_TIERING" {
		switch location.ArchiveStatus {
		case "ARCHIVE_ACCESS":
			class = "INTELLIGENT_TIERING_AA"
		case "DEEP_ARCHIVE_ACCESS":
			class = "INTELLIGENT_TIERING_DAA"
		}
	}
	return class
}

// displayClass names class for the status table; providers without storage
// classes show their own name.
func displayClass(class, kind string) string {
	if !cloud.CapabilitiesOf(kind).StorageClasses {
		class = strings.ToUpper(kind)
	}
	if class == "" {
		return "-"
	}
	return class
}

func (g *latestCopyStatus) add(class string, location model.Location) {
	if g.Objects == 0 {
		g.Class, g.Verification = class, location.Verification
	} else {
		if g.Class != class {
			g.Class = "mixed"
		}
		if g.Verification != location.Verification {
			g.Verification = "mixed"
		}
	}
	g.Objects++
	g.Cold = g.Cold || location.Cold
}

// copySet records archive copies already counted. It is a temporary database so
// that long histories do not need memory proportional to their size.
type copySet struct {
	db   *sql.DB
	file string
}

func newCopySet(ctx context.Context, dir string) (*copySet, error) {
	tracker, err := fsutil.Temp(dir)
	if err != nil {
		return nil, err
	}
	if err = tracker.Close(); err != nil {
		os.Remove(tracker.Name())
		return nil, err
	}
	db, err := sql.Open("sqlite", tracker.Name())
	if err != nil {
		os.Remove(tracker.Name())
		return nil, err
	}
	set := &copySet{db: db, file: tracker.Name()}
	if _, err = db.ExecContext(ctx, "PRAGMA cache_size=-256; CREATE TABLE copies(provider TEXT,key TEXT,version TEXT,PRIMARY KEY(provider,key,version))"); err != nil {
		set.Close()
		return nil, err
	}
	return set, nil
}

// add records location and reports whether it was new.
func (c *copySet) add(ctx context.Context, location model.Location) (bool, error) {
	result, err := c.db.ExecContext(ctx, "INSERT OR IGNORE INTO copies VALUES(?,?,?)", location.Provider, location.Key, location.Version)
	if err != nil {
		return false, err
	}
	added, err := result.RowsAffected()
	return added > 0, err
}

func (c *copySet) Close() {
	c.db.Close()
	os.Remove(c.file)
}

// statusTotals counts archive copies once across every set of one status run.
type statusTotals struct {
	copies    *copySet
	bytes     int64
	annualUSD float64
}

func (t *statusTotals) add(ctx context.Context, location model.Location, size int64, annualUSD float64) error {
	added, err := t.copies.add(ctx, location)
	if added {
		t.bytes += size
		t.annualUSD += annualUSD
	}
	return err
}

func statusTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04 MST")
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
