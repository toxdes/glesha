package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"glesha/cloud"
	"glesha/database/model"
)

type retrievalStore struct {
	*memoryStore
	heads, downloads int
	versions         []string
}

func (s *retrievalStore) Head(ctx context.Context, key, version string) (cloud.Object, error) {
	s.heads++
	s.versions = append(s.versions, version)
	return s.memoryStore.Head(ctx, key, version)
}

func (s *retrievalStore) Get(context.Context, string, string) (io.ReadCloser, cloud.Object, error) {
	s.downloads++
	return nil, cloud.Object{}, errors.New("unexpected download")
}

func TestImportRetrievalStatusUsesMetadataAndCachesReadiness(t *testing.T) {
	ctx := context.Background()
	s, original, _ := setupService(t)
	remote := s.Set.To[0]
	store := &retrievalStore{memoryStore: original}
	s.Stores[remote], s.Kinds[remote] = store, "aws"
	key := "encb-existing.tar.gz.gpg"
	fixture := filepath.Join("..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg")
	object, err := original.Put(ctx, key, fixture, "DEEP_ARCHIVE")
	if err != nil {
		t.Fatal(err)
	}
	object.Cold = true
	original.objects[key] = object
	if _, _, _, err = s.PrepareImport(ctx, []string{remote}, key, RetrievalOptions{Request: true}); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	if original.puts["retrieval-days:"+key] != 3 {
		t.Fatal("new retrieval did not use three days")
	}
	store.heads = 0
	rows, err := s.Retrievals(ctx, false)
	if err != nil || len(rows) != 1 || rows[0].State != RETRIEVAL_QUEUED || rows[0].Waiting != 1 || store.heads != 0 {
		t.Fatal("cached queued import missing:", rows, err)
	}
	expires := now().Add(72 * time.Hour).Truncate(time.Second)
	object.Cold = false
	object.Restore = `ongoing-request="false", expiry-date="` + expires.Format(http.TimeFormat) + `"`
	original.objects[key] = object
	rows, err = s.Retrievals(ctx, true)
	if err != nil || len(rows) != 1 || rows[0].State != RETRIEVAL_READY || rows[0].Ready != 1 || rows[0].Waiting != 0 || rows[0].ExpiresAt == nil || !rows[0].ExpiresAt.Equal(expires) {
		t.Fatal("ready import missing:", rows, err)
	}
	if store.heads != 1 || store.downloads != 0 || original.puts["retrieval:"+key] != 1 || store.versions[len(store.versions)-1] != object.Version {
		t.Fatal("status did more than a pinned HEAD")
	}
	store.heads = 0
	s.Stores = nil
	rows, err = s.Retrievals(ctx, false)
	if err != nil || rows[0].State != RETRIEVAL_READY || rows[0].CheckedAt == nil || store.heads != 0 {
		t.Fatal("offline cache lost readiness:", err)
	}
	cacheFile := filepath.Join(s.Directory, rows[0].ID+".retrieval-status")
	beforeMismatch, _ := os.ReadFile(cacheFile)
	changed := object
	changed.Version = "unexpected-version"
	original.objects[key] = changed
	s.Stores = map[string]cloud.Store{remote: store}
	if _, err = s.Retrievals(ctx, true); err == nil {
		t.Fatal("changed pinned version accepted")
	}
	afterMismatch, _ := os.ReadFile(cacheFile)
	if string(beforeMismatch) != string(afterMismatch) {
		t.Fatal("failed refresh overwrote cached observations")
	}
	original.objects[key] = object
	s.Stores = nil
	data, _ := os.ReadFile(cacheFile)
	var cache retrievalCache
	if err = json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}
	cache.Objects[0].Object.Restore = `ongoing-request="false", expiry-date="` + now().Add(-time.Hour).Format(http.TimeFormat) + `"`
	if err = s.saveJob(cacheFile, cache); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Retrievals(ctx, false)
	if err != nil || rows[0].State != RETRIEVAL_EXPIRED || rows[0].Ready != 0 || rows[0].Waiting != 1 {
		t.Fatal("expired cached copy reported ready:", rows, err)
	}
	cache.Version = 99
	s.saveJob(cacheFile, cache)
	before, _ := os.ReadFile(cacheFile)
	if _, err = s.Retrievals(ctx, true); err == nil {
		t.Fatal("unknown cache version accepted")
	}
	after, _ := os.ReadFile(cacheFile)
	if string(before) != string(after) {
		t.Fatal("unknown cache modified")
	}
}

