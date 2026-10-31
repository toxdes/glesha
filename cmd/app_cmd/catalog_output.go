package app_cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"glesha/backup"
	L "glesha/logger"
)

type CatalogSyncOutcome string

const (
	CatalogSyncUpdated  CatalogSyncOutcome = "updated"
	CatalogSyncUpToDate CatalogSyncOutcome = "up_to_date"
)

func (v CatalogSyncOutcome) String() string { return string(v) }

func ParseCatalogSyncOutcome(value string) (CatalogSyncOutcome, error) {
	v := CatalogSyncOutcome(value)
	switch v {
	case CatalogSyncUpdated, CatalogSyncUpToDate:
		return v, nil
	default:
		return "", fmt.Errorf("cli: unknown catalog sync result %q", value)
	}
}

type catalogSyncSummary struct {
	Checked               int `json:"checked"`
	Updated               int `json:"updated"`
	UpToDate              int `json:"up_to_date"`
	Locked                int `json:"locked"`
	Skipped               int `json:"skipped"`
	Failed                int `json:"failed"`
	RegistryCopiesUpdated int `json:"registry_copies_updated"`
	localOnly             int
	outOfScope            int
}

func (r *runtime) beginCatalogCheck() error {
	var err error
	r.catalogCheckProgress, err = r.startActivity("Checking remote catalogs")
	r.catalogCheckActive = err == nil
	return err
}

func (r *runtime) finishCatalogCheck() {
	r.catalogCheckProgress.Stop()
	r.catalogCheckProgress = nil
	r.catalogCheckActive = false
}

func (r *runtime) localCatalogRevision() (string, error) {
	if _, err := os.Stat(filepath.Join(r.service.Directory, "catalog.db")); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	c, err := r.service.Catalog(r.ctx)
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.GetMeta(r.ctx, "remote_revision")
}

func (r *runtime) emitCatalogResult(state backup.CatalogState, updated, locked bool, summary *catalogSyncSummary) error {
	result := CatalogSyncUpToDate
	summary.Checked++
	if updated {
		result = CatalogSyncUpdated
		summary.Updated++
	} else {
		summary.UpToDate++
	}
	if locked {
		summary.Locked++
	}
	if !r.env.jsonOutput() && !updated {
		return nil
	}
	name := r.service.Set.Name
	if name == "" {
		name = r.service.Set.ID
	}
	if !r.env.jsonOutput() && summary.Updated == 1 {
		if err := r.result(fmt.Sprintf("%-36s  %s", "SET", "RESULT")); err != nil {
			return err
		}
	}
	text := fmt.Sprintf("%-36s  Updated", L.Value(name))
	if locked {
		text += " (locked)"
	}
	return r.emit("catalog_state", catalogSyncResult{Set: r.service.Set.ID, State: state, Result: result, Locked: locked}, text)
}

func (r *runtime) emitCatalogSummary(summary catalogSyncSummary) error {
	r.finishCatalogCheck()
	summary.RegistryCopiesUpdated = r.catalogReplicasUpdated
	var counts []string
	if summary.Updated > 0 {
		counts = append(counts, fmt.Sprintf("%d updated", summary.Updated))
	}
	if summary.UpToDate > 0 {
		counts = append(counts, fmt.Sprintf("%d up to date", summary.UpToDate))
	}
	if summary.Locked > 0 {
		counts = append(counts, fmt.Sprintf("%d locked", summary.Locked))
	}
	if summary.Failed > 0 {
		counts = append(counts, fmt.Sprintf("%d failed", summary.Failed))
	}
	text := "Catalog sync complete."
	level := L.INFO
	if summary.Failed > 0 {
		text = "Catalog sync incomplete."
		level = L.ERROR
	}
	if len(counts) > 0 {
		text += " " + strings.Join(counts, "; ") + "."
	}
	if summary.Updated == 0 && summary.Failed == 0 && summary.RegistryCopiesUpdated == 0 {
		switch {
		case summary.Checked > 0:
			noun := "catalogs"
			if summary.Checked == 1 {
				noun = "catalog"
			}
			text = fmt.Sprintf("Everything is up to date. %d %s checked", summary.Checked, noun)
			if summary.Locked > 0 {
				text += fmt.Sprintf("; %d locked", summary.Locked)
			}
			text += "."
		case summary.Skipped > 0:
			text = "No catalogs checked."
		default:
			text = "No catalogs found in the selected buckets."
		}
	}
	if summary.Skipped > 0 {
		text += fmt.Sprintf(" %d skipped", summary.Skipped)
		var reasons []string
		if summary.localOnly > 0 {
			reasons = append(reasons, fmt.Sprintf("%d local-only", summary.localOnly))
		}
		if summary.outOfScope > 0 {
			reasons = append(reasons, fmt.Sprintf("%d outside selection", summary.outOfScope))
		}
		if len(reasons) > 0 {
			text += " (" + strings.Join(reasons, ", ") + ")"
		}
		text += "."
	}
	if summary.RegistryCopiesUpdated > 0 {
		text += fmt.Sprintf(" %d registry copies updated.", summary.RegistryCopiesUpdated)
	}
	if summary.Locked > 0 {
		text += "\n  Open a cached catalog: glesha catalog unlock SET"
	}
	return r.emitAtLevel("catalog_sync_summary", summary, text, level)
}
