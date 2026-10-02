package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"glesha/archive"
	"glesha/cloud"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
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
	found := errors.New("archive already registered")
	err = c.EachSnapshot(ctx, func(existing model.Snapshot) error {
		if existing.Hash == hash && existing.Size == size {
			v = existing
			return found
		}
		return nil
	})
	if errors.Is(err, found) {
		err = nil
		if o.Remote != "" {
			o.Object.Snapshot = v.ID
			if err = c.PutLocation(ctx, o.Object); err != nil {
				return v, err
			}
			err = c.SetMeta(ctx, "dirty", "import")
		}
		return v, err
	}
	if err != nil {
		return v, err
	}
	temp, err := file_io.Temp(s.Directory)
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
	var manifest model.Manifest
	scan := "scan-" + v.ID
	v.Compression, err = archive.ExtractInto(ctx, archive.ExtractOptions{Input: o.File, Password: o.Password, StripWrapper: true, Catalog: staged, Snapshot: scan}, extraction, &manifest)
	if err != nil {
		return v, err
	}
	if manifest.ID != "" {
		if !manifest.Full {
			return v, fmt.Errorf("backup: only full archives may be imported")
		}
		v.PayloadID = manifest.ID
		v.InitialClass = manifest.InitialClass
	}
	roots := []model.Root{}
	dir, err := os.Open(extraction)
	if err != nil {
		return v, err
	}
	defer dir.Close()
	bytes := 0
	for {
		entries, err := dir.ReadDir(128)
		if err != nil && len(entries) == 0 {
			if errors.Is(err, io.EOF) {
				break
			}
			return v, err
		}
		for _, e := range entries {
			bytes += len(e.Name()) + 64
			if bytes > 4<<20 {
				return v, fmt.Errorf("backup: too many imported roots")
			}
			roots = append(roots, model.Root{Name: e.Name()})
		}
	}
	for i := range roots {
		for _, r := range s.Set.Roots {
			if r.Name == roots[i].Name {
				roots[i].Path = r.Path
			}
		}
		for _, r := range o.Roots {
			if r.Name == roots[i].Name {
				if roots[i].Path != "" && roots[i].Path != r.Path {
					return v, fmt.Errorf("backup: source mappings differ from registered set")
				}
				roots[i].Path = r.Path
			}
		}
	}
	for _, r := range o.Roots {
		seen := false
		for _, x := range roots {
			seen = seen || x.Name == r.Name
		}
		if !seen {
			return v, fmt.Errorf("backup: mapped root %q is absent", r.Name)
		}
	}
	if len(s.Set.Roots) > 0 {
		if len(roots) != len(s.Set.Roots) {
			return v, fmt.Errorf("backup: imported root selection differs")
		}
		for _, r := range roots {
			found := false
			for _, old := range s.Set.Roots {
				found = found || r.Name == old.Name
			}
			if !found {
				return v, fmt.Errorf("backup: imported root selection differs")
			}
		}
	}
	v.Roots = roots
	scanRoots := make([]model.Root, len(roots))
	for i, r := range roots {
		scanRoots[i] = model.Root{Name: r.Name, Path: filepath.Join(extraction, r.Name)}
	}
	if err = archive.Inventory(ctx, staged, v.ID, scanRoots, nil, s.Budget.HashWorkers); err != nil {
		return v, err
	}
	wrapper := ""
	if err = staged.EachEntry(ctx, v.ID, func(e model.Entry) error {
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
		e.Archive = v.ID
		e.Member = original.Member
		e.Source, e.Identity, e.SourceIdentity = "", "", ""
		return staged.PutEntry(ctx, v.ID, e)
	}); err != nil {
		return v, err
	}
	afterHash, afterSize, err := archive.Hash(ctx, o.File)
	if err != nil || hash != afterHash || size != afterSize {
		return v, fmt.Errorf("backup: archive changed during import")
	}
	if o.Remote != "" {
		o.Object.Snapshot = v.ID
		if err = staged.PutLocation(ctx, o.Object); err != nil {
			return v, err
		}
	}
	if err = staged.Commit(ctx, v, wrapper, o.Baseline); err != nil {
		return v, err
	}
	if err = staged.DeleteEntries(ctx, scan); err != nil {
		return v, err
	}
	if err = staged.SetMeta(ctx, "dirty", "import"); err != nil {
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
	s.Set.Roots = roots
	if len(s.Set.To) == 0 && o.Remote != "" {
		s.Set.To = []string{o.Remote}
		s.Set.CatalogTo = o.Remote
	}
	err = s.Registry.Save(ctx, s.Set)
	return v, err
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
			b, err := os.ReadFile(filepath.Join(s.Directory, "observation-"+v.ID+"-"+ls[i].Provider+".json"))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			var h cloud.Object
			if err = json.Unmarshal(b, &h); err != nil {
				return err
			}
			info, err := os.Stat(filepath.Join(s.Directory, "observation-"+v.ID+"-"+ls[i].Provider+".json"))
			if err != nil {
				return err
			}
			if h.Key != ls[i].Key || h.Version != "" && h.Version != ls[i].Version || ls[i].ObservedAt != nil && info.ModTime().Before(*ls[i].ObservedAt) {
				continue
			}
			observed := info.ModTime().UTC()
			ls[i].ObservedAt = &observed
			ls[i].ObservedClass = h.Class
			ls[i].CurrentClass = h.Class
			ls[i].Cold = h.Cold
			ls[i].Restore = h.Restore
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
	return c.SetMeta(ctx, "dirty", "baseline")
}
