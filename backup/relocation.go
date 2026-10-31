package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"glesha/cloud"
	"glesha/database/model"
	"glesha/database/repository"
)

const authorityRelocationKey = "authority_relocation"
const maximumAuthorityRedirects = 16

var ErrAuthorityMoved = errors.New("backup: catalog authority moved")

type authorityRelocation struct {
	Locator model.Locator   `json:"locator"`
	History []model.Locator `json:"history"`
}

func (s *Service) readLocator(ctx context.Context, store cloud.Store, key string) (model.Locator, error) {
	var value model.Locator
	parts := strings.Split(strings.TrimPrefix(key, s.directoryPrefix()), "/")
	if len(parts) != 2 || !strings.HasSuffix(parts[1], ".json") {
		return value, fmt.Errorf("backup: malformed discovery key %q", key)
	}
	r, _, err := store.Get(ctx, key, "")
	if err != nil {
		return value, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return value, fmt.Errorf("backup: read discovery record: %w", err)
	}
	if len(b) > 1<<20 {
		return value, fmt.Errorf("backup: discovery record exceeds 1MiB")
	}
	if err = json.Unmarshal(b, &value); err != nil {
		return value, err
	}
	if value.Set != parts[0] || locatorID(value)+".json" != parts[1] {
		return value, fmt.Errorf("backup: discovery identity mismatch")
	}
	return value, s.rememberLocator(ctx, value)
}

func (s *Service) refreshAuthority(ctx context.Context, store cloud.Store) error {
	return cloud.List(ctx, store, s.directoryPrefix()+s.Set.ID+"/", func(object cloud.Object) error {
		_, err := s.readLocator(ctx, store, object.Key)
		return err
	})
}

func (s *Service) followAuthority(ctx context.Context, rev revision) error {
	if rev.Redirect == nil {
		return nil
	}
	if s.redirectHops >= maximumAuthorityRedirects {
		return fmt.Errorf("%w: too many catalog redirects; refresh discovery", ErrConflict)
	}
	s.redirectHops++
	for _, value := range rev.AuthorityHistory {
		if value.Set != s.Set.ID {
			return fmt.Errorf("backup: authority history belongs to another set")
		}
		if err := s.rememberLocator(ctx, value); err != nil {
			return err
		}
	}
	if err := s.rememberLocator(ctx, *rev.Redirect); err != nil {
		return err
	}
	return s.useLocator(ctx)
}

func (s *Service) checkPublishingAuthority(ctx context.Context, store cloud.Store) error {
	if err := s.refreshAuthority(ctx, store); err != nil {
		return err
	}
	current, err := s.Locator(ctx, s.Set.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Authority.ID != s.Set.CatalogTo || current.Prefix != s.Config.Catalog.Prefix {
		return fmt.Errorf("%w: catalog authority changed; run catalog sync before publication", ErrConflict)
	}
	return nil
}

func (s *Service) retireMoveSource(ctx context.Context, pending repository.Cataloger, previous model.Set, parent model.Locator) error {
	if previous.CatalogTo == s.Set.CatalogTo {
		return nil
	}
	expected, err := pending.GetMeta(ctx, "move_source_revision")
	if err != nil {
		return err
	}
	peer := *s
	peer.Set = previous
	peer.Directory, err = os.MkdirTemp(s.Directory, ".glesha-retirement-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(peer.Directory)
	store, err := peer.metadataStore()
	if err != nil {
		return err
	}
	history, err := peer.revisions(ctx, store)
	if err != nil {
		return err
	}
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Redirect != nil && last.Redirect.Authority.ID == s.Set.CatalogTo && last.Parent == expected {
			return nil
		}
		if last.ID != expected {
			return fmt.Errorf("%w: source changed during move; preserve pending work and restart after syncing", ErrConflict)
		}
	} else if expected != "" {
		return fmt.Errorf("%w: source history disappeared during move", ErrConflict)
	}
	if err = peer.Pull(ctx); err != nil {
		return err
	}
	c, err := peer.Catalog(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	current, err := c.GetMeta(ctx, "remote_revision")
	if err != nil {
		return err
	}
	if current != expected {
		return fmt.Errorf("%w: source changed while preparing retirement", ErrConflict)
	}
	authority, err := s.Registry.Remote(ctx, s.Set.CatalogTo)
	if err != nil {
		return err
	}
	authority.Name = authority.ID
	move := authorityRelocation{Locator: model.Locator{Version: model.DiscoveryVersionInitial, Set: s.Set.ID, Authority: authority, Prefix: s.Config.Catalog.Prefix, Parent: locatorID(parent)}}
	values, err := s.Registry.Locators(ctx, s.Set.ID)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		move.History = append(move.History, values[id])
	}
	b, err := json.Marshal(move)
	if err != nil {
		return err
	}
	if err = c.SetMeta(ctx, authorityRelocationKey, string(b)); err != nil {
		return err
	}
	if err = c.SetMeta(ctx, "force_checkpoint", "true"); err != nil {
		return err
	}
	if err = c.SetMeta(ctx, "dirty", "relocation"); err != nil {
		return err
	}
	c.Close()
	return peer.Push(ctx)
}
