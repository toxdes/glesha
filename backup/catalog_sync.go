package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"glesha/database/model"
	"glesha/database/repository"
)

const stateProtocolMeta = model.StateProtocolKey

func catalogStateVersion(ctx context.Context, c repository.Cataloger) (model.StateVersion, error) {
	value, err := c.GetMeta(ctx, stateProtocolMeta)
	if err != nil {
		return 0, err
	}
	if value == "" {
		return model.StateVersionLegacy, nil
	}
	return model.ParseStateVersion(value)
}

// Sync never resolves divergent changes by replacing remote history.
func (s *Service) Sync(ctx context.Context) error {
	if err := s.SyncCatalog(ctx); err != nil {
		return err
	}
	return s.syncObservations(ctx)
}

// SyncCatalog synchronizes portable state without probing archive objects.
func (s *Service) SyncCatalog(ctx context.Context) error {
	if err := s.useLocator(ctx); err != nil {
		return err
	}
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
		file := s.path()
		if s.Pending() {
			file = s.pending()
		}
		local, err := repository.NewCatalogRepository(ctx, file)
		if err != nil {
			return err
		}
		authority, err := local.GetMeta(ctx, model.StateAuthorityKey)
		local.Close()
		if err != nil {
			return err
		}
		if authority == "" {
			authority = s.localAuthority
		}
		if authority != "" && authority != s.Set.CatalogTo {
			return fmt.Errorf("%w: catalog authority changed with unpublished work; use catalog resolve --keep local or remote", ErrConflict)
		}
		if err := s.Push(ctx); err != nil {
			if !errors.Is(err, ErrConflict) {
				return err
			}
			if dirty == "baseline" {
				return fmt.Errorf("%w: incremental starting snapshot changed on both sides", ErrConflict)
			}
			if dirty == "configuration" {
				return fmt.Errorf("%w: unpublished configuration conflicts with remote state; use catalog resolve", ErrConflict)
			}
			if err = s.Reconcile(ctx); err != nil {
				return err
			}
		}
	} else if err := s.Pull(ctx); err != nil {
		return err
	}
	migrate, err := s.prepareStateMigration(ctx)
	if err != nil {
		return err
	}
	if migrate {
		if err = s.Push(ctx); err != nil {
			return err
		}
	}
	if err = s.rememberAuthority(ctx, ""); err != nil {
		return err
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.SetMeta(ctx, "last_sync", now().Format(time.RFC3339Nano))
}

func (s *Service) prepareStateMigration(ctx context.Context) (bool, error) {
	c, err := s.Catalog(ctx)
	if err != nil {
		return false, err
	}
	defer c.Close()
	version, err := catalogStateVersion(ctx, c)
	if err != nil {
		return false, fmt.Errorf("backup: migrate catalog state: %w", err)
	}
	if version == model.StateVersionPortable {
		return false, nil
	}
	if err = c.SetMeta(ctx, "force_checkpoint", "true"); err != nil {
		return false, err
	}
	if err = c.SetMeta(ctx, "dirty", "migration"); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) syncObservations(ctx context.Context) error {
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
