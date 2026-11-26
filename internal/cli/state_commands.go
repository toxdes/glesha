package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"glesha/internal/backup"
	"glesha/internal/database/model"
	"glesha/internal/logger"
)

func (r *runtime) selectedSets(args []string) ([]model.Set, error) {
	if r.env.All && len(args) > 0 || !r.env.All && len(args) != 1 {
		return nil, fmt.Errorf("cli: choose SET or --all")
	}
	if !r.env.All {
		set, err := r.registry.Set(r.ctx, args[0])
		return []model.Set{set}, setError(args[0], err)
	}
	sets := []model.Set{}
	err := r.registry.Each(r.ctx, func(set model.Set) error { sets = append(sets, set); return nil })
	return sets, err
}
func catalogSync(r *runtime, args []string) error {
	if r.env.Keep != "" || r.env.Authority != "" {
		return fmt.Errorf("cli: --keep and --authority require catalog resolve")
	}
	if len(args) > 1 || r.env.All && len(args) > 0 {
		return fmt.Errorf("cli: choose SET or bare catalog sync, not both SET and --all")
	}
	if err := r.selectCommandRemotes(); err != nil {
		return err
	}
	if err := r.beginCatalogCheck(); err != nil {
		return err
	}
	defer r.finishCatalogCheck()
	if len(args) == 0 {
		return catalogDiscover(r)
	}
	if _, err := r.registry.Set(r.ctx, args[0]); errors.Is(err, sql.ErrNoRows) {
		if discoveryErr := r.discoverState(r.env.From); discoveryErr != nil {
			return discoveryErr
		}
	} else if err != nil {
		return err
	}
	sets, err := r.selectedSets(args)
	if err != nil {
		return err
	}
	r.catalogSync.replicasUpdated = 0
	summary := catalogSyncSummary{}
	failures := catalogFailures{}
	for _, set := range sets {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if set.CatalogTo == "" || set.CatalogTo == "local" {
			summary.Skipped++
			summary.localOnly++
			if !r.env.jsonOutput() {
				continue
			}
			if err := r.emit("catalog_state", catalogSyncResult{Set: set.ID, State: backup.CatalogStateSkipped}, fmt.Sprintf("%s  SKIPPED  Local-only set", logger.ASCII(set.Name))); err != nil {
				return err
			}
			continue
		}
		updated, err := syncSetCatalog(r, set)
		if err != nil {
			summary.Failed++
			if outputErr := failures.addSet(r, set, err); outputErr != nil {
				return errors.Join(err, outputErr)
			}
		} else {
			if outputErr := r.emitCatalogResult(backup.CatalogStateSynced, updated, false, &summary); outputErr != nil {
				return outputErr
			}
		}
	}
	return failures.finish(r, summary)
}

func syncSetCatalog(r *runtime, set model.Set) (updated bool, result error) {
	if err := r.openSet(set.ID); err != nil {
		return false, err
	}
	if err := r.checkRemote(r.service.Set.CatalogTo); err != nil {
		return false, err
	}
	unlock, err := r.lock()
	if err != nil {
		return false, err
	}
	defer unlock()
	r.catalogSync.cacheUpdated = false
	before, err := r.localCatalogRevision()
	if err != nil {
		return false, err
	}
	defer func() {
		if e := r.service.RecordResult(context.WithoutCancel(r.ctx), "sync", result); e != nil {
			logger.Debug(e.Error())
		}
	}()
	defer func() { wipe(r.service.CatalogPassword) }()
	if r.service.Pending() {
		if err = r.syncStores(); err != nil {
			return false, err
		}
		if err = r.catalogSecret(true); err != nil {
			return false, err
		}
		if err = r.service.SyncCatalog(r.ctx); err != nil {
			return false, err
		}
	} else if err = r.prepareMutation(); err != nil {
		return false, err
	}
	if err = r.replicateDirectory(); err != nil {
		return false, err
	}
	after, err := r.localCatalogRevision()
	updated = r.catalogSync.cacheUpdated || before != after
	return updated, err
}

