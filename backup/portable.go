package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"glesha/archive"
	"glesha/cloud"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
)

var ErrNoRemoteCatalog = errors.New("backup: catalog authority has no committed state")

func locatorID(v model.Locator) string {
	fields := []string{v.Version.String(), v.Set, v.Authority.ID, v.Authority.Kind,
		v.Authority.Bucket, v.Authority.Endpoint, v.Authority.Region, v.Prefix, v.Parent}
	fields = append(fields, v.Supersedes...)
	sum := sha256.Sum256([]byte(strings.Join(fields, "\x00")))
	return hex.EncodeToString(sum[:])
}

func validateLocator(v model.Locator) error {
	if err := v.Version.Validate(); err != nil {
		return fmt.Errorf("backup: read discovery record: %w", err)
	}
	if _, err := uuid.Parse(v.Set); err != nil {
		return fmt.Errorf("backup: invalid discovery set: %w", err)
	}
	r := v.Authority
	id := r.Identity()
	if r.ID != id || r.Name != id || r.Bucket == "" || (r.Kind != "aws" && r.Kind != "b2") || v.Prefix == "" || strings.HasPrefix(v.Prefix, "/") || path.Clean(v.Prefix) != v.Prefix {
		return fmt.Errorf("backup: invalid discovery authority or prefix")
	}
	parents := append([]string(nil), v.Supersedes...)
	if v.Parent != "" {
		parents = append(parents, v.Parent)
	}
	for _, parent := range parents {
		b, err := hex.DecodeString(parent)
		if err != nil || len(b) != sha256.Size {
			return fmt.Errorf("backup: invalid discovery parent")
		}
	}
	return nil
}

// Locator selects the sole leaf, never a winner based on timestamps.
func (s *Service) Locator(ctx context.Context, set string) (model.Locator, error) {
	values, err := s.Registry.Locators(ctx, set)
	if err != nil {
		return model.Locator{}, err
	}
	parents := map[string]bool{}
	for id, v := range values {
		if err = validateLocator(v); err != nil {
			return model.Locator{}, err
		}
		if locatorID(v) != id {
			return model.Locator{}, fmt.Errorf("backup: invalid cached discovery record")
		}
		predecessors := append([]string(nil), v.Supersedes...)
		if v.Parent != "" {
			predecessors = append(predecessors, v.Parent)
		}
		for _, predecessor := range predecessors {
			if _, ok := values[predecessor]; !ok {
				return model.Locator{}, fmt.Errorf("%w: incomplete authority history for %s", ErrConflict, set)
			}
			parents[predecessor] = true
		}
	}
	for id, value := range values {
		if value.Parent != "" {
			continue
		}
		for _, successor := range values {
			if successor.Parent != "" && successor.Authority.ID == value.Authority.ID && successor.Prefix == value.Prefix {
				parents[id] = true
			}
		}
	}
	var result model.Locator
	for id, v := range values {
		if parents[id] {
			continue
		}
		if result.Set != "" {
			if result.Authority.ID == v.Authority.ID && result.Prefix == v.Prefix {
				if v.Parent != "" {
					result = v
				}
				continue
			}
			return model.Locator{}, fmt.Errorf("%w: multiple catalog authorities for %s; use catalog resolve %s --authority REMOTE", ErrConflict, set, set)
		}
		result = v
	}
	if result.Set == "" {
		return result, sql.ErrNoRows
	}
	return result, nil
}

func (s *Service) rememberLocator(ctx context.Context, value model.Locator) error {
	if err := validateLocator(value); err != nil {
		return err
	}
	if err := s.Registry.PutLocator(ctx, locatorID(value), value); err != nil {
		return err
	}
	remote, err := s.Registry.BindRemote(ctx, value.Authority)
	if err != nil {
		return err
	}
	if _, err = s.Registry.Set(ctx, value.Set); errors.Is(err, sql.ErrNoRows) {
		return s.Registry.Create(ctx, model.Set{ID: value.Set, Name: value.Set, CatalogTo: remote.ID})
	}
	return err
}

func (s *Service) directoryPrefix() string {
	prefix := s.DiscoveryPrefix
	if prefix == "" {
		prefix = s.Config.Catalog.Prefix
	}
	return strings.TrimSuffix(prefix, "/") + "/directory/v1/"
}

