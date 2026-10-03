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

	"glesha/archive"
	"glesha/cloud"
	"glesha/config"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/memory"
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
	Kinds           map[string]string
	CatalogPassword []byte
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
func (s *Service) path() string    { return filepath.Join(s.Directory, "catalog.db") }
func (s *Service) pending() string { return filepath.Join(s.Directory, "pending.db") }
func (s *Service) Pending() bool   { _, err := os.Stat(s.pending()); return err == nil }
func (s *Service) OpenSet(ctx context.Context, name string, root string) error {
	v, err := s.Registry.Set(ctx, name)
	if err != nil {
		return fmt.Errorf("backup: unknown set %q: %w", name, err)
	}
	s.Set = v
	s.Directory = filepath.Join(root, v.ID)
	return os.MkdirAll(s.Directory, 0700)
}
func NewSet(name string, roots []model.Root, c config.Config) (model.Set, error) {
	if !validName.MatchString(name) {
		return model.Set{}, fmt.Errorf("backup: invalid set name %q", name)
	}
	return model.Set{ID: uuid.NewString(), Name: name, Roots: roots, Compression: c.Archive.Compression, Level: c.Archive.Level, Prefix: c.Archive.Prefix, Class: c.Backup.FullClass, IncrementalClass: c.Backup.IncrementalClass, Created: now()}, nil
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
	Chunked                                  bool
	ChunkedSet                               bool
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
	chunked := s.Set.Chunked
	if o.ChunkedSet || o.Chunked {
		chunked = o.Chunked
	}
	spool := o.SpoolMax
	if spool == 0 {
		spool = s.Set.SpoolMax
	}
	if spool == 0 {
		spool = DefaultSpoolMax
	}
	if chunked && (o.KeepArchive || o.Output != "" || o.Mode == "memory") {
		return model.Snapshot{}, fmt.Errorf("backup: chunked mode cannot use --keep-archive, --output or memory archive mode")
	}
	if !chunked && o.SpoolMax > 0 {
		return model.Snapshot{}, fmt.Errorf("backup: --spool-max requires --chunked")
	}
	if chunked && spool < MinimumSpoolMax {
		return model.Snapshot{}, fmt.Errorf("backup: chunked spool requires at least 2MiB")
	}
	if o.Full && o.Incremental {
		return model.Snapshot{}, fmt.Errorf("backup: --full and --incremental are mutually exclusive")
	}
	to := o.To
	if len(to) == 0 {
		to = s.Set.To
	}
	if len(to) == 0 {
		return model.Snapshot{}, fmt.Errorf("backup: --to is required at creation or run time")
	}
	if s.Set.Authority != "" && (len(to) != 1 || to[0] != s.Set.Authority) {
		return model.Snapshot{}, fmt.Errorf("backup: incremental set is bound to one remote; use move to switch")
	}
	if o.Incremental && len(to) != 1 {
		return model.Snapshot{}, fmt.Errorf("backup: incrementals require exactly one authoritative remote")
	}
	if err := s.ValidateRoots(); err != nil {
		return model.Snapshot{}, err
	}
	for _, r := range to {
		if s.Stores[r] == nil {
			return model.Snapshot{}, fmt.Errorf("backup: selected remote is unavailable")
		}
	}
	if len(s.Set.To) == 0 {
		s.Set.To = slices.Clone(to)
		if err := s.Registry.Save(ctx, s.Set); err != nil {
			return model.Snapshot{}, err
		}
	}
	if s.Set.CatalogTo == "" {
		s.Set.CatalogTo = to[0]
		if err := s.Registry.Save(ctx, s.Set); err != nil {
			return model.Snapshot{}, err
		}
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
	v := model.Snapshot{Manifest: model.Manifest{Version: 1, Set: s.Set.ID, ID: uuid.NewString(), Full: !o.Incremental, Roots: s.Set.Roots, Compression: s.Set.Compression, InitialClass: s.Set.Class}, Created: now(), Status: model.STATUS_RUNNING, Destinations: strings.Join(to, ",")}
	if chunked {
		v.Layout = "chunked"
		v.Version = 2
	}
	if o.Compression != "" {
		v.Compression = o.Compression
	}
	level := s.Set.Level
	if o.Level > 0 {
		level = o.Level
	}
	mode := s.Config.Archive.Mode
	if o.Mode != "" {
		mode = o.Mode
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
			if s.Kinds[remote] != "aws" {
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
	if v.InitialClass != "STANDARD" {
		supported := false
		for _, id := range to {
			supported = supported || s.Kinds[id] == "aws"
		}
		if !supported {
			return v, fmt.Errorf("backup: storage class requires an AWS destination")
		}
	}
	if err = ValidateClass(v.InitialClass); err != nil {
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
	request := runRequest{Chunked: chunked, SpoolMax: spool, To: to, Incremental: o.Incremental, AutoCleanup: (o.Output == "" || o.autoCleanup) && !o.KeepArchive, Compression: v.Compression, Class: v.InitialClass, Mode: mode, Level: level, Prefix: o.Prefix}
	b, _ := json.Marshal(request)
	for k, value := range map[string]string{"operation": "run", "request": string(b), "pending_snapshot": v.ID, "archive_wrapper": archive.Stem(v.File)} {
		if err = p.SetMeta(ctx, k, value); err != nil {
			return v, err
		}
	}
	if err = p.PutSnapshot(ctx, v); err != nil {
		return v, err
	}
	exclusions := []string{v.File, filepath.Dir(s.Directory)}
	if err = archive.Inventory(ctx, p, v.ID, s.Set.Roots, exclusions, s.Budget.HashWorkers); err != nil {
		return v, err
	}
	changed := v.Full
	removed := 0
	sample := []string{}
	if err = p.EachEntry(ctx, v.ID, func(e model.Entry) error {
		yes, err := archive.Changed(ctx, p, v.ID, v.Parent, e)
		changed = changed || yes
		return err
	}); err != nil {
		return v, err
	}
	if v.Parent != "" {
		err = p.EachEntry(ctx, v.Parent, func(e model.Entry) error {
			current, err := p.Entry(ctx, v.ID, e.Path)
			missing := errors.Is(err, sql.ErrNoRows)
			if missing || err == nil && current.Type != e.Type {
				removed++
				if len(sample) < 10 {
					sample = append(sample, fmt.Sprintf("%q", e.Path))
				}
				changed = true
				if missing {
					v.DeletionsMember = archive.DeletionsName
				}
				return nil
			}
			return err
		})
		if err != nil {
			return v, err
		}
	}
	if removed > 0 {
		message := fmt.Sprintf("%d removed or type-changed paths:\n%s\nRecord changes?", removed, strings.Join(sample, "\n"))
		if o.Confirm == nil {
			return v, ErrConfirmationRequired
		}
		if err = o.Confirm(ctx, message); err != nil {
			return v, err
		}
	}
	if !changed {
		if err = archive.ValidateSources(ctx, p, v.ID); err != nil {
			return v, err
		}
		if err = archive.ValidateTree(ctx, p, v.ID, s.Set.Roots, exclusions); err != nil {
			return v, err
		}
		return model.Snapshot{}, nil
	}
	if err = p.PutSnapshot(ctx, v); err != nil {
		return v, err
	}
	if chunked {
		keep = true
		if err = s.createChunks(ctx, p, &v, request, o.Password); err != nil {
			return v, err
		}
	} else {
		v.Hash, v.Size, err = archive.Create(ctx, archive.Options{Output: v.File, Compression: v.Compression, Level: level, Mode: mode, Password: o.Password, Budget: s.Budget, Manifest: v.Manifest, Catalog: p, Exclusions: exclusions, OnReady: func(ctx context.Context, hash string, size int64, file string) error {
			v.Hash, v.Size, v.ReadyFile = hash, size, file
			err := p.PutSnapshot(ctx, v)
			if err == nil {
				keep = true
			}
			return err
		}})
		if err != nil {
			return v, err
		}
	}
	if !chunked {
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