func catalogResolve(r *runtime, args []string) (result error) {
	if len(args) != 1 || r.env.All {
		return fmt.Errorf("cli: catalog resolve requires SET")
	}
	if r.env.Keep != "" && r.env.Keep != "local" && r.env.Keep != "remote" {
		return fmt.Errorf("cli: --keep must be local or remote")
	}
	if r.env.Authority != "" && r.env.Keep != "" {
		return fmt.Errorf("cli: choose --authority or --keep, not both")
	}
	if err := r.openSet(args[0]); err != nil {
		return err
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if r.env.Authority != "" {
		ids, err := r.remoteIDs([]string{r.env.Authority})
		if err != nil {
			return err
		}
		if err = r.stores(ids); err != nil {
			return err
		}
		if err = r.catalogSecret(false); err != nil {
			return err
		}
		defer wipe(r.service.CatalogPassword)
		if err = confirm(r.ctx, "Select "+r.env.Authority+" as catalog authority for "+r.service.Set.ID+"? Both original histories will be preserved.", r.env.AssumeYes); err != nil {
			return err
		}
		if err = r.service.ResolveAuthority(r.ctx, ids[0]); err != nil {
			return err
		}
		if err := r.publishState(); err != nil {
			return err
		}
		return r.result(fmt.Sprintf("Catalog resolved for %q.", r.service.Set.Name))
	}
	if r.env.Keep != "" {
		defer func() {
			if e := r.service.RecordResult(context.WithoutCancel(r.ctx), "sync", result); e != nil {
				logger.Debug(e.Error())
			}
		}()
	}
	if err = r.syncStores(); err != nil {
		return err
	}
	if err = r.cacheCatalog(); err != nil {
		return err
	}
	if err = r.service.CachedEncryption(r.ctx); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(r.service.Directory, "catalog.db")); errors.Is(err, os.ErrNotExist) {
		if err = r.readableCatalog(); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err = r.catalogSecret(true); err != nil {
		return err
	}
	defer wipe(r.service.CatalogPassword)
	if err = r.service.ResolutionDifferences(r.ctx, func(d backup.ResolutionDifference) error {
		return r.emit("resolution_difference", d, fmt.Sprintf("%s\n  Local   %s\n  Remote  %s", logger.ASCII(d.Field), logger.ASCII(d.Local), logger.ASCII(d.Remote)))
	}); err != nil {
		return err
	}
	if r.env.Keep == "" {
		return r.emit("resolution_choices", struct {
			Choices []string `json:"choices"`
		}{[]string{"local", "remote"}}, "Choose --keep local or --keep remote; both histories are preserved.")
	}
	if err = confirm(r.ctx, "Resolve "+r.service.Set.Name+" using "+r.env.Keep+" values for conflicting metadata?", r.env.AssumeYes); err != nil {
		return err
	}
	if err = r.service.Resolve(r.ctx, r.env.Keep); err != nil {
		return err
	}
	if err := r.publishState(); err != nil {
		return err
	}
	return r.completed("", "", fmt.Sprintf("Catalog resolved for %q.", r.service.Set.Name))
}

type cleanupWork struct {
	service *backup.Service
	plan    backup.CleanupPlan
}

func cleanup(r *runtime, args []string) error {
	sets, err := r.selectedSets(args)
	if err != nil {
		return err
	}
	var deferRelease []func()
	defer func() {
		for _, release := range deferRelease {
			release()
		}
	}()
	var plans []cleanupWork
	var failures []error
	var total int64
	count := 0
	for _, set := range sets {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		err := func() error {
			plan, release, err := planSetCleanup(r, set)
			if err != nil || release == nil {
				return err
			}
			deferRelease = append(deferRelease, release)
			plans = append(plans, cleanupWork{r.service, plan})
			for _, file := range plan.Files {
				if err := r.emit("cleanup_candidate", struct {
					Set  string `json:"set"`
					Path string `json:"path"`
					Size int64  `json:"size"`
				}{set.ID, file.Path, file.Size}, fmt.Sprintf("%10s  %s", compactSize(file.Size), logger.Value(file.Path))); err != nil {
					return err
				}
				total += file.Size
				count++
			}
			return nil
		}()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", set.Name, err))
			if outputErr := r.setEvent("set_error", set, model.STATUS_FAILED, err.Error()); outputErr != nil {
				return errors.Join(err, outputErr)
			}
		}
	}
	if count == 0 {
		if len(failures) > 0 {
			return reportedError("cleanup", errors.Join(failures...))
		}
		return r.emit("cleanup_summary", cleanupSummary{DryRun: r.env.DryRun}, "No local archives are eligible for cleanup.")
	}
	if err := r.emit("cleanup_summary", cleanupSummary{Files: count, Bytes: total, DryRun: r.env.DryRun}, fmt.Sprintf("\n%d local archives; %s reclaimable.", count, compactSize(total))); err != nil {
		return err
	}
	if r.env.DryRun {
		return reportedError("cleanup", errors.Join(failures...))
	}
	if err = confirm(r.ctx, "Remove these local archives? Remote copies will be kept.", r.env.AssumeYes); err != nil {
		return err
	}
	removals, err := removeCleanupFiles(r, plans)
	if err != nil {
		return err
	}
	return reportedError("cleanup", errors.Join(append(failures, removals...)...))
}

