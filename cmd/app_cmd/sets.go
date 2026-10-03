package app_cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"glesha/archive"
	"glesha/backup"
	"glesha/cloud"
	"glesha/database/model"
	"glesha/file_io"
	L "glesha/logger"
)

func (r *runtime) emit(kind string, v any, text string) error {
	if r.env.JSON {
		return json.NewEncoder(r.out).Encode(struct {
			Version int    `json:"version"`
			Kind    string `json:"kind"`
			Data    any    `json:"data"`
		}{2, kind, v})
	}
	_, err := fmt.Fprintln(r.out, L.HumanText(text, term.IsTerminal(int(os.Stdout.Fd()))))
	return err
}
func create(r *runtime, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("cli: create requires SET and source paths")
	}
	roots, err := archive.Roots(args[1:])
	if err != nil {
		return err
	}
	set, err := backup.NewSet(args[0], roots, r.config)
	if err != nil {
		return err
	}
	set.Chunked = r.env.Chunked
	if r.env.SpoolMax != "" {
		if !set.Chunked {
			return fmt.Errorf("cli: --spool-max requires --chunked")
		}
		set.SpoolMax, err = backup.ParseSpoolSize(r.env.SpoolMax)
		if err != nil {
			return err
		}
		if set.SpoolMax < backup.MinimumSpoolMax {
			return fmt.Errorf("cli: spool requires at least 2MiB")
		}
	}
	set.To, err = r.remoteIDs(r.env.To)
	if err != nil {
		return err
	}
	set.CatalogTo, err = r.catalogDestination(set.To)
	if err != nil {
		return err
	}
	if r.env.Class != "" {
		if err = backup.ValidateClass(r.env.Class); err != nil {
			return err
		}
		set.Class = r.env.Class
	}
	if r.env.IncrementalClass != "" {
		if err = backup.ValidateClass(r.env.IncrementalClass); err != nil {
			return err
		}
		set.IncrementalClass = r.env.IncrementalClass
	}
	if err = r.registry.Create(r.ctx, set); err != nil {
		return fmt.Errorf("cli: set already exists or cannot be registered: %w", err)
	}
	return r.emit("set", set, fmt.Sprintf("Created %q.", set.Name))
}
func (r *runtime) catalogDestination(to []string) (string, error) {
	if r.env.CatalogTo == "local" {
		return "local", nil
	}
	if r.env.CatalogTo != "" {
		ids, err := r.remoteIDs([]string{r.env.CatalogTo})
		if err != nil {
			return "", err
		}
		return ids[0], nil
	}
	if len(to) > 0 {
		return to[0], nil
	}
	return "", nil
}
func configure(r *runtime, args []string) error {
	unlock, err := requireSet(r, args)
	if err != nil {
		return err
	}
	defer unlock()
	s := r.service
	if r.env.present["chunked"] {
		s.Set.Chunked = r.env.Chunked
	}
	if r.env.SpoolMax != "" {
		if !s.Set.Chunked {
			return fmt.Errorf("cli: --spool-max requires --chunked")
		}
		size, err := backup.ParseSpoolSize(r.env.SpoolMax)
		if err != nil {
			return err
		}
		if size < backup.MinimumSpoolMax {
			return fmt.Errorf("cli: spool requires at least 2MiB")
		}
		s.Set.SpoolMax = size
	}
	if s.Pending() {
		return fmt.Errorf("%w: finish pending work before configuring", backup.ErrPending)
	}
	roots, err := rootMappings(r.env.Roots)
	if err != nil {
		return err
	}
	if len(r.env.To) > 0 {
		ids, err := r.remoteIDs(r.env.To)
		if err != nil {
			return err
		}
		if s.Set.Authority != "" && (len(ids) != 1 || ids[0] != s.Set.Authority) {
			return fmt.Errorf("cli: incremental set destination changes require move")
		}
		s.Set.To = ids
	}
	if r.env.CatalogTo != "" {
		target, err := r.catalogDestination(s.Set.To)
		if err != nil {
			return err
		}
		if target != s.Set.CatalogTo {
			c, err := s.Catalog(r.ctx)
			if err != nil {
				return err
			}
			revision, err := c.GetMeta(r.ctx, "remote_revision")
			c.Close()
			if err != nil {
				return err
			}
			if revision != "" {
				return fmt.Errorf("cli: changing a published catalog remote requires move")
			}
		}
		s.Set.CatalogTo = target
	}
	if r.env.Compression != "" {
		s.Set.Compression = r.env.Compression
	}
	if r.env.Level > 0 {
		s.Set.Level = r.env.Level
	}
	if r.env.Prefix != "" {
		s.Set.Prefix = r.env.Prefix
	}
	if r.env.Class != "" {
		if err = backup.ValidateClass(r.env.Class); err != nil {
			return err
		}
		s.Set.Class = r.env.Class
	}
	if r.env.IncrementalClass != "" {
		if err = backup.ValidateClass(r.env.IncrementalClass); err != nil {
			return err
		}
		s.Set.IncrementalClass = r.env.IncrementalClass
	}
	if r.env.Baseline != "" {
		c, err := s.Catalog(r.ctx)
		if err != nil {
			return err
		}
		v, err := backup.Resolve(r.ctx, c, r.env.Baseline)
		c.Close()
		if err != nil {
			return err
		}
		if v.Status != model.STATUS_COMPLETED {
			return fmt.Errorf("cli: baseline is not completed")
		}
	}
	if len(roots) > 0 {
		if err = s.BindRoots(r.ctx, roots); err != nil {
			return err
		}
	}
	if err = r.registry.Save(r.ctx, s.Set); err != nil {
		return err
	}
	if r.env.Baseline != "" {
		if err = s.SetBaseline(r.ctx, r.env.Baseline); err != nil {
			return err
		}
	}
	return r.emit("set", s.Set, "Configuration saved.")
}
func rootMappings(values mappings) ([]model.Root, error) {
	out := []model.Root{}
	for name, p := range values {
		if strings.Contains(name, "/") {
			return nil, fmt.Errorf("cli: root name must be a basename")
		}
		if _, err := archive.SafeName(name); err != nil {
			return nil, err
		}
		expanded, err := file_io.Expand(p)
		if err != nil {
			return nil, err
		}
		expanded, err = filepath.Abs(expanded)
		if err != nil {
			return nil, err
		}
		if actual, err := filepath.EvalSymlinks(expanded); err == nil {
			expanded = actual
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		out = append(out, model.Root{Name: name, Path: expanded})
	}
	return out, nil
}
func run(r *runtime, args []string) error {
	unlock, err := requireSet(r, args)
	if err != nil {
		return err
	}
	defer unlock()
	s := r.service
	chunked := s.Set.Chunked
	if r.env.present["chunked"] {
		chunked = r.env.Chunked
	}
	if chunked && (r.env.KeepArchive || r.env.Output != "" || r.env.Mode == "memory") {
		return fmt.Errorf("cli: --chunked cannot use --keep-archive, --output or memory archive mode")
	}
	var spool int64
	if r.env.SpoolMax != "" {
		if !chunked && !s.Pending() {
			return fmt.Errorf("cli: --spool-max requires --chunked")
		}
		spool, err = backup.ParseSpoolSize(r.env.SpoolMax)
		if err != nil {
			return err
		}
		if spool < backup.MinimumSpoolMax {
			return fmt.Errorf("cli: spool requires at least 2MiB")
		}
	}
	to, err := r.remoteIDs(r.env.To)
	if err != nil {
		return err
	}
	if len(to) == 0 && !s.Pending() {
		to = s.Set.To
	}
	if len(to) == 0 && !s.Pending() {
		return fmt.Errorf("cli: --to is required at creation or run time")
	}
	if !s.Pending() {
		if err = s.ValidateRoots(); err != nil {
			return err
		}
	}
	required := to
	if s.Pending() {
		required, err = s.RequiredRemotes(r.ctx)
		if err != nil {
			return err
		}
	}
	if err = r.stores(required); err != nil {
		return err
	}
	if s.Set.CatalogTo == "" && len(to) > 0 {
		s.Set.CatalogTo = to[0]
	}
	if err = r.syncStores(); err != nil {
		return err
	}
	if err = r.catalogSecret(true); err != nil {
		return err
	}
	defer wipe(s.CatalogPassword)
	var pw []byte
	needsArchive, err := s.NeedsArchive(r.ctx)
	if err != nil {
		return err
	}
	if needsArchive {
		pw, err = password(r.env.PasswordFile, "Archive passphrase", true)
		if err != nil {
			return err
		}
		defer wipe(pw)
	} else {
		fmt.Fprintln(os.Stderr, "Resuming previous run.")
	}
	v, err := s.Run(r.ctx, backup.RunOptions{Chunked: r.env.Chunked, ChunkedSet: r.env.present["chunked"], SpoolMax: spool, To: to, Incremental: r.env.Incremental, Full: r.env.Full, KeepArchive: r.env.KeepArchive, Output: r.env.Output, Compression: r.env.Compression, Level: r.env.Level, Class: r.env.Class, Mode: r.env.Mode, Prefix: r.env.Prefix, Password: pw, Confirm: func(ctx context.Context, message string) error { return confirm(ctx, message, r.env.AssumeYes) }})
	if err != nil {
		return err
	}
	if v.ID == "" {
		fmt.Fprintln(r.out, "No changes.")
		return nil
	}
	message := fmt.Sprintf("Backup %s completed for %q.", v.ID, s.Set.Name)
	if _, err := os.Lstat(v.File); err == nil {
		message += fmt.Sprintf(" Archive: %q", v.File)
	}
	return r.emit("snapshot", v, message)
}
func retry(r *runtime, args []string) error {
	unlock, err := requireSet(r, args)
	if err != nil {
		return err
	}
	defer unlock()
	s := r.service
	if !s.Pending() {
		return fmt.Errorf("cli: no pending operation")
	}
	if r.env.KeepArchive {
		if err = s.RetainPendingArchive(r.ctx); err != nil {
			return err
		}
	}
	ids, err := s.RequiredRemotes(r.ctx)
	if err != nil {
		return err
	}
	if err = r.stores(ids); err != nil {
		return err
	}
	if err = r.syncStores(); err != nil {
		return err
	}
	if err = r.catalogSecret(true); err != nil {
		return err
	}
	defer wipe(s.CatalogPassword)
	if r.env.Archive != "" {
		if err = s.Relocate(r.ctx, r.env.Archive); err != nil {
			return err
		}
	}
	return s.Retry(r.ctx)
}
func history(r *runtime, args []string) error {
	if err := count(args, 1); err != nil {
		return err
	}
	if err := r.openSet(args[0]); err != nil {
		return err
	}
	if r.env.Refresh {
		if err := r.stores(r.service.Set.To); err != nil {
			return err
		}
		if err := r.service.Refresh(r.ctx); err != nil {
			return err
		}
	}
	return r.service.History(r.ctx, func(v model.Snapshot, ls []model.Location) error {
		date := "-"
		if !v.Imported {
			date = v.Created.Format("2006-01-02 15:04 UTC")
		} else if v.BackupDate != nil {
			date = v.BackupDate.Format("2006-01-02 15:04 UTC")
		}
		mode := "full"
		if !v.Full {
			mode = "incremental"
		}
		return r.emit("snapshot", struct {
			Snapshot  model.Snapshot   `json:"snapshot"`
			Locations []model.Location `json:"locations"`
		}{v, ls}, fmt.Sprintf("%s  %s  %s  %s%s", v.ID, date, mode, L.Bytes(v.Size), r.locationSummary(ls)))
	})
}
func browse(r *runtime, args []string) error {
	if err := count(args, 1); err != nil {
		return err
	}
	if err := r.openSet(args[0]); err != nil {
		return err
	}
	c, err := r.service.Catalog(r.ctx)
	if err != nil {
		return err
	}
	snapshot, err := backup.Resolve(r.ctx, c, r.env.Snapshot)
	c.Close()
	if err != nil {
		return err
	}
	return r.service.Browse(r.ctx, snapshot.ID, r.env.Filter, r.env.Recursive, func(e model.Entry) error {
		return r.emit("entry", struct {
			Snapshot string      `json:"snapshot"`
			Entry    model.Entry `json:"entry"`
		}{snapshot.ID, e}, fmt.Sprintf("%-10s %10s %q", e.Type, L.Bytes(e.Size), e.Path))
	})
}
func list(r *runtime, args []string) error {
	if err := count(args, 0); err != nil {
		return err
	}
	if !r.env.Remote {
		return r.registry.Each(r.ctx, func(set model.Set) error { return r.emit("set", set, fmt.Sprintf("%q  %s", set.Name, set.ID)) })
	}
	if len(r.env.From) == 0 {
		return fmt.Errorf("cli: remote discovery requires --from")
	}
	ids, err := r.remoteIDs(r.env.From)
	if err != nil {
		return err
	}
	r.service = &backup.Service{Config: r.config, Budget: r.budget, Registry: r.registry, Stores: map[string]cloud.Store{}, Kinds: map[string]string{}, Directory: r.root}
	if err = r.stores(ids); err != nil {
		return err
	}
	if err = r.catalogSecret(false); err != nil {
		return err
	}
	defer wipe(r.service.CatalogPassword)
	for _, id := range ids {
		if err = r.service.Discover(r.ctx, id, func(d backup.Descriptor) error {
			return r.emit("remote_set", d, fmt.Sprintf("%q  %s", d.Set.Name, d.Set.ID))
		}); err != nil {
			return err
		}
	}
	return nil
}
func catalog(r *runtime, args []string) error {
	if err := count(args, 2); err != nil {
		return err
	}
	operation, name := args[0], args[1]
	if err := r.openSet(name); err != nil {
		if operation != "pull" {
			return err
		}
		return fmt.Errorf("cli: discover this set with list --remote --from before pulling: %w", err)
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if operation == "abandon" {
		return r.service.Abandon(r.ctx)
	}
	if err = r.syncStores(); err != nil {
		return err
	}
	if err = r.catalogSecret(operation != "pull"); err != nil {
		return err
	}
	defer wipe(r.service.CatalogPassword)
	switch operation {
	case "pull":
		return r.service.Pull(r.ctx)
	case "push":
		return r.service.Push(r.ctx)
	case "reconcile":
		return r.service.Reconcile(r.ctx)
	}
	return fmt.Errorf("cli: unknown catalog operation")
}

func (r *runtime) locationSummary(locations []model.Location) string {
	out := ""
	for _, l := range locations {
		state := "readable"
		if l.Cold {
			state = "retrieval required"
			if strings.Contains(l.Restore, `ongoing-request="true"`) {
				state = "retrieval pending"
			}
		}
		name := l.Provider
		if remote, err := r.registry.Remote(r.ctx, l.Provider); err == nil {
			name = remote.Name
		}
		out += fmt.Sprintf("; %s: %s (%s)", name, l.ObservedClass, state)
	}
	return out
}
