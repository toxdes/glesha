package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"glesha/archive"
	"glesha/cloud"
	"glesha/database/model"
	"glesha/database/repository"
	L "glesha/logger"
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
	downloaded, err := s.downloadedState()
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

func (s *Service) metadataCachePath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.Directory, "metadata-"+hex.EncodeToString(sum[:]))
}

func (s *Service) downloadedState() (downloadedState, error) {
	var v downloadedState
	b, err := os.ReadFile(filepath.Join(s.Directory, "downloaded-state.json"))
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("backup: read downloaded state: %w", err)
	}
	if err = v.Version.Validate(); err != nil {
		return v, err
	}
	if len(v.Revisions) == 0 {
		return v, fmt.Errorf("backup: downloaded catalog has no revisions")
	}
	if v.Authority != "" && v.Authority != s.Set.CatalogTo {
		return v, fmt.Errorf("backup: downloaded catalog belongs to a previous authority; run catalog sync")
	}
	for _, rev := range v.Revisions {
		if err = rev.Version.Validate(); err != nil {
			return v, err
		}
		if rev.Set != s.Set.ID || !strings.HasPrefix(rev.Key, s.prefix()+"batches/") {
			return v, fmt.Errorf("backup: downloaded catalog identity differs")
		}
	}
	last := v.Revisions[len(v.Revisions)-1]
	stem := s.prefix() + "descriptors/" + fmt.Sprintf("%020d-%s.json.gz", last.Number, last.ID)
	if v.Descriptor != stem && v.Descriptor != stem+".gpg" {
		return v, fmt.Errorf("backup: downloaded descriptor identity differs")
	}
	return v, nil
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
	value, err := s.downloadedState()
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
	v, err := s.downloadedState()
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
	peer.Stores = map[string]cloud.Store{s.Set.CatalogTo: &cachedCatalogStore{service: &peer, state: v}}
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
	service *Service
	state   downloadedState
}

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
	if c.service.Kinds[c.service.Set.CatalogTo] == "aws" {
		return c.service.prefix() + "commits/" + fmt.Sprintf("%020d.json", rev.Number)
	}
	return c.service.prefix() + "heads/" + fmt.Sprintf("%020d-%s.json", rev.Number, rev.ID)
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
	file := c.service.metadataCachePath(key)
	hash, size, err := archive.Hash(L.WithProgressLabel(L.WithProgress(ctx, nil), "Checking cached catalog"), file)
	if err != nil {
		return nil, cloud.Object{}, err
	}
	if expected != "" && hash != expected {
		return nil, cloud.Object{}, fmt.Errorf("backup: cached metadata checksum differs")
	}
	r, err := os.Open(file)
	return r, cloud.Object{Key: key, Size: size, Hash: hash}, err
}
