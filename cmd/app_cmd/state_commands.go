package app_cmd

import (
	"context"
	"errors"
	"fmt"

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
			fmt.Fprintf(r.out, "%s  SKIPPED  Local-only set\n", L.ASCII(set.Name))
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
			fmt.Fprintf(r.out, "%s  FAILED  %s\n", L.ASCII(set.Name), L.ASCII(err.Error()))
			failures = append(failures, fmt.Errorf("%s: %w", set.Name, err))
		} else {
			fmt.Fprintf(r.out, "%s  SYNCED\n", L.ASCII(set.Name))
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
		_, err := fmt.Fprintf(r.out, "%s\n  Local   %s\n  Remote  %s\n", L.ASCII(d.Field), L.ASCII(d.Local), L.ASCII(d.Remote))
		return err
	}); err != nil {
		return err
	}
	if r.env.Keep == "" {
		fmt.Fprintln(r.out, "Choose --keep local or --keep remote; both histories are preserved.")
		return nil
	}
	if err = confirm(r.ctx, "Resolve "+r.service.Set.Name+" using "+r.env.Keep+" values for conflicting metadata?", r.env.AssumeYes); err != nil {
		return err
	}
	return r.service.Resolve(r.ctx, r.env.Keep)
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
				fmt.Fprintf(r.out, "%s  SKIPPED  %s\n", L.ASCII(set.Name), reason)
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
				fmt.Fprintf(r.out, "%s  SKIPPED  %s\n", L.ASCII(set.Name), plan.Skipped)
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
				fmt.Fprintf(r.out, "%s  %s\n", compactSize(file.Size), L.ASCII(file.Path))
				total += file.Size
				count++
			}
			return nil
		}()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", set.Name, err))
			fmt.Fprintf(r.out, "%s  SKIPPED  %s\n", L.ASCII(set.Name), L.ASCII(err.Error()))
		}
	}
	if count == 0 {
		fmt.Fprintln(r.out, "No archives eligible for cleanup.")
		return errors.Join(failures...)
	}
	fmt.Fprintf(r.out, "\n%d local archives; %s reclaimable.\n", count, compactSize(total))
	fmt.Fprintln(r.out, "Remote backups and catalogs are kept; cold copies require retrieval to download again.")
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
			}
		}
	}
	return errors.Join(failures...)
}