// planSetCleanup returns a nil release when the set has nothing to remove.
func planSetCleanup(r *runtime, set model.Set) (backup.CleanupPlan, func(), error) {
	if err := r.openSet(set.ID); err != nil {
		return backup.CleanupPlan{}, nil, err
	}
	unlock, err := r.lock()
	if err != nil {
		return backup.CleanupPlan{}, nil, err
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	// skip pending work before opening cloud connections.
	reason, err := r.service.CleanupBlock(r.ctx)
	if err != nil {
		return backup.CleanupPlan{}, nil, err
	}
	if reason != "" {
		return backup.CleanupPlan{}, nil, r.setEvent("skipped", set, "", reason)
	}
	if r.env.DryRun {
		if err = r.syncStores(); err != nil {
			return backup.CleanupPlan{}, nil, err
		}
		if err = r.catalogSecret(false); err != nil {
			return backup.CleanupPlan{}, nil, err
		}
		if err = r.service.Pull(r.ctx); err != nil {
			return backup.CleanupPlan{}, nil, err
		}
	} else if err = r.prepareMutation(); err != nil {
		return backup.CleanupPlan{}, nil, err
	}
	defer wipe(r.service.CatalogPassword)
	ids, err := r.service.CleanupRemotes(r.ctx)
	if err != nil {
		return backup.CleanupPlan{}, nil, err
	}
	if err = r.stores(ids); err != nil {
		return backup.CleanupPlan{}, nil, err
	}
	plan, err := r.service.PlanCleanup(r.ctx)
	if err != nil {
		return backup.CleanupPlan{}, nil, err
	}
	if plan.Skipped != "" {
		return backup.CleanupPlan{}, nil, r.setEvent("skipped", set, "", plan.Skipped)
	}
	if len(plan.Files) == 0 {
		return backup.CleanupPlan{}, nil, nil
	}
	release := unlock
	unlock = nil
	return plan, release, nil
}

// removeCleanupFiles deletes the planned files and reports each one. It returns
// the files it could not remove as failures, and err only when output fails.
func removeCleanupFiles(r *runtime, plans []cleanupWork) (failures []error, err error) {
	removed := 0
	var reclaimed int64
	for _, work := range plans {
		for _, file := range work.plan.Files {
			if err := work.service.RemoveCleanupFile(r.ctx, file); err != nil {
				failures = append(failures, err)
				if outputErr := r.emitAtLevel("", nil, fmt.Sprintf("Could not remove %q: %s", file.Path, err), logger.ERROR); outputErr != nil {
					return nil, errors.Join(err, outputErr)
				}
			} else {
				if err := r.emit("cleanup_removed", struct {
					Path string `json:"path"`
					Size int64  `json:"size"`
				}{file.Path, file.Size}, ""); err != nil {
					return nil, err
				}
				removed++
				reclaimed += file.Size
			}
		}
	}
	if err := r.result(fmt.Sprintf("Removed %d local archives; reclaimed %s.", removed, compactSize(reclaimed))); err != nil {
		return nil, err
	}
	return failures, nil
}

type cleanupSummary struct {
	Files  int   `json:"candidate_files"`
	Bytes  int64 `json:"reclaimable_bytes"`
	DryRun bool  `json:"dry_run"`
}

func (r *runtime) setEvent(kind string, set model.Set, status model.Status, reason string) error {
	label := "SKIPPED"
	if status == model.STATUS_FAILED {
		label = "FAILED"
	} else if status == model.STATUS_COMPLETED {
		label = "SYNCED"
	}
	level := logger.INFO
	if kind == "set_error" {
		level = logger.ERROR
	} else if kind == "skipped" {
		level = logger.WARN
	}
	return r.emitAtLevel(kind, struct {
		Set    string       `json:"set"`
		Name   string       `json:"name"`
		State  model.Status `json:"state,omitempty"`
		Reason string       `json:"reason,omitempty"`
	}{set.ID, set.Name, status, reason}, strings.TrimSpace(fmt.Sprintf("%s  %s  %s", logger.Value(set.Name), label, logger.Value(reason))), level)
}
