package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"glesha/internal/archive"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
	"glesha/internal/fsutil"
)

type ImportOptions struct {
	File, Remote string
	Object       model.Location
	Password     []byte
	Roots        []model.Root
	Date         *time.Time
	Baseline     bool
}

func (s *Service) Import(ctx context.Context, o ImportOptions) (model.Snapshot, error) {
	var v model.Snapshot
	if s.Pending() {
		return v, fmt.Errorf("%w: finish or abandon pending work before import", ErrPending)
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return v, err
	}
	defer c.Close()
	hash, size, err := archive.Hash(ctx, o.File)
	if err != nil {
		return v, err
	}
	if existing, ok, err := findRegistered(ctx, c, hash, size); err != nil || ok {
		if err != nil || o.Remote == "" {
			return existing, err
		}
		o.Object.Snapshot = existing.ID
		if err = c.PutLocation(ctx, o.Object); err != nil {
			return existing, err
		}
		return existing, c.SetMeta(ctx, "dirty", "import")
	}
	temp, err := fsutil.Temp(s.Directory)
	if err != nil {
		return v, err
	}
	temp.Close()
	os.Remove(temp.Name())
	defer os.Remove(temp.Name())
	if err = c.Vacuum(ctx, temp.Name()); err != nil {
		return v, err
	}
	staged, err := repository.NewCatalogRepository(ctx, temp.Name())
	if err != nil {
		return v, err
	}
	defer staged.Close()
	extraction, err := os.MkdirTemp(s.Directory, ".glesha-import-*")
	if err != nil {
		return v, err
	}
	defer os.RemoveAll(extraction)
	v = model.Snapshot{Manifest: model.Manifest{Version: 1, ID: uuid.NewString(), Set: s.Set.ID, Full: true}, Imported: true, BackupDate: o.Date, Created: now(), File: o.File, Hash: hash, Size: size, Status: model.STATUS_COMPLETED}
	scan := "scan-" + v.ID
	v, wrapper, err := s.extractImport(ctx, staged, extraction, o, v, scan)
	if err != nil {
		return v, err
	}
	afterHash, afterSize, err := archive.Hash(ctx, o.File)
	if err != nil || hash != afterHash || size != afterSize {
		return v, fmt.Errorf("backup: archive changed during import")
	}
	if err = commitImport(ctx, staged, o, v, wrapper, scan); err != nil {
		return v, err
	}
	compact := temp.Name() + ".compact"
	defer os.Remove(compact)
	if err = staged.Vacuum(ctx, compact); err != nil {
		return v, err
	}
	if err = staged.Close(); err != nil {
		return v, err
	}
	if err = c.Close(); err != nil {
		return v, err
	}
	if err = os.Rename(compact, s.path()); err != nil {
		return v, err
	}
	if s.Set.CatalogTo == "" && o.Remote == "" {
		s.Set.CatalogTo = "local"
	}
	s.Set.Roots = v.Roots
	if len(s.Set.To) == 0 && o.Remote != "" {
		s.Set.To = []string{o.Remote}
		s.Set.CatalogTo = o.Remote
	}
	err = s.Registry.Save(ctx, s.Set)
	return v, err
}

