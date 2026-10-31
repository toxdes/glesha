package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"glesha/cloud"
	"glesha/database/model"
	"glesha/database/repository"
)

type stateOnlyStore struct {
	*memoryStore
	payload string
}

func (s *stateOnlyStore) Head(ctx context.Context, key, version string) (cloud.Object, error) {
	if key == s.payload {
		return cloud.Object{}, fmt.Errorf("unexpected payload HEAD")
	}
	return s.memoryStore.Head(ctx, key, version)
}

func (s *stateOnlyStore) Get(ctx context.Context, key, version string) (io.ReadCloser, cloud.Object, error) {
	if key == s.payload {
		return nil, cloud.Object{}, fmt.Errorf("unexpected payload download")
	}
	return s.memoryStore.Get(ctx, key, version)
}

func (s *stateOnlyStore) Put(ctx context.Context, key, file, class string) (cloud.Object, error) {
	if key == s.payload {
		return cloud.Object{}, fmt.Errorf("unexpected payload upload")
	}
	return s.memoryStore.Put(ctx, key, file, class)
}

func freshStateService(t *testing.T, source *Service) *Service {
	t.Helper()
	registry, err := repository.NewRegistryRepository(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })
	if source.Set.CatalogTo != "" {
		remote, err := source.Registry.Remote(context.Background(), source.Set.CatalogTo)
		if err != nil {
			t.Fatal(err)
		}
		remote.Name = "new-machine-alias"
		if _, err = registry.BindRemote(context.Background(), remote); err != nil {
			t.Fatal(err)
		}
	}
	return &Service{Config: source.Config, Budget: source.Budget, Registry: registry, Directory: t.TempDir(), Stores: source.Stores, Kinds: source.Kinds}
}

