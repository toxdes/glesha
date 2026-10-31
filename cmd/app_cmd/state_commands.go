package app_cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"glesha/backup"
	"glesha/database/model"
	L "glesha/logger"
)

func (r *runtime) selectedSets(args []string) ([]model.Set, error) {
	if r.env.All && len(args) > 0 || !r.env.All && len(args) != 1 {
		return nil, fmt.Errorf("cli: choose SET or --all")
	}
	if !r.env.All {
		set, err := r.registry.Set(r.ctx, args[0])
		return []model.Set{set}, err
	}
	sets := []model.Set{}
	err := r.registry.Each(r.ctx, func(set model.Set) error { sets = append(sets, set); return nil })
	return sets, err
}
func catalogSync(r *runtime, args []string) error {
	if r.env.Keep != "" {
		return fmt.Errorf("cli: --keep requires catalog resolve")
	}
	sets, err := r.selectedSets(args)
	if err != nil {
		return err
	}
	var failures []error
	for _, set := range sets {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if set.CatalogTo == "" || set.CatalogTo == "local" {
			if err := r.setEvent("skipped", set, "", "Local-only set"); err != nil {
				return err
			}
			continue
		}
		err := func() (result error) {
			if err := r.openSet(set.ID); err != nil {
				return err
			}
			unlock, err := r.lock()
			if err != nil {
				return err
			}
			defer unlock()
			defer func() {
				if e := r.service.RecordResult(context.WithoutCancel(r.ctx), "sync", result); e != nil {
					L.Debug(e.Error())
				}
			}()
			ids, err := r.service.CleanupRemotes(r.ctx)
			if err != nil {
				return err
			}
			ids = append(ids, set.To...)
			if err = r.stores(ids); err != nil {
				return err
			}
			if err = r.syncStores(); err != nil {
				return err
			}
			if err = r.catalogSecret(true); err != nil {
				return err
			}
			defer wipe(r.service.CatalogPassword)
			return r.service.Sync(r.ctx)
		}()
		if err != nil {
			if outputErr := r.setEvent("set_error", set, model.STATUS_FAILED, err.Error()); outputErr != nil {
				return outputErr
			}
			failures = append(failures, fmt.Errorf("%s: %w", set.Name, err))
		} else {
			if err := r.setEvent("synced", set, model.STATUS_COMPLETED, ""); err != nil {
				return err
			}
		}
	}
	return errors.Join(failures...)
}
func catalogResolve(r *runtime, args []string) (result error) {
	if len(args) != 1 || r.env.All {
		return fmt.Errorf("cli: catalog resolve requires SET")
	}
	if r.env.Keep != "" && r.env.Keep != "local" && r.env.Keep != "remote" {
		return fmt.Errorf("cli: --keep must be local or remote")
	}
	if err := r.openSet(args[0]); err != nil {
		return err
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if r.env.Keep != "" {
		defer func() {
			if e := r.service.RecordResult(context.WithoutCancel(r.ctx), "sync", result); e != nil {
				L.Debug(e.Error())
			}
		}()
	}
	if err = r.syncStores(); err != nil {
		return err
	}
	if err = r.catalogSecret(true); err != nil {
		return err
	}
	defer wipe(r.service.CatalogPassword)
	if err = r.service.ResolutionDifferences(r.ctx, func(d backup.ResolutionDifference) error {
		return r.emit("resolution_difference", d, fmt.Sprintf("%s\n  Local   %s\n  Remote  %s", L.ASCII(d.Field), L.ASCII(d.Local), L.ASCII(d.Remote)))
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
	return r.completed("", "", "")
}
func cleanup(r *runtime, args []string) error {
	sets, err := r.selectedSets(args)
	if err != nil {
		return err
	}
	type work struct {
		service *backup.Service
		plan    backup.CleanupPlan
	}
	var deferRelease []func()
	defer func() {
		for _, release := range deferRelease {
			release()
		}
	}()
	var plans []work
	var failures []error
	var total int64
	count := 0
	for _, set := range sets {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		err := func() error {
			if err := r.openSet(set.ID); err != nil {
				return err
			}
			unlock, err := r.lock()
			if err != nil {
				return err
			}
			defer func() {
				if unlock != nil {
					unlock()
				}
			}()
			// skip pending work before opening cloud connections.
			reason, err := r.service.CleanupBlock(r.ctx)
			if err != nil {
				return err
			}
			if reason != "" {
				if err := r.setEvent("skipped", set, "", reason); err != nil {
					return err
				}
				return nil
			}
			ids, err := r.service.CleanupRemotes(r.ctx)
			if err != nil {
				return err
			}
			if err = r.stores(ids); err != nil {
				return err
			}
			plan, err := r.service.PlanCleanup(r.ctx)
			if err != nil {
				return err
			}
			if plan.Skipped != "" {
				if err := r.setEvent("skipped", set, "", plan.Skipped); err != nil {
					return err
				}
				return nil
			}
			if len(plan.Files) == 0 {
				return nil
			}
			release := unlock
			unlock = nil
			deferRelease = append(deferRelease, release)
			plans = append(plans, work{r.service, plan})
			for _, file := range plan.Files {
				if err := r.emit("cleanup_candidate", struct {
					Set  string `json:"set"`
					Path string `json:"path"`
					Size int64  `json:"size"`
				}{set.ID, file.Path, file.Size}, fmt.Sprintf("%s  %s", compactSize(file.Size), L.ASCII(file.Path))); err != nil {
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
				return outputErr
			}
		}
	}
	if count == 0 {
		if err := r.emit("cleanup_summary", cleanupSummary{DryRun: r.env.DryRun}, "No archives eligible for cleanup."); err != nil {
			return err
		}
		return errors.Join(failures...)
	}
	if err := r.emit("cleanup_summary", cleanupSummary{Files: count, Bytes: total, DryRun: r.env.DryRun}, fmt.Sprintf("\n%d local archives; %s reclaimable.\nRemote backups and catalogs are kept; cold copies require retrieval to download again.", count, compactSize(total))); err != nil {
		return err
	}
	if r.env.DryRun {
		return errors.Join(failures...)
	}
	if err = confirm(r.ctx, "Remove these local archives?", r.env.AssumeYes); err != nil {
		return err
	}
	for _, w := range plans {
		for _, file := range w.plan.Files {
			if err = w.service.RemoveCleanupFile(r.ctx, file); err != nil {
				failures = append(failures, err)
			} else if err := r.emit("cleanup_removed", struct {
				Path string `json:"path"`
				Size int64  `json:"size"`
			}{file.Path, file.Size}, ""); err != nil {
				return err
			}
		}
	}
	return errors.Join(failures...)
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
	return r.emit(kind, struct {
		Set    string       `json:"set"`
		Name   string       `json:"name"`
		State  model.Status `json:"state,omitempty"`
		Reason string       `json:"reason,omitempty"`
	}{set.ID, set.Name, status, reason}, strings.TrimSpace(fmt.Sprintf("%s  %s  %s", L.ASCII(set.Name), label, L.ASCII(reason))))
}
