package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"glesha/internal/backup"
	"glesha/internal/cloud"
	"glesha/internal/database/model"
	"glesha/internal/logger"
)

// allowedInTrash names the commands that work on a set in the trash; they
// read it, or delete or restore it.
var allowedInTrash = map[string]bool{
	"restore": true, "browse": true, "history": true, "status": true, "list": true,
	"catalog": true, "delete": true, "undelete": true,
}

func trashError(set model.Set) error {
	return fmt.Errorf("cli: %s is in trash; run glesha undelete %s", set.Name, set.Name)
}

// deletionTarget is one set named on the command line, with its own service
// and writer lock.
type deletionTarget struct {
	service *backup.Service
	release func()
	sizes   map[string]cloud.Freed
}

func (t *deletionTarget) total() cloud.Freed {
	var all cloud.Freed
	for _, size := range t.sizes {
		all.Add(size)
	}
	return all
}

type deletionRecord struct {
	Set      string     `json:"set"`
	Name     string     `json:"name"`
	State    string     `json:"state"`
	Deadline *time.Time `json:"deadline,omitempty"`
	Objects  int        `json:"objects"`
	Bytes    int64      `json:"bytes"`
	Remotes  []string   `json:"remotes,omitempty"`
	DryRun   bool       `json:"dry_run,omitempty"`
}

func deleteSets(r *runtime, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("cli: delete requires at least one set; see glesha help delete")
	}
	period, err := r.config.TrashPeriod()
	if err != nil {
		return err
	}
	targets, err := r.openDeletionTargets(args, true)
	defer releaseTargets(targets)
	if err != nil {
		return err
	}
	permanent := r.env.Now || period == 0
	if err = r.listDeletionTargets(targets, permanent); err != nil {
		return err
	}
	if r.env.DryRun {
		return nil
	}
	if permanent {
		return r.deletePermanently(targets)
	}
	return r.moveToTrash(targets, period)
}

// openDeletionTargets opens and locks each named set once, in the given
// order, with stores for every remote the command may access.
func (r *runtime) openDeletionTargets(names []string, measure bool) ([]*deletionTarget, error) {
	ids, err := r.remoteIDs(r.selectedRemotes())
	if err != nil {
		return nil, err
	}
	var targets []*deletionTarget
	seen := map[string]bool{}
	for _, name := range names {
		if err = r.openSet(name); err != nil {
			return targets, err
		}
		if seen[r.service.Set.ID] {
			continue
		}
		seen[r.service.Set.ID] = true
		release, err := r.lock()
		if err != nil {
			return targets, fmt.Errorf("cli: %s: %w", name, err)
		}
		target := &deletionTarget{service: r.service, release: release}
		targets = append(targets, target)
		if err = r.stores(ids); err != nil {
			return targets, err
		}
		if !measure {
			continue
		}
		if locked, err := r.service.CatalogLocked(r.ctx); err != nil {
			return targets, err
		} else if locked {
			return targets, fmt.Errorf("cli: catalog of %s is locked; run glesha catalog unlock %s to find its imported archives", name, name)
		}
		if target.sizes, err = r.service.DeletionSize(r.ctx); err != nil {
			return targets, err
		}
	}
	return targets, nil
}

func releaseTargets(targets []*deletionTarget) {
	for _, t := range targets {
		t.release()
	}
}

func (r *runtime) listDeletionTargets(targets []*deletionTarget, permanent bool) error {
	width := 0
	for _, t := range targets {
		width = max(width, len(logger.ASCII(t.service.Set.Name)))
	}
	for _, t := range targets {
		var remotes []string
		for _, id := range sortedRemoteIDs(t.sizes) {
			if t.sizes[id].Objects > 0 {
				remotes = append(remotes, r.remoteLabel(id))
			}
		}
		total := t.total()
		text := fmt.Sprintf("%-*s  %6s  %5d objects  %s", width, logger.ASCII(t.service.Set.Name), compactSize(total.Bytes), total.Objects, strings.Join(remotes, ", "))
		state := "trash"
		if permanent {
			state = "deleted"
		}
		record := deletionRecord{Set: t.service.Set.ID, Name: t.service.Set.Name, State: state, Objects: total.Objects, Bytes: total.Bytes, Remotes: remotes, DryRun: true}
		if err := r.emit("deletion_candidate", record, text); err != nil {
			return err
		}
	}
	return nil
}

