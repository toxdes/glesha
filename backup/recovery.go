package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"glesha/archive"
	"glesha/database/model"
	"glesha/database/repository"
)

func (s *Service) RequiredRemotes(ctx context.Context) ([]string, error) {
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return nil, err
	}
	defer p.Close()
	kind, err := p.GetMeta(ctx, "operation")
	if err != nil {
		return nil, err
	}
	out := []string{}
	if kind == "run" {
		id, err := p.GetMeta(ctx, "pending_snapshot")
		if err != nil {
			return nil, err
		}
		v, err := p.Snapshot(ctx, id)
		if err != nil {
			return nil, err
		}
		locations, err := p.Locations(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, remote := range strings.Split(v.Destinations, ",") {
			done := false
			for _, l := range locations {
				done = done || l.Provider == remote && l.Status == model.STATUS_COMPLETED
			}
			if !done {
				out = append(out, remote)
			}
		}
	} else if kind == "move" {
		value, _ := p.GetMeta(ctx, "move_to")
		out = append(out, s.Set.To...)
		out = append(out, value)
	} else if kind == "storage-class" {
		out, err = s.ClassRemotes(ctx, "")
		if err != nil {
			return nil, err
		}
	}
	if s.Set.CatalogTo != "" && s.Set.CatalogTo != "local" {
		out = append(out, s.Set.CatalogTo)
	}
	return out, nil
}
func (s *Service) Relocate(ctx context.Context, file string) error {
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return err
	}
	defer p.Close()
	id, err := p.GetMeta(ctx, "pending_snapshot")
	if err != nil {
		return err
	}
	v, err := p.Snapshot(ctx, id)
	if err != nil {
		return err
	}
	file, err = filepath.Abs(file)
	if err != nil {
		return err
	}
	hash, size, err := archive.Hash(ctx, file)
	if err != nil || hash != v.Hash || size != v.Size {
		return fmt.Errorf("backup: relocated archive checksum mismatch")
	}
	v.File = file
	v.ReadyFile = ""
	if err = retainRunArchive(ctx, p); err != nil {
		return err
	}
	return p.PutSnapshot(ctx, v)
}

func (s *Service) RetainPendingArchive(ctx context.Context) error {
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return err
	}
	defer p.Close()
	return retainRunArchive(ctx, p)
}

