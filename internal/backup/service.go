package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"glesha/internal/archive"
	"glesha/internal/cloud"
	"glesha/internal/config"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
	"glesha/internal/memory"
)

var ErrConfirmationRequired = errors.New("backup: explicit confirmation required")
var ErrPending = errors.New("backup: operation pending")
var ErrConflict = errors.New("backup: catalog conflict")
var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)

type Service struct {
	Config          config.Config
	Budget          memory.Budget
	Registry        repository.Registryor
	Set             model.Set
	Directory       string
	Stores          map[string]cloud.Store
	CatalogPassword []byte
	DiscoveryPrefix string
	localAuthority  string
}

func (s *Service) Catalog(ctx context.Context) (repository.Cataloger, error) {
	c, err := repository.NewCatalogRepository(ctx, s.path())
	if err != nil {
		return nil, err
	}
	id, err := c.GetMeta(ctx, "set_id")
	if err != nil {
		c.Close()
		return nil, err
	}
	if id != "" && id != s.Set.ID {
		c.Close()
		return nil, fmt.Errorf("backup: catalog belongs to another set")
	}
	if id == "" {
		if err = c.SetMeta(ctx, "set_id", s.Set.ID); err != nil {
			c.Close()
			return nil, err
		}
	}
	return c, nil
}

// scratch returns a copy of s that keeps its catalog files in a new directory
// under s.Directory, deleted by remove. The copy shares Stores, Registry and
// the maps and slices in Config and Set with s.
func (s *Service) scratch(purpose string) (peer *Service, remove func(), err error) {
	directory, err := os.MkdirTemp(s.Directory, ".glesha-"+purpose+"-")
	if err != nil {
		return nil, nil, err
	}
	copied := *s
	copied.Directory = directory
	return &copied, func() { os.RemoveAll(directory) }, nil
}

// provider describes the store for remote; an unopened remote is the zero Provider.
func (s *Service) provider(remote string) cloud.Provider {
	if store := s.Stores[remote]; store != nil {
		return store.Provider()
	}
	return cloud.Provider{}
}

// Capabilities reports what the store for remote supports.
func (s *Service) Capabilities(remote string) cloud.Capabilities {
	return s.provider(remote).Capabilities
}
func (s *Service) path() string    { return filepath.Join(s.Directory, "catalog.db") }
func (s *Service) pending() string { return filepath.Join(s.Directory, "pending.db") }
func (s *Service) Pending() bool   { _, err := os.Stat(s.pending()); return err == nil }
func (s *Service) OpenSet(ctx context.Context, name string, root string) error {
	v, err := s.Registry.Set(ctx, name)
	if err != nil {
		return fmt.Errorf("backup: unknown set %q: %w", name, err)
	}
	s.Set = v
	s.localAuthority = v.CatalogTo
	s.Directory = filepath.Join(root, v.ID)
	if err = os.MkdirAll(s.Directory, 0700); err != nil {
		return err
	}
	if err = s.useLocator(ctx); errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}
func NewSet(name string, roots []model.Root, c config.Config) (model.Set, error) {
	if !validName.MatchString(name) {
		return model.Set{}, fmt.Errorf("backup: invalid set name %q", name)
	}
	return model.Set{
		ID:               uuid.NewString(),
		Name:             name,
		Roots:            roots,
		Compression:      c.Archive.Compression,
		Level:            c.Archive.Level,
		Prefix:           c.Archive.Prefix,
		Class:            c.Backup.FullClass,
		IncrementalClass: c.Backup.IncrementalClass,
		Created:          now(),
	}, nil
}
func Resolve(ctx context.Context, c repository.Cataloger, id string) (model.Snapshot, error) {
	if id == "" || id == "latest" {
		return c.Latest(ctx)
	}
	return c.Snapshot(ctx, id)
}
func sameManifest(a, b model.Manifest) bool {
	a.Roots = slices.Clone(a.Roots)
	b.Roots = slices.Clone(b.Roots)
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}
func (s *Service) ValidateRoots() error {
	if len(s.Set.Roots) == 0 {
		return fmt.Errorf("backup: imported roots are not mapped; use configure --root NAME=PATH")
	}
	for i, r := range s.Set.Roots {
		if r.Path == "" {
			return fmt.Errorf("backup: root %q is not mapped", r.Name)
		}
		if name, err := archive.SafeName(r.Name); err != nil || name != r.Name || strings.Contains(name, "/") {
			return fmt.Errorf("backup: invalid registered root name")
		}
		roots, err := archive.Roots([]string{r.Path})
		if err != nil {
			return err
		}
		if roots[0].Path != r.Path {
			return fmt.Errorf("backup: registered source mapping changed; use a different set")
		}
		for _, old := range s.Set.Roots[:i] {
			if old.Name == r.Name || archive.Within(r.Path, old.Path) || archive.Within(old.Path, r.Path) {
				return fmt.Errorf("backup: source roots collide or overlap")
			}
		}
	}

	return nil
}

