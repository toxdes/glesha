package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"glesha/internal/cloud"
	"glesha/internal/database/model"
)

func (m *memoryStore) ListVersions(_ context.Context, prefix string, each func(cloud.Object) error) error {
	m.mu.Lock()
	var found []cloud.Object
	for key, o := range m.objects {
		if strings.HasPrefix(key, prefix) {
			found = append(found, o)
		}
	}
	m.mu.Unlock()
	for _, o := range found {
		if err := each(o); err != nil {
			return err
		}
	}
	return nil
}

func (m *memoryStore) DeleteVersion(_ context.Context, key, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	delete(m.files, key)
	return nil
}

func (m *memoryStore) keys(prefix string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for key := range m.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys
}

// trashedFixture backs up one file, publishes the set's discovery records
// and returns the service and its store.
func trashedFixture(t *testing.T) (*Service, *memoryStore) {
	t.Helper()
	ctx := context.Background()
	s, store, source := setupService(t)
	if err := os.WriteFile(filepath.Join(source, "a.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(ctx, RunOptions{Password: []byte("pw"), Compression: "gzip", Output: filepath.Join(t.TempDir(), "full.gpg")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.keys(archivesPrefix+s.Set.ID+"/")) == 0 || len(store.keys(s.directoryPrefix()+s.Set.ID+"/")) == 0 {
		t.Fatalf("fixture uploaded no archive or discovery record: %v", store.keys(""))
	}
	return s, store
}

func TestUndeleteRestoresTrashedSet(t *testing.T) {
	ctx := context.Background()
	s, store := trashedFixture(t)
	before := len(store.keys(""))
	if _, err := s.Trash(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Registry.Set(ctx, s.Set.ID)
	if err != nil || saved.Trash == nil {
		t.Fatalf("trash not saved locally: %v", err)
	}
	if len(store.keys(s.trashKey())) != 1 || len(store.keys("")) != before+1 {
		t.Fatal("trash must add one marker and change no data")
	}
	if err = s.Undelete(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.keys("")) != before {
		t.Fatal("undelete left the trash marker")
	}
	if saved, _ = s.Registry.Set(ctx, s.Set.ID); saved.Trash != nil {
		t.Fatal("undelete kept the local trash state")
	}
}

func TestTrashAgainKeepsDeadline(t *testing.T) {
	ctx := context.Background()
	s, _ := trashedFixture(t)
	first, err := s.Trash(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Trash(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Deadline.Equal(first.Deadline) {
		t.Fatalf("deadline moved from %v to %v", first.Deadline, again.Deadline)
	}
}

func TestUndeleteAfterDeadlineIsRefused(t *testing.T) {
	ctx := context.Background()
	s, _ := trashedFixture(t)
	if _, err := s.Trash(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Undelete(ctx); !errors.Is(err, ErrDeletionStarted) {
		t.Fatalf("expected ErrDeletionStarted, got %v", err)
	}
}

func TestPurgeRemovesEverythingAndStaysDeleted(t *testing.T) {
	ctx := context.Background()
	s, store := trashedFixture(t)
	// a device that predates deletion markers republishes this record later
	locators := store.keys(s.directoryPrefix() + s.Set.ID + "/")
	stale := map[string][]byte{}
	for _, key := range locators {
		stale[key] = append([]byte{}, store.files[key]...)
	}
	if _, err := s.Trash(ctx, 0); err != nil {
		t.Fatal(err)
	}
	result, err := s.Purge(ctx, []string{s.Set.CatalogTo}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Done || result.Freed.Objects == 0 || result.Freed.Bytes == 0 {
		t.Fatalf("purge result %+v", result)
	}
	if left := store.keys(""); len(left) != 1 || left[0] != s.deletedKey() {
		t.Fatalf("objects left after purge: %v", left)
	}
	if _, err = s.Registry.Set(ctx, s.Set.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("set still registered: %v", err)
	}
	if _, err = os.Stat(s.Directory); !os.IsNotExist(err) {
		t.Fatal("local state kept")
	}

	for key, data := range stale {
		file := filepath.Join(t.TempDir(), "locator.json")
		if err = os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = store.Put(ctx, key, file, "STANDARD"); err != nil {
			t.Fatal(err)
		}
	}
	peer := freshStateService(t, s)
	if err = peer.DiscoverState(ctx, s.Set.CatalogTo); err != nil {
		t.Fatal(err)
	}
	if _, err = peer.Registry.Set(ctx, s.Set.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted set discovered again: %v", err)
	}
	if left := store.keys(s.directoryPrefix()); len(left) != 0 {
		t.Fatalf("republished discovery record kept: %v", left)
	}
}

func TestPurgeWaitsForEveryRemote(t *testing.T) {
	ctx := context.Background()
	s, _ := trashedFixture(t)
	if _, err := s.Trash(ctx, 0); err != nil {
		t.Fatal(err)
	}
	result, err := s.Purge(ctx, []string{s.Set.CatalogTo, "unreachable"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Done {
		t.Fatal("purge finished without the unreachable remote")
	}
	if _, err = s.Registry.Set(ctx, s.Set.ID); err != nil {
		t.Fatal("set forgotten before every remote was clean")
	}
}

func TestPartialPurgeCannotBeUndone(t *testing.T) {
	ctx := context.Background()
	s, _ := trashedFixture(t)
	if _, err := s.Trash(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Purge(ctx, []string{s.Set.CatalogTo, "unreachable"}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Undelete(ctx); !errors.Is(err, ErrDeletionStarted) {
		t.Fatalf("undelete of a partly deleted set: %v", err)
	}
	s.Set.Trash.Started = false
	if err := s.Undelete(ctx); !errors.Is(err, ErrDeletionStarted) {
		t.Fatalf("undelete ignored the deleted marker on the remote: %v", err)
	}
}

func TestPurgeWaitsForTheBucketDeadline(t *testing.T) {
	ctx := context.Background()
	s, store := trashedFixture(t)
	if _, err := s.Trash(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	before := len(store.keys(""))
	// a local clock that runs ahead
	s.Set.Trash.Deadline = time.Now().Add(-time.Minute)
	if _, err := s.Purge(ctx, []string{s.Set.CatalogTo}, false); !errors.Is(err, ErrNotDue) {
		t.Fatalf("purge before the bucket deadline: %v", err)
	}
	if len(store.keys("")) != before || s.Set.Trash.Due(time.Now()) {
		t.Fatal("purge deleted data or kept the wrong deadline")
	}
}

func TestPurgeAfterUndeleteElsewhereDeletesNothing(t *testing.T) {
	ctx := context.Background()
	s, store := trashedFixture(t)
	if _, err := s.Trash(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	// another device removed the marker; this one still has a due deadline
	store.DeleteVersion(ctx, s.trashKey(), "")
	before := len(store.keys(""))
	s.Set.Trash.Deadline = time.Now().Add(-time.Minute)
	if _, err := s.Purge(ctx, []string{s.Set.CatalogTo}, false); !errors.Is(err, ErrNotInTrash) {
		t.Fatalf("purge of a restored set: %v", err)
	}
	if len(store.keys("")) != before || s.Set.Trash != nil {
		t.Fatal("purge of a restored set deleted data or kept it in trash")
	}
}

func TestDiscoveryKeepsUnfinishedDeletion(t *testing.T) {
	ctx := context.Background()
	s, _ := trashedFixture(t)
	peer := freshStateService(t, s)
	if err := peer.DiscoverState(ctx, s.Set.CatalogTo); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Trash(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Purge(ctx, []string{s.Set.CatalogTo, "unreachable"}, true); err != nil {
		t.Fatal(err)
	}
	if err := peer.DiscoverState(ctx, s.Set.CatalogTo); err != nil {
		t.Fatal(err)
	}
	seen, err := peer.Registry.Set(ctx, s.Set.ID)
	if err != nil || seen.Trash == nil || !seen.Trash.Started {
		t.Fatalf("second device forgot a deletion that has not finished: %+v %v", seen.Trash, err)
	}
}

func TestDiscoveryAppliesTrashAndUndelete(t *testing.T) {
	ctx := context.Background()
	s, _ := trashedFixture(t)
	peer := freshStateService(t, s)
	if _, err := s.Trash(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := peer.DiscoverState(ctx, s.Set.CatalogTo); err != nil {
		t.Fatal(err)
	}
	seen, err := peer.Registry.Set(ctx, s.Set.ID)
	if err != nil || seen.Trash == nil {
		t.Fatalf("second device does not see the trash: %v", err)
	}
	if err = s.Undelete(ctx); err != nil {
		t.Fatal(err)
	}
	if err = peer.DiscoverState(ctx, s.Set.CatalogTo); err != nil {
		t.Fatal(err)
	}
	if seen, _ = peer.Registry.Set(ctx, s.Set.ID); seen.Trash != nil {
		t.Fatal("second device kept the trash after undelete")
	}
}

func TestPurgeKeepsImportedArchiveSharedWithAnotherSet(t *testing.T) {
	ctx := context.Background()
	s, store, v := importManagedFixture(t)
	imported, err := s.externalObjects(ctx)
	if err != nil || len(imported) != 1 || imported[0].Key != "legacy.gpg" {
		t.Fatalf("imported archive not found: %v %v", imported, err)
	}
	other, err := NewSet("photos", nil, s.Config)
	if err != nil {
		t.Fatal(err)
	}
	other.CatalogTo = "local"
	if err = s.Registry.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	peer := &Service{Config: s.Config, Registry: s.Registry, Stores: s.Stores}
	if err = peer.OpenSet(ctx, "photos", filepath.Dir(s.Directory)); err != nil {
		t.Fatal(err)
	}
	c, err := peer.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := model.Snapshot{Manifest: model.Manifest{Version: 1, ID: "photos-snapshot", Set: other.ID, Full: true}, Status: model.STATUS_COMPLETED}
	if err = c.PutSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = c.PutLocation(ctx, model.Location{Snapshot: snapshot.ID, Provider: imported[0].Remote, Key: imported[0].Key, Version: imported[0].Version, Status: model.STATUS_COMPLETED}); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if counts, err := s.SharedArchives(ctx); err != nil || counts["photos"] != 1 {
		t.Fatalf("shared archives %v: %v", counts, err)
	}

	if _, err = s.Trash(ctx, 0); err != nil {
		t.Fatal(err)
	}
	result, err := s.Purge(ctx, []string{s.Set.CatalogTo}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Kept) != 1 || result.Kept[0].Reason != "set photos uses it" {
		t.Fatalf("shared archive not reported as kept: %+v", result.Kept)
	}
	if len(store.keys("legacy.gpg")) != 1 {
		t.Fatalf("shared archive of snapshot %s deleted", v.ID)
	}
}

// trashedImport imports legacy.gpg, puts the set in the trash, and returns
// the service, its store and the imported reference.
func trashedImport(t *testing.T) (*Service, *memoryStore, model.ObjectRef) {
	t.Helper()
	s, store, _ := importManagedFixture(t)
	trash, err := s.Trash(context.Background(), 0)
	if err != nil || len(trash.Imported) != 1 {
		t.Fatalf("imported archive not in the trash marker: %+v %v", trash, err)
	}
	return s, store, trash.Imported[0]
}

func TestPurgeKeepsReplacedUnversionedImport(t *testing.T) {
	ctx := context.Background()
	s, store, ref := trashedImport(t)
	if ref.ETag == "" {
		t.Fatal("trash marker has no ETag for the import")
	}
	s.Set.Trash.Imported[0].Version = ""
	replaced := store.objects["legacy.gpg"]
	replaced.ETag = "another file"
	store.objects["legacy.gpg"] = replaced
	result, err := s.Purge(ctx, []string{s.Set.CatalogTo}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.keys("legacy.gpg")) != 1 || len(result.Kept) != 1 || result.Kept[0].Reason != "it changed after it was imported" {
		t.Fatalf("replaced object deleted or not reported: %+v", result.Kept)
	}
}

func TestPurgeKeepsImportWhileAnotherCatalogIsMissing(t *testing.T) {
	ctx := context.Background()
	s, store, ref := trashedImport(t)
	other, err := NewSet("photos", nil, s.Config)
	if err != nil {
		t.Fatal(err)
	}
	other.CatalogTo = s.Set.CatalogTo
	if err = s.Registry.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err = s.checkImportUnused(ctx, ref.Remote, "new.gpg", ""); err != nil {
		t.Fatalf("a set without a published catalog blocked the import: %v", err)
	}
	if err = s.Registry.PutLocator(ctx, other.ID+"-locator", model.Locator{Version: 1, Set: other.ID, Prefix: s.Config.Catalog.Prefix}); err != nil {
		t.Fatal(err)
	}
	if err = s.checkImportUnused(ctx, ref.Remote, "new.gpg", ""); err == nil || !strings.Contains(err.Error(), "cannot check whether set photos uses new.gpg") {
		t.Fatalf("import checked without the catalog of photos: %v", err)
	}
	result, err := s.Purge(ctx, []string{s.Set.CatalogTo}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.keys("legacy.gpg")) != 1 || len(result.Kept) != 1 || !strings.Contains(result.Kept[0].Reason, "catalog of set photos") {
		t.Fatalf("import deleted while another catalog was missing: %+v", result.Kept)
	}
}

func TestPurgeKeepsOwnArchiveAnotherSetImported(t *testing.T) {
	ctx := context.Background()
	s, store := trashedFixture(t)
	own, err := s.objectRefs(ctx)
	if err != nil || len(own) == 0 {
		t.Fatalf("no archives: %v", err)
	}
	other, err := NewSet("photos", nil, s.Config)
	if err != nil {
		t.Fatal(err)
	}
	other.CatalogTo = "local"
	if err = s.Registry.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	peer := &Service{Config: s.Config, Registry: s.Registry, Stores: s.Stores}
	if err = peer.OpenSet(ctx, "photos", filepath.Dir(s.Directory)); err != nil {
		t.Fatal(err)
	}
	c, err := peer.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := model.Snapshot{Manifest: model.Manifest{Version: 1, ID: "photos-snapshot", Set: other.ID, Full: true}, Status: model.STATUS_COMPLETED}
	if err = c.PutSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = c.PutLocation(ctx, model.Location{Snapshot: snapshot.ID, Provider: own[0].Remote, Key: own[0].Key, Version: own[0].Version, Status: model.STATUS_COMPLETED}); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err = s.Trash(ctx, 0); err != nil {
		t.Fatal(err)
	}
	sizes, err := s.DeletionSize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Purge(ctx, []string{s.Set.CatalogTo}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.keys(own[0].Key)) != 1 || len(result.Kept) != 1 {
		t.Fatalf("archive that photos uses was deleted: %+v", result.Kept)
	}
	if sizes[s.Set.CatalogTo].Objects != result.Freed.Objects {
		t.Fatalf("dry run counted %d objects, purge freed %d", sizes[s.Set.CatalogTo].Objects, result.Freed.Objects)
	}
}