// DiscoverState needs bucket credentials, never a catalog or archive passphrase.
func (s *Service) DiscoverState(ctx context.Context, remote string) error {
	store := s.Stores[remote]
	if store == nil {
		return fmt.Errorf("backup: discovery remote unavailable")
	}
	if err := s.Registry.ForgetLocatorReplicas(ctx, remote+":"+s.directoryPrefix()); err != nil {
		return err
	}
	prefix := s.directoryPrefix()
	if err := cloud.List(ctx, store, prefix, func(object cloud.Object) error {
		v, err := s.readLocator(ctx, store, object.Key)
		if err != nil {
			return err
		}
		return s.Registry.MarkLocatorReplicated(ctx, locatorID(v), remote+":"+s.directoryPrefix())
	}); err != nil {
		return err
	}
	sets := map[string]bool{}
	setPrefix := strings.TrimSuffix(s.Config.Catalog.Prefix, "/") + "/sets/"
	folder := "heads"
	if s.Kinds[remote] == "aws" {
		folder = "commits"
	}
	if err := cloud.List(ctx, store, setPrefix, func(object cloud.Object) error {
		parts := strings.Split(strings.TrimPrefix(object.Key, setPrefix), "/")
		if len(parts) == 3 && parts[1] == folder {
			if _, err := uuid.Parse(parts[0]); err != nil {
				return fmt.Errorf("backup: invalid discovered set ID")
			}
			sets[parts[0]] = true
		}
		return nil
	}); err != nil {
		return err
	}
	if len(sets) == 0 {
		return nil
	}
	authority, err := s.Registry.Remote(ctx, remote)
	if err != nil {
		return err
	}
	authority.Name = authority.ID
	ordered := make([]string, 0, len(sets))
	for id := range sets {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		values, err := s.Registry.Locators(ctx, id)
		if err != nil {
			return err
		}
		known := false
		for _, value := range values {
			known = known || value.Authority.ID == authority.ID && value.Prefix == s.Config.Catalog.Prefix
		}
		if known {
			continue
		}
		if err = s.rememberLocator(ctx, model.Locator{Version: model.DiscoveryVersionInitial, Set: id, Authority: authority, Prefix: strings.TrimSuffix(s.Config.Catalog.Prefix, "/")}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) useLocator(ctx context.Context) error {
	if s.DiscoveryPrefix == "" {
		s.DiscoveryPrefix = s.Config.Catalog.Prefix
	}
	v, err := s.Locator(ctx, s.Set.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	s.Set.CatalogTo = v.Authority.ID
	s.Config.Catalog.Prefix = v.Prefix
	return nil
}

func (s *Service) cachedMetadata(ctx context.Context, store cloud.Store, key, expected string) (string, error) {
	name := s.metadataCachePath(key)
	if expected != "" {
		if hash, _, err := archive.Hash(L.WithProgressLabel(L.WithProgress(ctx, nil), "Checking cached catalog"), name); err == nil && hash == expected {
			return name, nil
		}
	}
	r, _, err := store.Get(ctx, key, "")
	if err != nil {
		return "", err
	}
	defer r.Close()
	f, err := file_io.Temp(s.Directory)
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = copyContext(ctx, f, r); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if expected != "" {
		hash, _, err := archive.Hash(L.WithProgressLabel(ctx, "Checking downloaded catalog"), f.Name())
		if err != nil || hash != expected {
			return "", fmt.Errorf("backup: downloaded metadata checksum differs")
		}
	}
	if err = os.Rename(f.Name(), name); err != nil {
		return "", err
	}
	s.metadataDownloads++
	return name, nil
}

func (s *Service) descriptorFile(ctx context.Context, store cloud.Store, rev revision) (string, string, error) {
	if cached, err := s.downloadedState(); err == nil && cached.Authority == s.Set.CatalogTo && cached.Revisions[len(cached.Revisions)-1].ID == rev.ID && rev.DescriptorHash != "" {
		file, err := s.cachedMetadata(ctx, store, cached.Descriptor, rev.DescriptorHash)
		return file, cached.Descriptor, err
	}

	key := s.prefix() + "descriptors/" + fmt.Sprintf("%020d-%s.json.gz", rev.Number, rev.ID)
	if _, err := store.Head(ctx, key+".gpg", ""); err == nil {
		key += ".gpg"
	} else if !cloud.NotFound(err) && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	file, err := s.cachedMetadata(ctx, store, key, rev.DescriptorHash)
	return file, key, err
}

type CatalogCacheResult struct {
	Updated bool
}

// CacheCatalog downloads metadata only and preserves the last usable database.
func (s *Service) CacheCatalog(ctx context.Context) error {
	_, err := s.CacheCatalogWithResult(ctx)
	return err
}

// CacheCatalogWithResult reports new or repaired cached metadata.
func (s *Service) CacheCatalogWithResult(ctx context.Context) (CatalogCacheResult, error) {
	if s.Set.CatalogTo == "" || s.Set.CatalogTo == "local" {
		return CatalogCacheResult{}, nil
	}
	path := filepath.Join(s.Directory, "downloaded-state.json")
	before, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return CatalogCacheResult{}, err
	}
	downloads := s.metadataDownloads
	if err = s.cacheCatalog(ctx); err != nil {
		return CatalogCacheResult{}, err
	}
	after, err := os.ReadFile(path)
	return CatalogCacheResult{Updated: downloads != s.metadataDownloads || !bytes.Equal(before, after)}, err
}

func (s *Service) cacheCatalog(ctx context.Context) error {
	if err := s.useLocator(ctx); err != nil {
		return err
	}
	store, err := s.metadataStore()
	if err != nil || store == nil {
		return err
	}
	revisions, err := s.revisions(ctx, store)
	if err != nil {
		return err
	}
	if len(revisions) == 0 {
		return ErrNoRemoteCatalog
	}
	if err = s.refreshAuthority(ctx, store); err != nil {
		return err
	}
	before := s.Set.CatalogTo
	if err = s.useLocator(ctx); err != nil {
		return err
	}
	if before != s.Set.CatalogTo {
		if s.Stores[s.Set.CatalogTo] == nil {
			return ErrAuthorityMoved
		}
		return s.cacheCatalog(ctx)
	}
	if revisions[len(revisions)-1].Redirect != nil {
		if err = s.followAuthority(ctx, revisions[len(revisions)-1]); err != nil {
			return err
		}
		if s.Stores[s.Set.CatalogTo] == nil {
			return ErrAuthorityMoved
		}
		return s.cacheCatalog(ctx)
	}
	start := 0
	for i, rev := range revisions {
		if rev.Checkpoint {
			start = i
		}
	}
	for _, rev := range revisions[start:] {
		if _, err = s.cachedMetadata(ctx, store, rev.Key, rev.Hash); err != nil {
			return err
		}
	}
	last := revisions[len(revisions)-1]
	_, key, err := s.descriptorFile(ctx, store, last)
	if err != nil {
		return err
	}
	return s.saveJob(filepath.Join(s.Directory, "downloaded-state.json"), downloadedState{
		Version: model.DiscoveryVersionInitial, Revisions: revisions[start:], Descriptor: key, Authority: s.Set.CatalogTo,
	})
}

func (s *Service) LoadDescriptor(ctx context.Context) error {
	if err := s.useLocator(ctx); err != nil {
		return err
	}
	return s.loadDescriptor(ctx)
}

func (s *Service) loadDescriptor(ctx context.Context) error {
	store, err := s.metadataStore()
	if err != nil || store == nil {
		return err
	}
	revisions, err := s.revisions(ctx, store)
	if err != nil || len(revisions) == 0 {
		return err
	}
	file, key, err := s.descriptorFile(ctx, store, revisions[len(revisions)-1])
	if err != nil {
		return err
	}
	s.Config.Catalog.Encrypted = strings.HasSuffix(key, ".gpg")
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := s.metadataReader(ctx, f, key)
	if err != nil {
		return fmt.Errorf("backup: cannot unlock catalog %s: %w", s.Set.ID, err)
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, (4<<20)+1))
	if err != nil {
		return fmt.Errorf("backup: read catalog descriptor: %w", err)
	}
	if len(b) > 4<<20 {
		return fmt.Errorf("backup: catalog descriptor exceeds 4MiB")
	}
	var d Descriptor
	if err = json.Unmarshal(b, &d); err != nil {
		return err
	}
	if err = d.Version.Validate(); err != nil {
		return fmt.Errorf("backup: unlock descriptor: %w", err)
	}
	if d.Version != revisions[len(revisions)-1].Version || d.Set.ID != s.Set.ID || d.Set.CatalogTo != s.Set.CatalogTo || !validName.MatchString(d.Set.Name) {
		return fmt.Errorf("backup: catalog descriptor identity or version differs")
	}
	for _, remote := range d.Remotes {
		bound, err := s.Registry.BindRemote(ctx, remote)
		if errors.Is(err, repository.ErrRemoteLocationChanged) {
			remote.Name = remote.ID
			bound, err = s.Registry.BindRemote(ctx, remote)
		}
		if err != nil {
			return fmt.Errorf("backup: bind catalog remote: %w", err)
		}
		if bound.ID != remote.ID {
			return fmt.Errorf("backup: catalog remote identity differs")
		}
	}
	for i := range d.Set.Roots {
		d.Set.Roots[i].Path = ""
		if s.Set.Name != s.Set.ID {
			for _, root := range s.Set.Roots {
				if root.Name == d.Set.Roots[i].Name {
					d.Set.Roots[i].Path = root.Path
				}
			}
		}
	}
	s.Set = d.Set
	return nil
}

func (s *Service) RefreshSet(ctx context.Context) error {
	if err := s.LoadDescriptor(ctx); err != nil {
		return err
	}
	if err := s.Registry.Save(ctx, s.Set); err != nil {
		return fmt.Errorf("backup: discovered set name conflicts; select by UUID: %w", err)
	}
	return nil
}

// PublishDirectory repairs immutable locator copies without changing catalog history.
func (s *Service) PublishDirectory(ctx context.Context) error {
	_, err := s.PublishDirectoryWithResult(ctx)
	return err
}

// PublishDirectoryWithResult counts registry copies repaired.
func (s *Service) PublishDirectoryWithResult(ctx context.Context) (int, error) {
	updated := 0
	var progress *L.Progress
	started := false
	defer func() { progress.Finish() }()
	ctx = cloud.WithTransferLabel(ctx, "registry record")
	var failures []error
	sets := []model.Set{}
	if err := s.Registry.Each(ctx, func(set model.Set) error { sets = append(sets, set); return nil }); err != nil {
		return updated, err
	}
	for _, set := range sets {
		locators, err := s.Registry.Locators(ctx, set.ID)
		if err != nil {
			return updated, err
		}
		for id, locator := range locators {
			if err = validateLocator(locator); err != nil {
				return updated, err
			}
			if locatorID(locator) != id {
				return updated, fmt.Errorf("backup: registry record checksum differs")
			}
			data, err := json.Marshal(locator)
			if err != nil {
				return updated, fmt.Errorf("backup: encode discovery record: %w", err)
			}
			file, err := file_io.Temp(s.Directory)
			if err != nil {
				return updated, err
			}
			_, writeErr := file.Write(data)
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				os.Remove(file.Name())
				return updated, errors.Join(writeErr, closeErr)
			}
			for remote, store := range s.Stores {
				scope := remote + ":" + s.directoryPrefix()
				copied, err := s.Registry.LocatorReplicated(ctx, id, scope)
				if err != nil {
					return updated, err
				}
				if copied {
					continue
				}
				if !started {
					progress = L.StartProgress(ctx, "Updating registry copies", -1)
					started = true
				}
				key := s.directoryPrefix() + set.ID + "/" + id + ".json"
				if _, err = cloud.Create(L.WithProgress(ctx, nil), store, key, file.Name(), "STANDARD"); err != nil {
					failures = append(failures, fmt.Errorf("backup: registry replica %s: %w", remote, err))
				} else if err = s.Registry.MarkLocatorReplicated(ctx, id, scope); err != nil {
					failures = append(failures, err)
				} else {
					updated++
				}
			}
			os.Remove(file.Name())
		}
	}
	return updated, errors.Join(failures...)
}

func (s *Service) rememberAuthority(ctx context.Context, previous string) error {
	if s.Set.CatalogTo == "" || s.Set.CatalogTo == "local" {
		return nil
	}
	current, lookupErr := s.Locator(ctx, s.Set.ID)
	if lookupErr == nil {
		if current.Authority.ID == s.Set.CatalogTo && current.Prefix == s.Config.Catalog.Prefix {
			return nil
		}
		if previous == "" {
			return fmt.Errorf("%w: catalog authority changed; resolve it explicitly", ErrConflict)
		}
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return lookupErr
	}
	authority, err := s.Registry.Remote(ctx, s.Set.CatalogTo)
	if err != nil {
		return err
	}
	authority.Name = authority.ID
	return s.rememberLocator(ctx, model.Locator{Version: model.DiscoveryVersionInitial, Set: s.Set.ID, Authority: authority, Prefix: strings.TrimSuffix(s.Config.Catalog.Prefix, "/"), Parent: previous})
}