func (s *Service) extractImport(ctx context.Context, staged repository.Cataloger, extraction string, o ImportOptions, v model.Snapshot, scan string) (model.Snapshot, string, error) {
	var err error
	var manifest model.Manifest
	v.Compression, err = archive.ExtractInto(ctx, archive.ExtractOptions{Input: o.File, Password: o.Password, StripWrapper: true, Catalog: staged, Snapshot: scan}, extraction, &manifest)
	if err != nil {
		return v, "", err
	}
	if manifest.Layout == "chunked" {
		return v, "", fmt.Errorf("backup: individual chunked objects cannot be imported; pull the set catalog")
	}
	if manifest.ID != "" {
		if !manifest.Full {
			return v, "", fmt.Errorf("backup: only full archives may be imported")
		}
		v.PayloadID = manifest.ID
		v.InitialClass = manifest.InitialClass
	}
	roots, err := readImportedRoots(extraction)
	if err != nil {
		return v, "", err
	}
	if err = mapImportedRoots(roots, s.Set.Roots, o.Roots); err != nil {
		return v, "", err
	}
	v.Roots = roots
	scanRoots := make([]model.Root, len(roots))
	for i, r := range roots {
		scanRoots[i] = model.Root{Name: r.Name, Path: filepath.Join(extraction, r.Name)}
	}
	if err = archive.Inventory(ctx, staged, v.ID, scanRoots, nil, s.Budget.HashWorkers); err != nil {
		return v, "", err
	}
	wrapper, err := catalogImportedEntries(ctx, staged, v.ID, scan)
	if err != nil {
		return v, "", err
	}
	return v, wrapper, nil
}

func commitImport(ctx context.Context, staged repository.Cataloger, o ImportOptions, v model.Snapshot, wrapper, scan string) error {
	if o.Remote != "" {
		o.Object.Snapshot = v.ID
		if err := staged.PutLocation(ctx, o.Object); err != nil {
			return err
		}
	}
	if o.Baseline {
		if err := staged.SetMeta(ctx, "baseline_changed", "true"); err != nil {
			return err
		}
	}
	if err := staged.Commit(ctx, v, wrapper, o.Baseline); err != nil {
		return err
	}
	if err := staged.DeleteEntries(ctx, scan); err != nil {
		return err
	}
	return staged.SetMeta(ctx, "dirty", "import")
}

// findRegistered returns the snapshot already holding the archive with hash and size.
func findRegistered(ctx context.Context, c repository.Cataloger, hash string, size int64) (model.Snapshot, bool, error) {
	var match model.Snapshot
	found := errors.New("archive already registered")
	err := c.EachSnapshot(ctx, func(existing model.Snapshot) error {
		if existing.Hash == hash && existing.Size == size {
			match = existing
			return found
		}
		return nil
	})
	if errors.Is(err, found) {
		return match, true, nil
	}
	return match, false, err
}

// readImportedRoots lists the top-level directories of an extracted archive.
func readImportedRoots(extraction string) ([]model.Root, error) {
	roots := []model.Root{}
	dir, err := os.Open(extraction)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	bytes := 0
	for {
		entries, err := dir.ReadDir(128)
		if err != nil && len(entries) == 0 {
			if errors.Is(err, io.EOF) {
				return roots, nil
			}
			return nil, err
		}
		for _, e := range entries {
			bytes += len(e.Name()) + 64
			if bytes > 4<<20 {
				return nil, fmt.Errorf("backup: too many imported roots")
			}
			roots = append(roots, model.Root{Name: e.Name()})
		}
	}
}

// mapImportedRoots binds each imported root to its registered or mapped source
// path, and checks the roots match the set's.
func mapImportedRoots(roots, registered, mappings []model.Root) error {
	for i := range roots {
		for _, r := range registered {
			if r.Name == roots[i].Name {
				roots[i].Path = r.Path
			}
		}
		for _, r := range mappings {
			if r.Name == roots[i].Name {
				if roots[i].Path != "" && roots[i].Path != r.Path {
					return fmt.Errorf("backup: source mappings differ from registered set")
				}
				roots[i].Path = r.Path
			}
		}
	}
	for _, r := range mappings {
		if !slices.ContainsFunc(roots, func(x model.Root) bool { return x.Name == r.Name }) {
			return fmt.Errorf("backup: mapped root %q is absent", r.Name)
		}
	}
	if len(registered) == 0 {
		return nil
	}
	if len(roots) != len(registered) {
		return fmt.Errorf("backup: imported root selection differs")
	}
	for _, r := range roots {
		if !slices.ContainsFunc(registered, func(old model.Root) bool { return r.Name == old.Name }) {
			return fmt.Errorf("backup: imported root selection differs")
		}
	}
	return nil
}