type RunOptions struct {
	To                                       []string
	Incremental, Full                        bool
	KeepArchive                              bool
	Chunked                                  *bool // nil keeps the set's setting
	SpoolMax                                 int64
	autoCleanup                              bool
	Output, Compression, Class, Mode, Prefix string
	Level                                    int
	Password                                 []byte
	Confirm                                  func(context.Context, string) error
}
type runRequest struct {
	To                               []string `json:"to"`
	Incremental                      bool     `json:"incremental"`
	Chunked                          bool     `json:"chunked,omitempty"`
	SpoolMax                         int64    `json:"spool_max,omitempty"`
	AutoCleanup                      bool     `json:"auto_cleanup,omitempty"`
	Compression, Class, Mode, Prefix string
	Level                            int
}

func (s *Service) Run(ctx context.Context, o RunOptions) (model.Snapshot, error) {
	if s.Pending() {
		return s.resumeRun(ctx, o)
	}
	plan, err := s.validateRun(o)
	if err != nil {
		return model.Snapshot{}, err
	}
	to := plan.to
	if err := s.saveTargets(ctx, to); err != nil {
		return model.Snapshot{}, err
	}
	if err := s.Pull(ctx); err != nil {
		return model.Snapshot{}, err
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	defer c.Close()
	if err = s.checkCatalogSecret(ctx, c); err != nil {
		return model.Snapshot{}, err
	}
	v, err := s.newSnapshot(ctx, c, o, plan)
	if err != nil {
		return v, err
	}
	if err = c.Vacuum(ctx, s.pending()); err != nil {
		return v, err
	}
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return v, err
	}
	defer p.Close()
	keep := false
	defer func() {
		if !keep {
			p.Close()
			os.Remove(s.pending())
		}
	}()
	request := runRequest{
		Chunked:     plan.chunked,
		SpoolMax:    plan.spool,
		To:          to,
		Incremental: o.Incremental,
		AutoCleanup: (o.Output == "" || o.autoCleanup) && !o.KeepArchive,
		Compression: v.Compression,
		Class:       v.InitialClass,
		Mode:        plan.mode,
		Level:       plan.level,
		Prefix:      o.Prefix,
	}
	b, _ := json.Marshal(request)
	exclusions := []string{v.File, filepath.Dir(s.Directory)}
	changes, err := s.recordInventory(ctx, p, &v, string(b), exclusions)
	if err != nil {
		return v, err
	}
	if err = confirmChanges(ctx, o, changes); err != nil {
		return v, err
	}
	if !changes.changed {
		if err = s.validateUnchanged(ctx, p, v.ID, exclusions); err != nil {
			return v, err
		}
		return model.Snapshot{}, nil
	}
	if err = p.PutSnapshot(ctx, v); err != nil {
		return v, err
	}
	if plan.chunked {
		keep = true
		if err = s.createChunks(ctx, p, &v, request, o.Password); err != nil {
			return v, err
		}
	} else {
		// the pending catalog is kept once it records a finished archive
		v.Hash, v.Size, err = archive.Create(ctx, archive.Options{
			Output:      v.File,
			Compression: v.Compression,
			Level:       plan.level,
			Mode:        plan.mode,
			Password:    o.Password,
			Budget:      s.Budget,
			Manifest:    v.Manifest,
			Catalog:     p,
			Exclusions:  exclusions,
			OnReady: func(ctx context.Context, hash string, size int64, file string) error {
				v.Hash, v.Size, v.ReadyFile = hash, size, file
				if err := p.PutSnapshot(ctx, v); err != nil {
					return err
				}
				keep = true
				return nil
			},
		})
		if err != nil {
			return v, err
		}
		v.ReadyFile = ""
	}
	if err = p.PutSnapshot(ctx, v); err != nil {
		return v, err
	}
	keep = true
	if o.Incremental {
		s.Set.Authority = to[0]
		s.Set.To = []string{to[0]}
		if err = s.Registry.Save(ctx, s.Set); err != nil {
			return v, err
		}
	}
	return v, s.finishRun(ctx, p, &v)
}