func (r *runtime) moveToTrash(targets []*deletionTarget, period time.Duration) error {
	subject := plural(len(targets), "set")
	if err := confirm(r.ctx, fmt.Sprintf("Move %s to trash? Deleted after %s.", subject, deletionTime(time.Now().Add(period))), r.env.AssumeYes); err != nil {
		return err
	}
	var failures []error
	var deadline time.Time
	var freed int64
	var names []string
	for _, t := range targets {
		set := t.service.Set
		trash, err := t.service.Trash(r.ctx, period)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", set.Name, err))
			if t.service.Set.Trash == nil {
				continue
			}
			trash = *t.service.Set.Trash
		}
		names = append(names, set.Name)
		if trash.Deadline.After(deadline) {
			deadline = trash.Deadline
		}
		total := t.total()
		freed += total.Bytes
		record := deletionRecord{Set: set.ID, Name: set.Name, State: "trash", Deadline: &trash.Deadline, Objects: total.Objects, Bytes: total.Bytes}
		if err = r.emit("deletion", record, ""); err != nil {
			return err
		}
	}
	if len(names) > 0 {
		list := strings.Join(names, " ")
		if err := r.result(fmt.Sprintf("Moved %s to trash; %s will be freed after %s.\n  Undo: glesha undelete %s\n  Delete now: glesha delete --now %s\nStorage is billed until then. The next glesha command that connects to a remote after that time deletes %s.",
			plural(len(names), "set"), compactSize(freed), deletionTime(deadline), list, list, pronoun(len(names)))); err != nil {
			return err
		}
	}
	return reportedError("delete", errors.Join(failures...))
}

func (r *runtime) deletePermanently(targets []*deletionTarget) error {
	terminal := term.IsTerminal(int(os.Stdin.Fd()))
	if !r.env.AssumeYes && terminal {
		fmt.Fprintln(os.Stderr, "Every stored version will be deleted. This cannot be undone.")
	}
	in := bufio.NewReader(os.Stdin)
	for _, t := range targets {
		label, name := "set name", t.service.Set.Name
		if name == t.service.Set.ID {
			label = "set UUID"
		}
		if err := readTypedConfirmation(r.ctx, in, os.Stderr, terminal, label, name, r.env.AssumeYes); err != nil {
			return err
		}
	}
	required, err := r.remoteIDs(r.configuredRemotes())
	if err != nil {
		return err
	}
	var failures []error
	var freed int64
	deleted := 0
	for _, t := range targets {
		set := t.service.Set
		result, err := purgeTarget(r, t.service, required, true)
		freed += result.Freed.Bytes
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", set.Name, err))
			continue
		}
		deleted++
		record := deletionRecord{Set: set.ID, Name: set.Name, State: "deleted", Objects: result.Freed.Objects, Bytes: result.Freed.Bytes}
		if err = r.emit("deletion", record, ""); err != nil {
			return err
		}
	}
	if deleted > 0 {
		if err := r.result(fmt.Sprintf("Deleted %s; freed %s.", plural(deleted, "set"), compactSize(freed))); err != nil {
			return err
		}
	}
	return reportedError("delete", errors.Join(failures...))
}

// purgeTarget publishes the trash marker, so another device can finish an
// interrupted deletion, then deletes the set from every open remote.
func purgeTarget(r *runtime, s *backup.Service, required []string, immediately bool) (backup.Deletion, error) {
	if s.Set.Trash == nil {
		if _, err := s.Trash(r.ctx, 0); err != nil {
			return backup.Deletion{}, err
		}
	}
	result, err := s.Purge(r.ctx, required, immediately)
	for _, kept := range result.Kept {
		logger.Info(fmt.Sprintf("Kept %s on %s; %s.", kept.Object.Key, r.remoteLabel(kept.Object.Remote), kept.Reason))
	}
	if err == nil && !result.Done {
		err = fmt.Errorf("cli: some configured remotes were not reachable or not selected; the deletion continues with the next glesha command that can reach every configured remote")
	}
	return result, err
}

