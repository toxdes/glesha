package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"glesha/database/model"
	"glesha/database/repository"
)

// ResolveAuthority preserves both histories and changes only the chosen authority.
func (s *Service) ResolveAuthority(ctx context.Context, remote string) error {
	if s.Pending() {
		return fmt.Errorf("%w: finish pending work before selecting an authority", ErrPending)
	}
	values, err := s.Registry.Locators(ctx, s.Set.ID)
	if err != nil {
		return err
	}
	selectedID := ""
	var selected model.Locator
	for id, value := range values {
		if err = validateLocator(value); err != nil {
			return err
		}
		if value.Authority.ID == remote {
			if selectedID != "" && selected.Prefix != value.Prefix {
				return fmt.Errorf("%w: selected authority has multiple metadata prefixes", ErrConflict)
			}
			selectedID, selected = id, value
		}
	}
	if selectedID == "" {
		return fmt.Errorf("backup: selected remote is not a discovered catalog authority")
	}
	peer := *s
	peer.Set.CatalogTo = remote
	peer.Config.Catalog.Prefix = selected.Prefix
	peer.Directory, err = os.MkdirTemp(s.Directory, ".glesha-authority-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(peer.Directory)
	if err = peer.loadDescriptor(ctx); err != nil {
		return err
	}
	if err = peer.Pull(ctx); err != nil {
		return err
	}
	if existing, lookupErr := s.Registry.Set(ctx, peer.Set.Name); lookupErr == nil && existing.ID != s.Set.ID {
		return fmt.Errorf("%w: selected catalog name belongs to another set", ErrConflict)
	} else if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return lookupErr
	}
	if _, err = os.Stat(s.path()); err == nil {
		previous, err := s.Catalog(ctx)
		if err != nil {
			return err
		}
		err = repository.Preserve(ctx, previous, filepath.Join(s.Directory, "before-authority-"+newID()+".db"))
		closeErr := previous.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	choice := selected
	choice.Parent = selectedID
	choice.Supersedes = nil
	for id := range values {
		if id != selectedID {
			choice.Supersedes = append(choice.Supersedes, id)
		}
	}
	sort.Strings(choice.Supersedes)
	if err = s.Registry.PutLocator(ctx, locatorID(choice), choice); err != nil {
		return err
	}
	if err = os.Rename(peer.path(), s.path()); err != nil {
		return err
	}
	if err = s.Registry.Save(ctx, peer.Set); err != nil {
		return err
	}
	s.Set, s.Config = peer.Set, peer.Config
	if err = os.Remove(filepath.Join(s.Directory, "downloaded-state.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