func (s *Service) saveTargets(ctx context.Context, to []string) error {
	if len(s.Set.To) == 0 {
		s.Set.To = slices.Clone(to)
		if err := s.Registry.Save(ctx, s.Set); err != nil {
			return err
		}
	}
	if s.Set.CatalogTo == "" {
		s.Set.CatalogTo = to[0]
		if err := s.Registry.Save(ctx, s.Set); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) recordInventory(ctx context.Context, p repository.Cataloger, v *model.Snapshot, request string, exclusions []string) (runChanges, error) {
	if err := markRunPending(ctx, p, *v, request); err != nil {
		return runChanges{}, err
	}
	if err := p.PutSnapshot(ctx, *v); err != nil {
		return runChanges{}, err
	}
	if err := archive.Inventory(ctx, p, v.ID, s.Set.Roots, exclusions, s.Budget.HashWorkers); err != nil {
		return runChanges{}, err
	}
	return detectChanges(ctx, p, v)
}

func markRunPending(ctx context.Context, p repository.Cataloger, v model.Snapshot, request string) error {
	for k, value := range map[string]string{"operation": "run", "request": request, "pending_snapshot": v.ID, "archive_wrapper": archive.Stem(v.File)} {
		if err := p.SetMeta(ctx, k, value); err != nil {
			return err
		}
	}
	return nil
}

func confirmChanges(ctx context.Context, o RunOptions, changes runChanges) error {
	if changes.removed > 0 {
		message := fmt.Sprintf("%d removed or type-changed paths:\n%s\nRecord changes?", changes.removed, strings.Join(changes.sample, "\n"))
		if o.Confirm == nil {
			return ErrConfirmationRequired
		}
		if err := o.Confirm(ctx, message); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateUnchanged(ctx context.Context, p repository.Cataloger, id string, exclusions []string) error {
	if err := archive.ValidateSources(ctx, p, id); err != nil {
		return err
	}
	return archive.ValidateTree(ctx, p, id, s.Set.Roots, exclusions)
}

// runPlan is a validated run request, resolved against the set's saved settings.
type runPlan struct {
	to      []string
	chunked bool
	spool   int64
	level   int
	mode    string
}

// validateRun checks o against the set and resolves its defaults without side effects.
func (s *Service) validateRun(o RunOptions) (runPlan, error) {
	chunked := s.Set.Chunked
	if o.Chunked != nil {
		chunked = *o.Chunked
	}
	spool := o.SpoolMax
	if spool == 0 {
		spool = s.Set.SpoolMax
	}
	if spool == 0 {
		spool = DefaultSpoolMax
	}
	if chunked && (o.KeepArchive || o.Output != "" || o.Mode == "memory") {
		return runPlan{}, fmt.Errorf("backup: chunked mode cannot use --keep-archive, --output or memory archive mode")
	}
	if !chunked && o.SpoolMax > 0 {
		return runPlan{}, fmt.Errorf("backup: --spool-max requires --chunked")
	}
	if chunked && spool < MinimumSpoolMax {
		return runPlan{}, fmt.Errorf("backup: chunked spool requires at least 2MiB")
	}
	if o.Full && o.Incremental {
		return runPlan{}, fmt.Errorf("backup: --full and --incremental are mutually exclusive")
	}
	to := o.To
	if len(to) == 0 {
		to = s.Set.To
	}
	if len(to) == 0 {
		return runPlan{}, fmt.Errorf("backup: --to is required at creation or run time")
	}
	if s.Set.Authority != "" && (len(to) != 1 || to[0] != s.Set.Authority) {
		return runPlan{}, fmt.Errorf("backup: incremental set is bound to one remote; use move to switch")
	}
	if o.Incremental && len(to) != 1 {
		return runPlan{}, fmt.Errorf("backup: incrementals require exactly one authoritative remote")
	}
	if err := s.ValidateRoots(); err != nil {
		return runPlan{}, err
	}
	for _, r := range to {
		if s.Stores[r] == nil {
			return runPlan{}, fmt.Errorf("backup: selected remote is unavailable")
		}
	}
	plan := runPlan{to: to, chunked: chunked, spool: spool, level: s.Set.Level, mode: s.Config.Archive.Mode}
	if o.Level > 0 {
		plan.level = o.Level
	}
	if o.Mode != "" {
		plan.mode = o.Mode
	}
	return plan, nil
}

// newSnapshot describes the snapshot a run creates: baseline, storage class and archive path.
func (s *Service) newSnapshot(ctx context.Context, c repository.Cataloger, o RunOptions, plan runPlan) (model.Snapshot, error) {
	to := plan.to
	var err error
	v := model.Snapshot{
		Manifest: model.Manifest{
			Version:      1,
			Set:          s.Set.ID,
			ID:           uuid.NewString(),
			Full:         !o.Incremental,
			Roots:        s.Set.Roots,
			Compression:  s.Set.Compression,
			InitialClass: s.Set.Class,
		},
		Created:      now(),
		Status:       model.STATUS_RUNNING,
		Destinations: strings.Join(to, ","),
	}
	if plan.chunked {
		v.Layout = "chunked"
		v.Version = 2
	}
	if o.Compression != "" {
		v.Compression = o.Compression
	}
	if o.Incremental {
		parent, err := c.Latest(ctx)
		if err != nil {
			return v, fmt.Errorf("backup: incremental requires a committed baseline: %w", err)
		}
		if err = s.coverage(ctx, c, parent, to[0]); err != nil {
			return v, err
		}
		v.Parent = parent.ID
		if s.Set.IncrementalClass != "" {
			v.InitialClass = s.Set.IncrementalClass
		}
	}
	if o.Class != "" {
		v.InitialClass = o.Class
	}
	if v.InitialClass == "" {
		for _, remote := range to {
			if !s.Capabilities(remote).StorageClasses {
				continue
			}
			if s.Registry != nil {
				ref, err := s.Registry.Remote(ctx, remote)
				if err != nil {
					return v, err
				}
				v.InitialClass = s.Config.RemotesByName()[ref.Name].StorageClass
			}
			if v.InitialClass != "" {
				break
			}
		}
		if v.InitialClass == "" {
			v.InitialClass = "STANDARD"
		}
	}
	if err = s.checkClass(to, v.InitialClass); err != nil {
		return v, err
	}
	v.File = o.Output
	if v.File == "" {
		prefix := s.Set.Prefix
		if o.Prefix != "" {
			prefix = o.Prefix
		}
		v.File = prefix + "-" + s.Set.Name + "-" + now().Format("20060102T150405.000000000Z") + archive.Extension(v.Compression) + ".gpg"
	}
	v.File, err = filepath.Abs(v.File)
	if err != nil {
		return v, err
	}
	if _, err = os.Lstat(v.File); !os.IsNotExist(err) {
		return v, fmt.Errorf("backup: archive output exists")
	}
	return v, nil
}

// runChanges is how a new snapshot differs from its baseline.
type runChanges struct {
	changed bool
	// removed counts paths deleted or changed in type; sample names up to ten of them
	removed int
	sample  []string
}

// detectChanges compares the inventory of v with its parent and records deletions on v.
func detectChanges(ctx context.Context, p repository.Cataloger, v *model.Snapshot) (runChanges, error) {
	changes := runChanges{changed: v.Full, sample: []string{}}
	if err := p.EachEntry(ctx, v.ID, func(e model.Entry) error {
		yes, err := archive.Changed(ctx, p, v.ID, v.Parent, e)
		changes.changed = changes.changed || yes
		return err
	}); err != nil {
		return changes, err
	}
	if v.Parent == "" {
		return changes, nil
	}
	err := p.EachEntry(ctx, v.Parent, func(e model.Entry) error {
		current, err := p.Entry(ctx, v.ID, e.Path)
		missing := errors.Is(err, sql.ErrNoRows)
		if missing || err == nil && current.Type != e.Type {
			changes.removed++
			if len(changes.sample) < 10 {
				changes.sample = append(changes.sample, fmt.Sprintf("%q", e.Path))
			}
			changes.changed = true
			if missing {
				v.DeletionsMember = archive.DeletionsName
			}
			return nil
		}
		return err
	})
	return changes, err
}
func (s *Service) coverage(ctx context.Context, c repository.Cataloger, v model.Snapshot, remote string) error {
	return c.EachSnapshot(ctx, func(snapshot model.Snapshot) error {
		if snapshot.Status != model.STATUS_COMPLETED {
			return fmt.Errorf("backup: uncommitted snapshot cannot establish authority")
		}
		locations, err := c.Locations(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		for _, l := range locations {
			if l.Provider == remote && l.Status == model.STATUS_COMPLETED {
				return nil
			}
		}
		return fmt.Errorf("backup: selected remote lacks historical archive %s; move the complete set first", snapshot.ID)
	})
}

// checkClass rejects an invalid class, and a class other than STANDARD unless
// a destination in to supports storage classes.
func (s *Service) checkClass(to []string, class string) error {
	if err := ValidateClass(class); err != nil || class == "STANDARD" {
		return err
	}
	for _, id := range to {
		if s.Capabilities(id).StorageClasses {
			return nil
		}
	}
	return fmt.Errorf("backup: storage class requires a destination with storage classes")
}

func ValidateClass(class string) error {
	switch class {
	case "STANDARD", "STANDARD_IA", "ONEZONE_IA", "INTELLIGENT_TIERING", "GLACIER_IR", "GLACIER", "DEEP_ARCHIVE", "REDUCED_REDUNDANCY":
		return nil
	}
	return fmt.Errorf("backup: unsupported AWS storage class %q", class)
}
func ParseDate(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("backup: date must use RFC3339")
	}
	t = t.UTC()
	return &t, nil
}