func retainRunArchive(ctx context.Context, c repository.Cataloger) error {
	kind, err := c.GetMeta(ctx, "operation")
	if err != nil {
		return err
	}
	if kind != "run" {
		return fmt.Errorf("backup: --keep-archive requires a pending run")
	}
	b, err := c.GetMeta(ctx, "request")
	if err != nil {
		return err
	}
	var request runRequest
	if err = json.Unmarshal([]byte(b), &request); err != nil {
		return err
	}
	request.AutoCleanup = false
	binary, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return c.SetMeta(ctx, "request", string(binary))
}
func (s *Service) Retry(ctx context.Context) error {
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return err
	}
	kind, err := p.GetMeta(ctx, "operation")
	p.Close()
	if err != nil {
		return err
	}
	switch kind {
	case "run":
		_, err = s.resumeRun(ctx, RunOptions{})
		return err
	case "push":
		return s.Push(ctx)
	case "storage-class":
		return s.StorageClass(ctx, "", "")
	case "move":
		return s.Move(ctx, "", RetrievalOptions{})
	}
	return fmt.Errorf("backup: unknown pending operation")
}
func (s *Service) Reconcile(ctx context.Context) error {
	if !s.Pending() {
		return fmt.Errorf("backup: no pending work")
	}
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return err
	}
	defer p.Close()
	kind, err := p.GetMeta(ctx, "operation")
	if err != nil {
		return err
	}
	if kind != "run" && kind != "push" {
		return fmt.Errorf("backup: this operation requires explicit retry, not branch reconciliation")
	}
	// preserve the entire branch before replaying its snapshots into a fresh remote view
	saved := filepath.Join(s.Directory, "reconciliation-"+newID()+".db")
	if err = repository.Preserve(ctx, p, saved); err != nil {
		return err
	}
	stagedName := filepath.Join(s.Directory, "reconciled-"+newID()+".db")
	defer os.Remove(stagedName)
	peer := *s
	peer.Directory = filepath.Join(s.Directory, ".glesha-reconcile-"+newID())
	if err = os.Mkdir(peer.Directory, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(peer.Directory)
	if err = peer.Pull(ctx); err != nil {
		return err
	}
	remote, err := peer.Catalog(ctx)
	if err != nil {
		return err
	}
	defer remote.Close()
	head, _ := remote.GetMeta(ctx, "head")
	err = p.EachSnapshot(ctx, func(v model.Snapshot) error {
		if v.Status != model.STATUS_COMPLETED {
			return fmt.Errorf("backup: unfinished uploads cannot be reconciled")
		}
		existing, err := remote.Snapshot(ctx, v.ID)
		if err == nil {
			if existing.Hash != v.Hash || !sameManifest(existing.Manifest, v.Manifest) {
				return fmt.Errorf("%w: snapshot identity differs", ErrConflict)
			}
			return nil
		}
		if v.Parent != "" {
			if _, err = remote.Snapshot(ctx, v.Parent); err != nil {
				return fmt.Errorf("backup: missing remote ancestry")
			}
		}
		if err = p.EachEntry(ctx, v.ID, func(e model.Entry) error { return remote.PutEntry(ctx, v.ID, e) }); err != nil {
			return err
		}
		locations, err := p.Locations(ctx, v.ID)
		if err != nil {
			return err
		}
		for _, l := range locations {
			if err = remote.PutLocation(ctx, l); err != nil {
				return err
			}
		}
		return remote.Commit(ctx, v, archive.Stem(v.File), false)
	})
	if err != nil {
		return err
	}
	if head != "" {
		if err = remote.SetMeta(ctx, "head", head); err != nil {
			return err
		}
	}
	if err = remote.SetMeta(ctx, "operation", "push"); err != nil {
		return err
	}
	if err = remote.Vacuum(ctx, stagedName); err != nil {
		return err
	}
	remote.Close()
	p.Close()
	if err = os.Rename(stagedName, s.pending()); err != nil {
		return err
	}
	return s.Push(ctx)
}

func (s *Service) finishPending(ctx context.Context, c repository.Cataloger) error {
	compact := filepath.Join(s.Directory, "completed-"+newID()+".db")
	defer os.Remove(compact)
	if err := c.Vacuum(ctx, compact); err != nil {
		return err
	}
	completed, err := repository.NewCatalogRepository(ctx, compact)
	if err != nil {
		return err
	}
	err = clearOperation(ctx, completed)
	closeErr := completed.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := c.Close(); err != nil {
		return err
	}
	if err := os.Rename(compact, s.path()); err != nil {
		return err
	}
	return os.Remove(s.pending())
}

func (s *Service) ClassRemotes(ctx context.Context, id string) ([]string, error) {
	file := s.path()
	if s.Pending() {
		file = s.pending()
	}
	c, err := repository.NewCatalogRepository(ctx, file)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	saved, err := c.GetMeta(ctx, "class_snapshot")
	if err != nil {
		return nil, err
	}
	if saved != "" {
		id = saved
	}
	v, err := Resolve(ctx, c, id)
	if err != nil {
		return nil, err
	}
	locations, err := c.Locations(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range locations {
		remote, err := s.Registry.Remote(ctx, l.Provider)
		if err != nil {
			return nil, err
		}
		if remote.Kind == "aws" {
			out = append(out, l.Provider)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("backup: snapshot has no registered AWS copy")
	}
	return out, nil
}

func clearOperation(ctx context.Context, c repository.Cataloger) error {
	for _, key := range []string{"operation", "move_to", "move_set", "move_retrieval", "move_metadata_ready", "class_snapshot", "class_target"} {
		if err := c.SetMeta(ctx, key, ""); err != nil {
			return err
		}
	}
	return nil
}
