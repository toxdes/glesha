package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"glesha/database/model"
	"glesha/database/repository"
)

// Sync never resolves divergent changes by replacing remote history.
func (s *Service) Sync(ctx context.Context) error {
	if s.Set.CatalogTo == "" || s.Set.CatalogTo == "local" {
		return nil
	}

	dirty := ""
	publish := s.Pending()
	if publish {
		p, err := repository.NewCatalogRepository(ctx, s.pending())
		if err != nil {
			return err
		}
		op, readErr := p.GetMeta(ctx, "operation")
		var dirtyErr error
		dirty, dirtyErr = p.GetMeta(ctx, "dirty")
		selected, selectionErr := p.GetMeta(ctx, "baseline_changed")
		if selected == "true" {
			dirty = "baseline"
		}
		p.Close()
		if selectionErr != nil {
			return selectionErr
		}
		if readErr != nil {
			return readErr
		}
		if dirtyErr != nil {
			return dirtyErr
		}
		if op != "push" {
			return fmt.Errorf("%w: finish %s before sync", ErrPending, op)
		}
	} else {
		c, err := s.Catalog(ctx)
		if err != nil {
			return err
		}
		dirty, err = c.GetMeta(ctx, "dirty")
		selected, selectionErr := c.GetMeta(ctx, "baseline_changed")
		if selected == "true" {
			dirty = "baseline"
		}
		c.Close()
		if selectionErr != nil {
			return selectionErr
		}
		if err != nil {
			return err
		}
		publish = dirty != ""
	}
	if publish {
		if err := s.Push(ctx); err != nil {
			if !errors.Is(err, ErrConflict) {
				return err
			}
			if dirty == "baseline" {
				return fmt.Errorf("%w: incremental starting snapshot changed on both sides", ErrConflict)
			}
			if err = s.Reconcile(ctx); err != nil {
				return err
			}
		}
	} else if err := s.Pull(ctx); err != nil {
		return err
	}

	if err := s.Refresh(ctx); err != nil {
		return err
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	changed := false
	err = s.History(ctx, func(v model.Snapshot, locations []model.Location) error {
		original, err := c.Locations(ctx, v.ID)
		if err != nil {
			return err
		}
		for i, l := range locations {
			if i < len(original) && sameObservation(l, original[i]) {
				continue
			}
			if err = c.PutLocation(ctx, l); err != nil {
				return err
			}
			changed = true
		}
		if v.Layout == "chunked" {
			saved, err := c.Snapshot(ctx, v.ID)
			if err != nil {
				return err
			}
			different := false
			for i, ch := range v.Chunks {
				for j, l := range ch.Locations {
					if !sameObservation(l, saved.Chunks[i].Locations[j]) {
						different = true
					}
				}
			}
			if different {
				if err = c.PutSnapshot(ctx, v); err != nil {
					return err
				}
				changed = true
			}
		}
		return nil
	})
	if err == nil && changed {
		err = c.SetMeta(ctx, "dirty", "observations")
	}
	c.Close()
	if err != nil {
		return err
	}
	if changed {
		if err = s.Push(ctx); err != nil {
			return err
		}
	}
	if err = s.RefreshMetadataStatus(ctx); err != nil {
		return err
	}
	c, err = s.Catalog(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.SetMeta(ctx, "last_sync", now().Format(time.RFC3339Nano))
}
func sameObservation(a, b model.Location) bool {
	return a.Key == b.Key && a.Version == b.Version && a.CurrentClass == b.CurrentClass && a.ObservedClass == b.ObservedClass && a.Cold == b.Cold && a.Restore == b.Restore && a.ArchiveStatus == b.ArchiveStatus
}

func sameReference(a, b model.Location) bool {
	return a.Provider == b.Provider && a.Key == b.Key && a.Version == b.Version && a.ETag == b.ETag
}

// PublishImport leaves a normal pending publication that Retry can finish.
func (s *Service) PublishImport(ctx context.Context) error {
	if s.Set.CatalogTo == "" || s.Set.CatalogTo == "local" {
		return nil
	}
	if err := s.Push(ctx); err != nil {
		return fmt.Errorf("%w: imported locally; catalog publication pending: %w", ErrPending, err)
	}
	return nil
}

func (s *Service) PrepareImportPublication(ctx context.Context) error {
	if s.Set.CatalogTo == "" || s.Set.CatalogTo == "local" {
		return nil
	}
	p, err := s.stageOperation(ctx, "push")
	if err != nil {
		return err
	}
	return p.Close()
}

func (s *Service) Resolve(ctx context.Context, keep string) error {
	if keep != "local" && keep != "remote" {
		return fmt.Errorf("backup: resolution must keep local or remote")
	}
	if !s.Pending() {
		p, err := s.stageOperation(ctx, "push")
		if err != nil {
			return err
		}
		p.Close()
	}
	return s.reconcile(ctx, keep)
}

func (s *Service) ResumeImportPublication(ctx context.Context, key, file string) error {
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return err
	}
	defer p.Close()
	op, err := p.GetMeta(ctx, "operation")
	if err != nil {
		return err
	}
	dirty, err := p.GetMeta(ctx, "dirty")
	if err != nil {
		return err
	}
	if op != "push" || dirty != "import" {
		return fmt.Errorf("%w: finish pending operation with retry", ErrPending)
	}
	found := false
	if err = p.EachSnapshot(ctx, func(v model.Snapshot) error {
		if !v.Imported {
			return nil
		}
		if file != "" && v.File == file {
			found = true
		}
		locations, err := p.Locations(ctx, v.ID)
		if err != nil {
			return err
		}
		for _, l := range locations {
			if key != "" && l.Key == key {
				found = true
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: different import publication pending; use retry", ErrPending)
	}
	p.Close()
	return s.Push(ctx)
}
