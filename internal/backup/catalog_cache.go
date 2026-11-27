package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"glesha/internal/archive"
	"glesha/internal/cloud"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
	"glesha/internal/logger"
)

type downloadedState struct {
	Version    model.DiscoveryVersion `json:"version"`
	Revisions  []revision             `json:"revisions"`
	Descriptor string                 `json:"descriptor"`
	Authority  string                 `json:"authority"`
}

func catalogCacheAhead(ctx context.Context, c repository.Cataloger, downloaded downloadedState, localAuthority string) (bool, error) {
	authority, err := c.GetMeta(ctx, model.StateAuthorityKey)
	if err != nil {
		return false, err
	}
	if authority == "" {
		authority = localAuthority
	}
	if downloaded.Authority != "" && downloaded.Authority != authority {
		return true, nil
	}
	current, err := c.GetMeta(ctx, "remote_number")
	if err != nil {
		return false, err
	}
	if current == "" {
		return true, nil
	}
	number, err := strconv.ParseInt(current, 10, 64)
	if err != nil {
		return false, fmt.Errorf("backup: invalid local catalog revision: %w", err)
	}
	return downloaded.Revisions[len(downloaded.Revisions)-1].Number > number, nil
}

func (s *Service) DownloadedCatalogAhead(ctx context.Context) (bool, error) {
	downloaded, err := s.remoteCatalog().downloadedState()
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return false, err
	}
	defer c.Close()
	return catalogCacheAhead(ctx, c, downloaded, s.localAuthority)
}

func (s *Service) CatalogLocked(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if s.Set.CatalogTo == "" || s.Set.CatalogTo == "local" {
		return false, nil
	}
	_, err := os.Stat(s.path())
	if err == nil {
		return false, nil
	}
	if !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

func (s *Service) CachedEncryption(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value, err := s.remoteCatalog().downloadedState()
	if err != nil {
		return err
	}
	s.Config.Catalog.Encrypted = strings.HasSuffix(value.Descriptor, ".gpg")
	return nil
}

// UnlockCached materializes downloaded state without contacting a provider.
func (s *Service) UnlockCached(ctx context.Context) (err error) {
	if s.Pending() {
		return fmt.Errorf("%w: finish pending work before unlocking downloaded state", ErrPending)
	}
	if err := s.useLocator(ctx); err != nil {
		return err
	}
	v, err := s.remoteCatalog().downloadedState()
	if err != nil {
		return err
	}
	peer := *s
	_, statErr := os.Stat(s.path())
	defer func() {
		if err != nil && errors.Is(statErr, os.ErrNotExist) {
			os.Remove(s.path())
		}
	}()
	peer.Stores = map[string]cloud.Store{s.Set.CatalogTo: &cachedCatalogStore{catalog: s.remoteCatalog(), state: v, provider: s.provider(s.Set.CatalogTo)}}
	if err = peer.LoadDescriptor(ctx); err != nil {
		return err
	}
	if err = peer.Pull(ctx); err != nil {
		return err
	}
	if err = s.Registry.Save(ctx, peer.Set); err != nil {
		return fmt.Errorf("backup: discovered set name conflicts; select by UUID: %w", err)
	}
	s.Set = peer.Set
	return nil
}

type cachedCatalogStore struct {
	catalog  *remoteCatalog
	state    downloadedState
	provider cloud.Provider
}

func (c *cachedCatalogStore) Provider() cloud.Provider { return c.provider }

func (c *cachedCatalogStore) Put(context.Context, string, string, string) (cloud.Object, error) {
	return cloud.Object{}, fmt.Errorf("backup: downloaded state is read-only")
}

func (c *cachedCatalogStore) List(ctx context.Context, prefix, after string) ([]cloud.Object, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	var objects []cloud.Object
	for _, rev := range c.state.Revisions {
		key := c.revisionKey(rev)
		if strings.HasPrefix(key, prefix) && key > after {
			objects = append(objects, cloud.Object{Key: key})
		}
	}
	return objects, "", nil
}

func (c *cachedCatalogStore) revisionKey(rev revision) string {
	if c.provider.Capabilities.ConditionalWrites {
		return c.catalog.prefix + "commits/" + fmt.Sprintf("%020d.json", rev.Number)
	}
	return c.catalog.prefix + "heads/" + fmt.Sprintf("%020d-%s.json", rev.Number, rev.ID)
}

func (c *cachedCatalogStore) Head(ctx context.Context, key, version string) (cloud.Object, error) {
	r, object, err := c.Get(ctx, key, version)
	if err != nil {
		return object, err
	}
	return object, r.Close()
}

func (c *cachedCatalogStore) Get(ctx context.Context, key, version string) (io.ReadCloser, cloud.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, cloud.Object{}, err
	}
	for _, rev := range c.state.Revisions {
		if key == c.revisionKey(rev) {
			b, err := json.Marshal(rev)
			return io.NopCloser(bytes.NewReader(b)), cloud.Object{Key: key, Size: int64(len(b))}, err
		}
	}
	expected := ""
	for _, rev := range c.state.Revisions {
		if rev.Key == key {
			expected = rev.Hash
		}
	}
	if key == c.state.Descriptor {
		expected = c.state.Revisions[len(c.state.Revisions)-1].DescriptorHash
	} else if expected == "" {
		return nil, cloud.Object{}, &os.PathError{Op: "read cached catalog", Path: key, Err: os.ErrNotExist}
	}
	file := c.catalog.metadataCachePath(key)
	hash, size, err := archive.Hash(logger.WithProgressLabel(logger.WithProgress(ctx, nil), "Checking cached catalog"), file)
	if err != nil {
		return nil, cloud.Object{}, err
	}
	if expected != "" && hash != expected {
		return nil, cloud.Object{}, fmt.Errorf("backup: cached metadata checksum differs")
	}
	r, err := os.Open(file)
	return r, cloud.Object{Key: key, Size: size, Hash: hash}, err
}