func TestEncryptedStateRecoveryWithoutPayloadTransfers(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	s.Config.Catalog.Encrypted = true
	s.CatalogPassword = []byte("catalog secret")
	if err := os.WriteFile(filepath.Join(source, "song.txt"), []byte("music"), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := s.Run(ctx, RunOptions{Password: []byte("archive secret"), Compression: "gzip", Output: filepath.Join(t.TempDir(), "full.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	guarded := &stateOnlyStore{memoryStore: store, payload: s.payloadKey(v)}
	s.Stores = map[string]cloud.Store{s.Set.CatalogTo: guarded}
	peer := freshStateService(t, s)
	root := peer.Directory
	if err = peer.DiscoverState(ctx, s.Set.CatalogTo); err != nil {
		t.Fatal(err)
	}
	if err = peer.OpenSet(ctx, s.Set.ID, root); err != nil {
		t.Fatal(err)
	}
	if peer.Set.Name != peer.Set.ID {
		t.Fatal("discovery exposed an encrypted name")
	}
	if err = peer.CacheCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	peer.Stores = nil
	peer.CatalogPassword = []byte("wrong")
	if err = peer.UnlockCached(ctx); err == nil {
		t.Fatal("wrong catalog password accepted")
	}
	if _, err = os.Stat(peer.path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed unlock left a usable-looking database", err)
	}
	peer.CatalogPassword = []byte("catalog secret")
	if err = peer.UnlockCached(ctx); err != nil {
		t.Fatal("offline unlock failed", err)
	}
	if peer.Set.Name != s.Set.Name || len(peer.Set.Roots) == 0 || peer.Set.Roots[0].Path != "" {
		t.Fatal("private name or local root binding was not recovered correctly")
	}
	c, err := peer.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	recovered, err := c.Snapshot(ctx, v.ID)
	if err != nil || recovered.Hash != v.Hash || recovered.File != "" {
		t.Fatal("snapshot identity or local cache paths changed incorrectly", err)
	}
	peer.Stores = s.Stores
	if err = peer.SyncCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	replica := newStore()
	peer.Stores["replica"] = replica
	if err = peer.PublishDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	for key, content := range replica.files {
		if !strings.Contains(key, "/directory/") || strings.Contains(string(content), s.Set.Name) || strings.Contains(string(content), source) {
			t.Fatal("directory leaked private state or uploaded payloads", key)
		}
	}
	if len(replica.files) == 0 {
		t.Fatal("registry not replicated")
	}
	c.Close()
	if err = os.WriteFile(filepath.Join(source, "song.txt"), []byte("new music"), 0600); err != nil {
		t.Fatal(err)
	}
	newer, err := s.Run(ctx, RunOptions{Password: []byte("archive secret"), Compression: "gzip", Output: filepath.Join(t.TempDir(), "newer.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	if err = peer.CacheCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if ahead, err := peer.DownloadedCatalogAhead(ctx); err != nil || !ahead {
		t.Fatal("newly downloaded state was mistaken for unlocked state", err)
	}
	condition, err := peer.Condition(ctx)
	if err != nil || condition.State != STATE_STALE {
		t.Fatal("stale local catalog was reported current", err)
	}
	peer.Stores = nil
	if err = peer.UnlockCached(ctx); err != nil {
		t.Fatal("newer downloaded state could not be unlocked offline", err)
	}
	updated, err := peer.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := Resolve(ctx, updated, "latest")
	updated.Close()
	if err != nil || latest.ID != newer.ID {
		t.Fatal("latest snapshot remained stale after unlock", err)
	}
	third := freshStateService(t, s)
	third.Stores = map[string]cloud.Store{"replica": replica}
	third.Kinds = map[string]string{"replica": "b2"}
	// the replica identity is irrelevant when it contains only directory entries
	if err = third.DiscoverState(ctx, "replica"); err != nil {
		t.Fatal(err)
	}
	if _, err = third.Registry.Set(ctx, s.Set.ID); err != nil {
		t.Fatal("replica did not locate the authoritative catalog", err)
	}
}

func TestLegacyStateMigrationAndUnknownVersions(t *testing.T) {
	ctx := context.Background()
	s, store, snapshot := importManagedFixture(t)
	payload := append([]byte(nil), store.files["legacy.gpg"]...)
	payloadWrites := store.puts["legacy.gpg"]
	s.Stores[s.Set.CatalogTo] = &stateOnlyStore{memoryStore: store, payload: "legacy.gpg"}
	c, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	legacy := revision{Version: model.StateVersionLegacy, Set: s.Set.ID, ID: id, Number: 1, Checkpoint: true, Key: s.prefix() + "batches/" + id + ".db.gz"}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetMeta(ctx, "publication", string(b)); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err = s.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	revisions, err := s.revisions(ctx, store)
	if err != nil || len(revisions) != 2 || revisions[0].Version != model.StateVersionLegacy || revisions[1].Version != model.StateVersionPortable || !revisions[1].Checkpoint {
		t.Fatal("migration did not preserve legacy history and create a portable checkpoint", err)
	}
	if err = s.SyncCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := s.revisions(ctx, store)
	if err != nil || len(again) != len(revisions) {
		t.Fatal("unchanged sync republished state", err)
	}
	if !bytes.Equal(payload, store.files["legacy.gpg"]) || payloadWrites != store.puts["legacy.gpg"] {
		t.Fatal("migration rewrote archive data")
	}
	if err = s.Restore(ctx, RestoreOptions{ID: snapshot.ID, Output: filepath.Join(t.TempDir(), "restored"), Password: func(model.Snapshot) ([]byte, error) { return []byte("encb fixture password"), nil }}); err != nil {
		t.Fatal("historical archive could not be restored after state migration", err)
	}
	bad := model.Locator{Version: model.DiscoveryVersion(99), Set: s.Set.ID}
	if err = s.rememberLocator(ctx, bad); err == nil {
		t.Fatal("unknown directory version accepted")
	}
}

func TestAuthorityConflictsRequireSelection(t *testing.T) {
	ctx := context.Background()
	s, _, _ := setupService(t)
	if err := s.SyncCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	other := model.Remote{Name: "other", Kind: "b2", Bucket: "other"}
	other.ID = other.Identity()
	other.Name = other.ID
	claim := model.Locator{Version: model.DiscoveryVersionInitial, Set: s.Set.ID, Authority: other, Prefix: s.Config.Catalog.Prefix}
	if err := s.rememberLocator(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncCatalog(ctx); !errors.Is(err, ErrConflict) {
		t.Fatal("authority conflict did not reject synchronization", err)
	}
	if err := s.ResolveAuthority(ctx, s.Set.CatalogTo); err != nil {
		t.Fatal(err)
	}
	selected, err := s.Locator(ctx, s.Set.ID)
	if err != nil || selected.Authority.ID != s.Set.CatalogTo {
		t.Fatal("explicit authority choice did not resolve the conflict", err)
	}
}

func TestAnotherComputerFollowsMovedCatalogAuthority(t *testing.T) {
	ctx := context.Background()
	s, _, source := setupService(t)
	if err := os.WriteFile(filepath.Join(source, "song.txt"), []byte("music"), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := s.Run(ctx, RunOptions{Password: []byte("archive secret"), Compression: "gzip", Output: filepath.Join(t.TempDir(), "full.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SyncCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	peer := freshStateService(t, s)
	root := peer.Directory
	oldAuthority := s.Set.CatalogTo
	if err = peer.DiscoverState(ctx, oldAuthority); err != nil {
		t.Fatal(err)
	}
	if err = peer.OpenSet(ctx, s.Set.ID, root); err != nil {
		t.Fatal(err)
	}
	if err = peer.CacheCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if err = peer.UnlockCached(ctx); err != nil {
		t.Fatal(err)
	}
	stale := freshStateService(t, s)
	staleRoot := stale.Directory
	if err = stale.DiscoverState(ctx, oldAuthority); err != nil {
		t.Fatal(err)
	}
	if err = stale.OpenSet(ctx, s.Set.ID, staleRoot); err != nil {
		t.Fatal(err)
	}
	if err = stale.CacheCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if err = stale.UnlockCached(ctx); err != nil {
		t.Fatal(err)
	}
	target, err := s.Registry.BindRemote(ctx, model.Remote{Name: "target", Kind: "b2", Bucket: "target"})
	if err != nil {
		t.Fatal(err)
	}
	s.Stores[target.ID] = newStore()
	s.Kinds[target.ID] = "b2"
	if err = s.Move(ctx, target.ID, RetrievalOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = stale.Push(ctx); !errors.Is(err, ErrConflict) {
		t.Fatal("a stale writer published into the retired authority", err)
	}
	if err = s.PublishDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	if err = peer.DiscoverState(ctx, oldAuthority); err != nil {
		t.Fatal(err)
	}
	if err = peer.OpenSet(ctx, s.Set.ID, root); err != nil {
		t.Fatal(err)
	}
	if err = peer.CacheCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if ahead, err := peer.DownloadedCatalogAhead(ctx); err != nil || !ahead {
		t.Fatal("authority move was mistaken for an old cached revision", err)
	}
	peer.Stores = nil
	if err = peer.UnlockCached(ctx); err != nil {
		t.Fatal("moved authority could not be followed", err)
	}
	if peer.Set.CatalogTo != target.ID {
		t.Fatal("catalog still points at old authority")
	}
	c, err := peer.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	recovered, err := c.Snapshot(ctx, v.ID)
	if err != nil || recovered.Hash != v.Hash {
		t.Fatal("authority move changed archive identity", err)
	}
}