func TestRetrievalAggregation(t *testing.T) {
	checked := now().Add(-time.Hour)
	expiry := now().Add(48 * time.Hour)
	later := expiry.Add(time.Hour)
	ready := RetrievalObject{State: RETRIEVAL_READY, CheckedAt: &checked, ExpiresAt: &expiry}
	for _, tc := range []struct {
		name      string
		objects   []RetrievalObject
		state     RetrievalState
		ready     int
		waiting   int
		expiry    bool
		checkedAt bool
	}{
		{"ready", []RetrievalObject{ready, {State: RETRIEVAL_READY, CheckedAt: &expiry, ExpiresAt: &later}}, RETRIEVAL_READY, 2, 0, true, true},
		{"partial", []RetrievalObject{ready, {State: RETRIEVAL_QUEUED, CheckedAt: &checked}}, RETRIEVAL_PARTIAL, 1, 1, false, true},
		{"queued", []RetrievalObject{{State: RETRIEVAL_QUEUED}}, RETRIEVAL_QUEUED, 0, 1, false, false},
		{"expired", []RetrievalObject{ready, {State: RETRIEVAL_EXPIRED}}, RETRIEVAL_EXPIRED, 1, 1, false, false},
		{"unknown expiry", []RetrievalObject{{State: RETRIEVAL_READY, CheckedAt: &checked}}, RETRIEVAL_READY, 1, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeRetrieval(retrievalCache{Objects: tc.objects})
			if got.State != tc.state || got.Ready != tc.ready || got.Waiting != tc.waiting || (got.ExpiresAt != nil) != tc.expiry || (got.CheckedAt != nil) != tc.checkedAt {
				t.Fatalf("unexpected summary: %+v", got)
			}
			if got.ExpiresAt != nil && !got.ExpiresAt.Equal(expiry) || got.CheckedAt != nil && !got.CheckedAt.Equal(checked) {
				t.Fatal("summary did not use earliest timestamps")
			}
		})
	}
}

func TestRestoreRetrievalHistoryRetainsReadyArchives(t *testing.T) {
	s, _, _ := setupService(t)
	checked := now()
	ready := retrievalItem{Requested: true, Location: model.Location{Provider: "aws", Key: "first", Version: "v1", Restore: `ongoing-request="false"`, ObservedAt: &checked}}
	waiting := retrievalItem{Requested: true, Location: model.Location{Provider: "aws", Key: "second", Version: "v2", Restore: `ongoing-request="true"`, ObservedAt: &checked}}
	job := restoreJob{Retrievals: []retrievalItem{ready, waiting}, Requests: []retrievalItem{waiting}}
	if err := s.saveJob(filepath.Join(s.Directory, "restore-example.json"), job); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Retrievals(context.Background(), false)
	if err != nil || len(rows) != 1 || rows[0].State != RETRIEVAL_PARTIAL || rows[0].Ready != 1 || rows[0].Waiting != 1 {
		t.Fatal("ready history lost or double-counted:", rows, err)
	}
}

func TestSavedImportDurationSurvivesDefaultChange(t *testing.T) {
	s, store, _ := setupService(t)
	remote := s.Set.To[0]
	s.Kinds[remote] = "aws"
	store.objects["old"] = cloud.Object{Key: "old", Version: "pinned", Cold: true, Size: 1, Class: "DEEP_ARCHIVE"}
	ctx := context.Background()
	if _, _, _, err := s.PrepareImport(ctx, []string{remote}, "old", RetrievalOptions{Days: 7, Request: true}); !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	for _, options := range []RetrievalOptions{{}, {Days: 3, UseSavedDays: true}} {
		if _, _, _, err := s.PrepareImport(ctx, []string{remote}, "old", options); !errors.Is(err, ErrPending) {
			t.Fatal("changed default broke pending import:", err)
		}
	}
	if _, _, _, err := s.PrepareImport(ctx, []string{remote}, "old", RetrievalOptions{Days: 3}); err == nil || errors.Is(err, ErrPending) {
		t.Fatal("explicit conflicting duration accepted:", err)
	}
	if store.puts["retrieval-days:old"] != 7 || store.puts["retrieval:old"] != 1 {
		t.Fatal("pending request duration changed or request repeated")
	}
}

func TestCompletedImportsLeaveRetrievalStatus(t *testing.T) {
	s, _, _ := setupService(t)
	file := filepath.Join(s.Directory, "import-completed.gpg")
	if err := s.saveJob(file+".json", importJob{Requested: true, Remote: s.Set.To[0], Object: cloud.Object{Key: "old", Version: "pinned", Restore: `ongoing-request="false"`}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.PutSnapshot(ctx, model.Snapshot{Manifest: model.Manifest{ID: "imported", Version: 1}, Status: model.STATUS_COMPLETED, File: file}); err != nil {
		t.Fatal(err)
	}
	c.Close()
	rows, err := s.Retrievals(ctx, false)
	if err != nil || len(rows) != 0 {
		t.Fatal("completed import retained in retrieval table:", rows, err)
	}
}