// catalogImportedEntries records the inventoried entries of snapshot id with the
// tar metadata of the scan, and returns the archive's common wrapper directory.
func catalogImportedEntries(ctx context.Context, staged repository.Cataloger, id, scan string) (string, error) {
	wrapper := ""
	err := staged.EachEntry(ctx, id, func(e model.Entry) error {
		original, err := staged.Entry(ctx, scan, e.Path)
		if err != nil {
			return err
		}
		prefix := strings.TrimSuffix(strings.TrimSuffix(original.Member, "/"), e.Path)
		prefix = strings.TrimSuffix(prefix, "/")
		if wrapper == "" {
			wrapper = prefix
		}
		if prefix != wrapper {
			return fmt.Errorf("backup: archive has inconsistent wrappers")
		}
		e.UID, e.GID, e.PAX = original.UID, original.GID, original.PAX
		e.Archive = id
		e.Member = original.Member
		e.Source, e.Identity, e.SourceIdentity = "", "", ""
		return staged.PutEntry(ctx, id, e)
	})
	return wrapper, err
}
func (s *Service) BindRoots(ctx context.Context, mappings []model.Root) error {
	roots := append([]model.Root(nil), s.Set.Roots...)
	for _, mapping := range mappings {
		found := false
		for i := range roots {
			if roots[i].Name == mapping.Name {
				found = true
				if roots[i].Path != "" && roots[i].Path != mapping.Path {
					return fmt.Errorf("backup: bound sources cannot be replaced")
				}
				roots[i].Path = mapping.Path
			}
		}
		if !found {
			return fmt.Errorf("backup: unknown root %q", mapping.Name)
		}
	}
	s.Set.Roots = roots
	return s.Registry.Save(ctx, s.Set)
}
func (s *Service) History(ctx context.Context, fn func(model.Snapshot, []model.Location) error) error {
	c, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.EachSnapshotReverse(ctx, func(v model.Snapshot) error {
		if v.Status != model.STATUS_COMPLETED {
			return nil
		}
		ls, err := c.Locations(ctx, v.ID)
		if err != nil {
			return err
		}
		for i := range ls {
			if err := s.observeLocation(ctx, v.ID, &ls[i]); err != nil {
				return err
			}
		}
		for i := range v.Chunks {
			for j := range v.Chunks[i].Locations {
				if err := s.observeLocation(ctx, v.ID, &v.Chunks[i].Locations[j]); err != nil {
					return err
				}
			}
		}
		return fn(v, ls)
	})
}
func (s *Service) Browse(ctx context.Context, id, filter string, recursive bool, fn func(model.Entry) error) error {
	c, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	v, err := Resolve(ctx, c, id)
	if err != nil {
		return err
	}
	if filter != "" {
		if _, err = archive.SafeName(filter); err != nil {
			return err
		}
		if _, err = c.Entry(ctx, v.ID, filter); err != nil {
			return err
		}
	}
	for _, directories := range []bool{true, false} {
		if err = c.EachEntry(ctx, v.ID, func(e model.Entry) error {
			if (e.Type == "directory") != directories {
				return nil
			}
			if recursive {
				if filter != "" && e.Path != filter && !strings.HasPrefix(e.Path, filter+"/") {
					return nil
				}
			} else if e.Parent != filter {
				return nil
			}
			return fn(e)
		}); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) SetBaseline(ctx context.Context, id string) error {
	c, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	v, err := Resolve(ctx, c, id)
	if err != nil {
		return err
	}
	if v.Status != model.STATUS_COMPLETED {
		return fmt.Errorf("backup: baseline must be completed")
	}
	if err = c.SetMeta(ctx, "head", v.ID); err != nil {
		return err
	}
	if err = c.SetMeta(ctx, "baseline_changed", "true"); err != nil {
		return err
	}
	return c.SetMeta(ctx, "dirty", "baseline")
}