func undeleteSets(r *runtime, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("cli: undelete requires at least one set; see glesha help undelete")
	}
	targets, err := r.openDeletionTargets(args, false)
	defer releaseTargets(targets)
	if err != nil {
		return err
	}
	for _, t := range targets {
		set := t.service.Set
		switch err := t.service.Undelete(r.ctx); {
		case errors.Is(err, backup.ErrNotInTrash):
			return fmt.Errorf("cli: %s is not in trash", set.Name)
		case errors.Is(err, backup.ErrDeletionStarted):
			return fmt.Errorf("cli: deletion of %s has started and cannot be undone", set.Name)
		case err != nil:
			return err
		}
		if err := r.emit("deletion", deletionRecord{Set: set.ID, Name: set.Name, State: "restored"}, fmt.Sprintf("Restored %s from trash.", logger.ASCII(set.Name))); err != nil {
			return err
		}
	}
	return nil
}

// finishDueDeletions deletes the sets whose trash period has ended, before
// the command runs. It reports on stderr, so command output stays clean, and
// a failure only warns: the next command tries again.
func (r *runtime) finishDueDeletions() {
	if r.registry == nil || r.command == "delete" || r.command == "undelete" {
		return
	}
	var due []model.Set
	if err := r.registry.Each(r.ctx, func(set model.Set) error {
		if set.Trash != nil && set.Trash.Due(time.Now()) {
			due = append(due, set)
		}
		return nil
	}); err != nil || len(due) == 0 {
		return
	}
	required, err := r.remoteIDs(r.configuredRemotes())
	if err == nil {
		var open []string
		if open, err = r.remoteIDs(r.selectedRemotes()); err == nil {
			for _, set := range due {
				r.finishDueDeletion(set, required, open)
			}
		}
	}
	if err != nil {
		logger.Warn(fmt.Sprintf("could not finish deletions from trash: %s", err))
	}
	r.service, r.operationLocked = nil, false
}

func (r *runtime) finishDueDeletion(set model.Set, required, open []string) {
	r.service = &backup.Service{Config: r.config, Budget: r.budget, Registry: r.registry, Stores: map[string]cloud.Store{}}
	err := r.service.OpenSet(r.ctx, set.ID, r.root)
	var release func()
	if err == nil {
		release, err = r.lock()
	}
	if err == nil {
		defer release()
		err = r.stores(open)
	}
	var result backup.Deletion
	if err == nil {
		result, err = purgeTarget(r, r.service, required, false)
	}
	if errors.Is(err, backup.ErrNotDue) || errors.Is(err, backup.ErrNotInTrash) {
		return
	}
	if err != nil {
		logger.Warn(fmt.Sprintf("could not finish deleting %s from trash: %s", set.Name, err))
		return
	}
	logger.Info(fmt.Sprintf("Finished deleting %s; freed %s.", set.Name, compactSize(result.Freed.Bytes)))
}

// skipDeleting leaves a set whose deletion has started out of catalog sync;
// its catalog may already be gone.
func (r *runtime) skipDeleting(set model.Set, summary *catalogSyncSummary) (bool, error) {
	if set.Trash == nil || !set.Trash.Started {
		return false, nil
	}
	summary.Skipped++
	summary.deleting++
	if !r.env.jsonOutput() {
		return true, nil
	}
	return true, r.emit("catalog_state", catalogSyncResult{Set: set.ID, State: backup.CatalogStateSkipped, Reason: "deletion not finished"}, fmt.Sprintf("%s  SKIPPED  Deletion not finished", logger.ASCII(set.Name)))
}

func deletionTime(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func pronoun(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

func sortedRemoteIDs(sizes map[string]cloud.Freed) []string {
	ids := make([]string, 0, len(sizes))
	for id := range sizes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
