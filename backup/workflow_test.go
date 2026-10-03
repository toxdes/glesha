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
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/aws/smithy-go"

	"glesha/archive"
	"glesha/cloud"
	"glesha/config"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/memory"
)

type memoryStore struct {
	mu      sync.Mutex
	files   map[string][]byte
	objects map[string]cloud.Object
	puts    map[string]int
	fail    string
}

func newStore() *memoryStore {
	return &memoryStore{files: map[string][]byte{}, objects: map[string]cloud.Object{}, puts: map[string]int{}}
}

var errMissing = &smithy.GenericAPIError{Code: "NoSuchKey", Message: "missing object"}

func (m *memoryStore) Head(_ context.Context, key, version string) (cloud.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.objects[key]
	if !ok {
		return h, errMissing
	}
	return h, nil
}
func (m *memoryStore) Get(ctx context.Context, key, version string) (io.ReadCloser, cloud.Object, error) {
	h, err := m.Head(ctx, key, version)
	if err != nil {
		return nil, h, err
	}
	m.mu.Lock()
	b := append([]byte{}, m.files[key]...)
	m.mu.Unlock()
	return io.NopCloser(bytes.NewReader(b)), h, nil
}
func (m *memoryStore) Put(ctx context.Context, key, file, class string) (cloud.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.put(ctx, key, file, class)
}
func (m *memoryStore) put(ctx context.Context, key, file, class string) (cloud.Object, error) {
	if m.fail != "" && strings.Contains(key, m.fail) {
		return cloud.Object{}, fmt.Errorf("mock: interrupted")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return cloud.Object{}, err
	}
	h, size, err := archive.Hash(ctx, file)
	if err != nil {
		return cloud.Object{}, err
	}
	v := cloud.Object{Key: key, Version: "version", ETag: h, Hash: h, Size: size, Class: class}
	m.files[key] = b
	m.objects[key] = v
	m.puts[key]++
	return v, nil
}
func (m *memoryStore) List(_ context.Context, prefix, after string) ([]cloud.Object, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := []string{}
	for key := range m.objects {
		if strings.HasPrefix(key, prefix) && key > after {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := []cloud.Object{}
	for _, key := range keys {
		out = append(out, m.objects[key])
	}
	return out, "", nil
}

// the adapter reports missing keys using the same public S3 error contract
func (m *memoryStore) PutNew(ctx context.Context, key, file, class string) (cloud.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.objects[key]; exists {
		return cloud.Object{}, cloud.ErrObjectExists
	}
	return m.put(ctx, key, file, class)
}

func setupService(t *testing.T) (*Service, *memoryStore, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "docs")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Catalog.Encrypted = false
	budget, err := memory.Resolve("128MiB", 2, 2, func() (int64, error) { panic("probe") })
	if err != nil {
		t.Fatal(err)
	}
	registry, err := repository.NewRegistryRepository(ctx, filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })
	roots, err := archive.Roots([]string{source})
	if err != nil {
		t.Fatal(err)
	}
	set, err := NewSet("docs", roots, cfg)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := registry.BindRemote(ctx, model.Remote{Name: "b2", Kind: "b2", Bucket: "test"})
	if err != nil {
		t.Fatal(err)
	}
	set.To = []string{remote.ID}
	set.CatalogTo = remote.ID
	if err = registry.Create(ctx, set); err != nil {
		t.Fatal(err)
	}
	store := newStore()
	s := &Service{Config: cfg, Budget: budget, Registry: registry, Stores: map[string]cloud.Store{remote.ID: store}, Kinds: map[string]string{remote.ID: "b2"}}
	if err = s.OpenSet(ctx, "docs", filepath.Join(root, "state")); err != nil {
		t.Fatal(err)
	}
	return s, store, source
}
func TestFullIncrementalRestoreAndDeletionConsent(t *testing.T) {
	ctx := context.Background()
	s, _, source := setupService(t)
	pw := []byte("test password")
	file := filepath.Join(source, "A.txt")
	if err := os.WriteFile(file, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	full, err := s.Run(ctx, RunOptions{Password: pw, Compression: "bzip2", Output: filepath.Join(t.TempDir(), "full.tar.bz2.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	inc, err := s.Run(ctx, RunOptions{Incremental: true, Password: pw, Compression: "gzip", Output: filepath.Join(t.TempDir(), "inc.tar.gz.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	if inc.Parent != full.ID {
		t.Fatal("incorrect parent")
	}
	if full.Compression != "bzip2" || inc.Compression != "gzip" {
		t.Fatal("compression override ignored")
	}
	c, _ := s.Catalog(ctx)
	old, err := c.Entry(ctx, full.ID, "docs/A.txt")
	if err != nil {
		t.Fatal(err)
	}
	current, err := c.Entry(ctx, inc.ID, "docs/A.txt")
	c.Close()
	if err != nil || old.Hash == current.Hash {
		t.Fatal("history lost", err)
	}
	none, err := s.Run(ctx, RunOptions{Incremental: true, Password: pw})
	if err != nil || none.ID != "" {
		t.Fatal("no-change payload generated", err)
	}
	output := filepath.Join(t.TempDir(), "restore")
	if err = s.Restore(ctx, RestoreOptions{ID: inc.ID, Output: output, Password: func(model.Snapshot) ([]byte, error) { return bytes.Clone(pw), nil }}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(output, "docs", "A.txt"))
	if err != nil || string(got) != "second" {
		t.Fatal("restore content incorrect", err)
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	_, err = s.Run(ctx, RunOptions{Incremental: true, Password: pw, Output: filepath.Join(t.TempDir(), "denied.gpg")})
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatal("deletion lacked confirmation", err)
	}
	if s.Pending() {
		t.Fatal("declined run retained pending state")
	}
	deleted, err := s.Run(ctx, RunOptions{Incremental: true, Password: pw, Output: filepath.Join(t.TempDir(), "deleted.gpg"), Confirm: func(context.Context, string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Compression != "xz" || deleted.Parent != inc.ID {
		t.Fatal("default xz delta did not continue mixed-format chain")
	}
	output = filepath.Join(t.TempDir(), "deleted")
	if err = s.Restore(ctx, RestoreOptions{ID: deleted.ID, Output: output, Password: func(model.Snapshot) ([]byte, error) { return bytes.Clone(pw), nil }}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(output, "docs", "A.txt")); !os.IsNotExist(err) {
		t.Fatal("deleted file restored")
	}
}

func TestPublicationRetryAndFreshCatalogPull(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	s.Config.Catalog.Encrypted = true
	s.CatalogPassword = []byte("catalog password")
	os.WriteFile(filepath.Join(source, "f"), []byte("value"), 0600)
	store.fail = "heads/"
	v, err := s.Run(ctx, RunOptions{Password: []byte("archive password"), Output: filepath.Join(t.TempDir(), "full.gpg")})
	if err == nil || !s.Pending() {
		t.Fatal("failed publication lost pending work", err)
	}
	key := s.payloadKey(v)
	if store.puts[key] != 1 {
		t.Fatal("payload not uploaded exactly once")
	}
	store.fail = ""
	if err = s.Retry(ctx); err != nil {
		t.Fatal("retry failed", err)
	}
	if store.puts[key] != 1 {
		t.Fatal("retry repeated successful payload upload")
	}
	peer := *s
	peer.Directory = filepath.Join(t.TempDir(), s.Set.ID)
	os.Mkdir(peer.Directory, 0700)
	if err = peer.Pull(ctx); err != nil {
		t.Fatal("fresh pull failed", err)
	}
	c, err := peer.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got, err := c.Entry(ctx, v.ID, "docs/f")
	if err != nil || got.Size != 5 {
		t.Fatal("remote inventory missing", err)
	}
	committed, err := c.Latest(ctx)
	if err != nil || committed.Status != model.STATUS_COMPLETED {
		t.Fatal("retry regressed committed status", err)
	}
}

func TestIncrementalAuthorityAndMove(t *testing.T) {
	ctx := context.Background()
	s, _, source := setupService(t)
	pw := []byte("pw")
	os.WriteFile(filepath.Join(source, "f"), []byte("a"), 0600)
	_, err := s.Run(ctx, RunOptions{Password: pw, Output: filepath.Join(t.TempDir(), "one.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(source, "f"), []byte("b"), 0600)
	_, err = s.Run(ctx, RunOptions{Incremental: true, Password: pw, Output: filepath.Join(t.TempDir(), "two.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Registry.BindRemote(ctx, model.Remote{Name: "b2-other", Kind: "b2", Bucket: "other"})
	if err != nil {
		t.Fatal(err)
	}
	dest := newStore()
	s.Stores[r.ID] = dest
	s.Kinds[r.ID] = "b2"
	_, err = s.Run(ctx, RunOptions{To: []string{r.ID}, Password: pw})
	if err == nil {
		t.Fatal("incremental authority switched silently")
	}
	if err = s.Move(ctx, r.ID, RetrievalOptions{Tier: "bulk", Days: 7}); err != nil {
		t.Fatal(err)
	}
	if s.Set.Authority != r.ID {
		t.Fatal("authority not switched after verified move")
	}
	c, _ := s.Catalog(ctx)
	defer c.Close()
	if err = c.EachSnapshot(ctx, func(v model.Snapshot) error {
		ls, err := c.Locations(ctx, v.ID)
		if err != nil {
			return err
		}
		for _, l := range ls {
			if l.Provider == r.ID {
				return nil
			}
		}
		return fmt.Errorf("missing moved snapshot")
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyImportIsIdempotentAndArchiveUnchanged(t *testing.T) {
	ctx := context.Background()
	s, store, _ := setupService(t)
	s.Set.Roots = nil
	s.Set.To = nil
	s.Set.CatalogTo = "local"
	s.Registry.Save(ctx, s.Set)
	file := filepath.Join("..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg")
	before, size, err := archive.Hash(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	pw := []byte("encb fixture password")
	v, err := s.Import(ctx, ImportOptions{File: file, Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	if v.BackupDate != nil || !v.Imported || v.Compression != "gzip" {
		t.Fatal("legacy metadata incorrect")
	}
	again, err := s.Import(ctx, ImportOptions{File: file, Password: pw})
	if err != nil || again.ID != v.ID {
		t.Fatal("repeat import created another snapshot", err)
	}
	after, n, err := archive.Hash(ctx, file)
	if err != nil || after != before || n != size {
		t.Fatal("legacy archive modified")
	}
	if len(store.puts) != 0 {
		t.Fatal("import wrote cloud payload")
	}
	out := filepath.Join(t.TempDir(), "restored")
	if err = s.Restore(ctx, RestoreOptions{ID: v.ID, Output: out, Password: func(model.Snapshot) ([]byte, error) { return bytes.Clone(pw), nil }}); err != nil {
		t.Fatal(err)
	}
}

func TestColdImportWaitsAndResumesPinnedObject(t *testing.T) {
	ctx := context.Background()
	s, store, _ := setupService(t)
	remote := s.Set.To[0]
	s.Kinds[remote] = "aws"
	key := "encb-existing.tar.gz.gpg"
	fixture := filepath.Join("..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg")
	object, err := store.Put(ctx, key, fixture, "DEEP_ARCHIVE")
	if err != nil {
		t.Fatal(err)
	}
	object.Cold = true
	store.objects[key] = object
	options := RetrievalOptions{Tier: "bulk", Days: 7}
	if _, _, _, err = s.PrepareImport(ctx, []string{remote}, key, options); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatal("cold import skipped confirmation", err)
	}
	options.Request = true
	for i := 0; i < 2; i++ {
		if _, _, _, err = s.PrepareImport(ctx, []string{remote}, key, options); !errors.Is(err, ErrPending) {
			t.Fatal("cold import did not wait", err)
		}
	}
	if store.puts["retrieval:"+key] != 1 {
		t.Fatal("cold import repeated retrieval request")
	}
	object.Cold, object.Restore = false, `ongoing-request="false"`
	store.objects[key] = object
	file, gotRemote, gotObject, err := s.PrepareImport(ctx, []string{remote}, key, options)
	if err != nil || gotRemote != remote || gotObject.Version != object.Version {
		t.Fatal("import did not resume pinned object", err)
	}
	before, size, err := archive.Hash(ctx, fixture)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, gotSize, err := archive.Hash(ctx, file)
	if err != nil || gotHash != before || gotSize != size {
		t.Fatal("resumed import changed encrypted bytes", err)
	}
	if _, _, _, err = s.PrepareImport(ctx, []string{remote}, key, options); err != nil {
		t.Fatal("cached import could not resume", err)
	}
	if store.puts[key] != 1 || store.puts["retrieval:"+key] != 1 {
		t.Fatal("import rewrote payload or repeated retrieval")
	}
}

func (m *memoryStore) RestoreVersion(_ context.Context, key, version string, days int32, tier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.objects[key]
	if h.Version != version || tier != "Bulk" || days != 7 {
		return fmt.Errorf("mock: retrieval not pinned/cheapest")
	}
	h.Restore = `ongoing-request="true"`
	m.objects[key] = h
	m.puts["retrieval:"+key]++
	return nil
}
func (m *memoryStore) CopyClass(_ context.Context, key, version, class string) (cloud.Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.objects[key]
	if version != h.Version {
		return h, fmt.Errorf("mock: copy source not pinned")
	}
	h.Version += "-copy"
	h.Class = class
	m.objects[key] = h
	m.puts["copy:"+key]++
	return h, nil
}
func TestColdConsentPinnedResumeAndExternalLifecycle(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	for id := range s.Kinds {
		s.Kinds[id] = "aws"
	}
	pw := []byte("password")
	os.WriteFile(filepath.Join(source, "f"), []byte("value"), 0600)
	v, err := s.Run(ctx, RunOptions{Password: pw, Output: filepath.Join(t.TempDir(), "full.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	key := s.payloadKey(v)
	h := store.objects[key]
	h.Class = "DEEP_ARCHIVE"
	h.Cold = true
	store.objects[key] = h
	os.Remove(v.File)
	out := filepath.Join(t.TempDir(), "restore")
	options := RetrievalOptions{Tier: "bulk", Days: 7}
	_, _, err = s.PrepareRestore(ctx, "latest", out, s.Set.To, options)
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatal("retrieval lacked cost consent", err)
	}
	if store.puts["retrieval:"+key] != 0 {
		t.Fatal("implicit charged request")
	}
	options.Request = true
	id, _, err := s.PrepareRestore(ctx, "latest", out, s.Set.To, options)
	if id != v.ID || !errors.Is(err, ErrPending) {
		t.Fatal("retrieval not pending", err)
	}
	_, _, err = s.PrepareRestore(ctx, "latest", out, s.Set.To, options)
	if !errors.Is(err, ErrPending) || store.puts["retrieval:"+key] != 1 {
		t.Fatal("retrieval repeated", err)
	}
	if err = s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.History(ctx, func(_ model.Snapshot, ls []model.Location) error {
		if !ls[0].Cold || ls[0].ObservedClass != "DEEP_ARCHIVE" || ls[0].InitialClass != "STANDARD" {
			t.Fatal("external lifecycle overwrote initial class")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h = store.objects[key]
	h.Cold = false
	h.Restore = `ongoing-request="false"`
	store.objects[key] = h
	id, _, err = s.PrepareRestore(ctx, "latest", out, s.Set.To, options)
	if err != nil || id != v.ID {
		t.Fatal("readable job did not resume pinned snapshot", err)
	}
	if err = s.Restore(ctx, RestoreOptions{ID: id, Output: out, Password: func(model.Snapshot) ([]byte, error) { return bytes.Clone(pw), nil }}); err != nil {
		t.Fatal(err)
	}
}
func TestStorageClassKeepsIdentityAndResumesPublication(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	for id := range s.Kinds {
		s.Kinds[id] = "aws"
	}
	os.WriteFile(filepath.Join(source, "f"), []byte("value"), 0600)
	v, err := s.Run(ctx, RunOptions{Password: []byte("pw"), Output: filepath.Join(t.TempDir(), "full.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	key := s.payloadKey(v)
	original := bytes.Clone(store.files[key])
	store.fail = "heads/"
	if err = s.StorageClass(ctx, v.ID, "STANDARD_IA"); err == nil {
		t.Fatal("publication failure ignored")
	}
	if store.puts["copy:"+key] != 1 {
		t.Fatal("class not copied")
	}
	store.fail = ""
	if err = s.Retry(ctx); err != nil {
		t.Fatal(err)
	}
	if store.puts["copy:"+key] != 1 {
		t.Fatal("retry repeated class copy")
	}
	if !bytes.Equal(original, store.files[key]) {
		t.Fatal("class change modified archive")
	}
	c, _ := s.Catalog(ctx)
	defer c.Close()
	ls, err := c.Locations(ctx, v.ID)
	if err != nil || ls[0].InitialClass != "STANDARD" || ls[0].CurrentClass != "STANDARD_IA" {
		t.Fatal("class identity not retained", err)
	}
}

func TestReconciliationPreservesAdvancedRemoteAndPendingBranch(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	pw := []byte("pw")
	os.WriteFile(filepath.Join(source, "f"), []byte("baseline"), 0600)
	base, err := s.Run(ctx, RunOptions{Password: pw, Output: filepath.Join(t.TempDir(), "baseline.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	peer := *s
	peer.Directory = filepath.Join(t.TempDir(), s.Set.ID)
	os.Mkdir(peer.Directory, 0700)
	if err = peer.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	store.fail = "heads/"
	os.WriteFile(filepath.Join(source, "f"), []byte("branch one"), 0600)
	local, err := s.Run(ctx, RunOptions{Password: pw, Output: filepath.Join(t.TempDir(), "local.gpg")})
	if err == nil {
		t.Fatal("expected interrupted publication")
	}
	store.fail = ""
	os.WriteFile(filepath.Join(source, "f"), []byte("branch two"), 0600)
	remote, err := peer.Run(ctx, RunOptions{Password: pw, Output: filepath.Join(t.TempDir(), "remote.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Retry(ctx); !errors.Is(err, ErrConflict) {
		t.Fatal("remote changes overwritten", err)
	}
	if err = s.Reconcile(ctx); err != nil {
		t.Fatal("reconciliation failed", err)
	}
	c, _ := s.Catalog(ctx)
	defer c.Close()
	for _, id := range []string{base.ID, local.ID, remote.ID} {
		if _, err = c.Entry(ctx, id, "docs/f"); err != nil {
			t.Fatal("branch lost", id, err)
		}
	}
	head, err := c.Latest(ctx)
	if err != nil || head.ID != remote.ID {
		t.Fatal("reconciliation changed baseline implicitly", err)
	}
}

func TestInterruptedScanRestartsAndPublicationDoesNotNeedLocalArchive(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	os.WriteFile(filepath.Join(source, "f"), []byte("value"), 0600)
	pw := []byte("pw")
	// model a killed scan: its request is durable but no archive bytes are complete
	c, _ := s.Catalog(ctx)
	if err := c.Vacuum(ctx, s.pending()); err != nil {
		t.Fatal(err)
	}
	c.Close()
	p, _ := repository.NewCatalogRepository(ctx, s.pending())
	id := "interrupted"
	file := filepath.Join(t.TempDir(), "restart.gpg")
	request, _ := json.Marshal(runRequest{To: s.Set.To, Compression: "bzip2", Class: "STANDARD", Mode: "stream", Level: 6})
	p.SetMeta(ctx, "operation", "run")
	p.SetMeta(ctx, "request", string(request))
	p.SetMeta(ctx, "pending_snapshot", id)
	p.PutSnapshot(ctx, model.Snapshot{Manifest: model.Manifest{ID: id}, File: file})
	p.Close()
	v, err := s.Run(ctx, RunOptions{Password: pw})
	if err != nil {
		t.Fatal("scan could not restart", err)
	}
	if v.ID == id || v.Status != model.STATUS_COMPLETED {
		t.Fatal("incomplete snapshot was committed")
	}
	store.fail = "heads/"
	os.WriteFile(filepath.Join(source, "f"), []byte("changed"), 0600)
	next, err := s.Run(ctx, RunOptions{Password: pw, Output: filepath.Join(t.TempDir(), "pending.gpg")})
	if err == nil {
		t.Fatal("expected publication failure")
	}
	os.Remove(next.File)
	store.fail = ""
	if err = s.Retry(ctx); err != nil {
		t.Fatal("metadata-only retry needed archive bytes", err)
	}
}

func TestFreshDiscoveryIncludesHistoricalRemoteReferences(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	os.WriteFile(filepath.Join(source, "f"), []byte("value"), 0600)
	_, err := s.Run(ctx, RunOptions{Password: []byte("pw"), Output: filepath.Join(t.TempDir(), "full.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	peer := *s
	peer.Registry = registry
	peer.Directory = t.TempDir()
	seen := 0
	if err = peer.Discover(ctx, s.Set.To[0], func(d Descriptor) error {
		seen++
		if d.Set.ID != s.Set.ID {
			return fmt.Errorf("wrong discovery identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatal("set not discovered")
	}
	registered, err := registry.Set(ctx, s.Set.Name)
	if err != nil || registered.ID != s.Set.ID {
		t.Fatal("discovery did not cache registry", err)
	}
	if len(store.files) == 0 {
		t.Fatal("no remote metadata")
	}
}

func TestImportedRootCanBindDifferentLocalBasename(t *testing.T) {
	ctx := context.Background()
	s, _, source := setupService(t)
	s.Set.Roots = nil
	s.Set.To = nil
	s.Set.CatalogTo = "local"
	s.Registry.Save(ctx, s.Set)
	file := filepath.Join("..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg")
	if _, err := s.Import(ctx, ImportOptions{File: file, Password: []byte("encb fixture password")}); err != nil {
		t.Fatal(err)
	}
	if len(s.Set.Roots) != 1 {
		t.Skip("fixture has multiple input roots")
	}
	name := s.Set.Roots[0].Name
	if err := s.BindRoots(ctx, []model.Root{{Name: name, Path: source}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateRoots(); err != nil {
		t.Fatal("logical root name wrongly required local basename", err)
	}
	if err := s.BindRoots(ctx, []model.Root{{Name: name, Path: filepath.Dir(source)}}); err == nil {
		t.Fatal("bound root changed")
	}
}

func TestCatalogSecretCannotChangeSilently(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	s.Config.Catalog.Encrypted = true
	s.CatalogPassword = []byte("original catalog secret")
	os.WriteFile(filepath.Join(source, "f"), []byte("value"), 0600)
	if _, err := s.Run(ctx, RunOptions{Password: []byte("archive pw"), Output: filepath.Join(t.TempDir(), "one.gpg")}); err != nil {
		t.Fatal(err)
	}
	before := len(store.files)
	s.CatalogPassword = []byte("different catalog secret")
	os.WriteFile(filepath.Join(source, "f"), []byte("new value"), 0600)
	if _, err := s.Run(ctx, RunOptions{Password: []byte("new archive pw"), Output: filepath.Join(t.TempDir(), "two.gpg")}); err == nil {
		t.Fatal("catalog secret silently rotated")
	}
	if len(store.files) != before {
		t.Fatal("wrong catalog secret uploaded data")
	}
}

func TestMoveBackToPreviouslyUsedProviderPreservesHistory(t *testing.T) {
	ctx := context.Background()
	s, _, source := setupService(t)
	original := s.Set.To[0]
	pw := []byte("pw")
	os.WriteFile(filepath.Join(source, "f"), []byte("first"), 0600)
	first, err := s.Run(ctx, RunOptions{Password: pw, Output: filepath.Join(t.TempDir(), "first.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Registry.BindRemote(ctx, model.Remote{Name: "other", Kind: "b2", Bucket: "other"})
	if err != nil {
		t.Fatal(err)
	}
	s.Stores[other.ID] = newStore()
	s.Kinds[other.ID] = "b2"
	if err = s.Move(ctx, other.ID, RetrievalOptions{Tier: "bulk", Days: 7}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(source, "f"), []byte("second"), 0600)
	second, err := s.Run(ctx, RunOptions{Password: pw, Incremental: true, Output: filepath.Join(t.TempDir(), "second.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Move(ctx, original, RetrievalOptions{Tier: "bulk", Days: 7}); err != nil {
		t.Fatal("could not move back to existing catalog", err)
	}
	peer := *s
	peer.Directory = t.TempDir()
	if err = peer.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	c, _ := peer.Catalog(ctx)
	defer c.Close()
	for _, id := range []string{first.ID, second.ID} {
		if _, err = c.Entry(ctx, id, "docs/f"); err != nil {
			t.Fatal("history missing after round trip", err)
		}
	}
	latest, err := c.Latest(ctx)
	if err != nil || latest.ID != second.ID {
		t.Fatal("move rolled back current snapshot", err)
	}
}

func TestAWSAbandonedProposalDoesNotConsumeRevision(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	s.Kinds[s.Set.CatalogTo] = "aws"
	os.WriteFile(filepath.Join(source, "f"), []byte("first"), 0600)
	store.fail = "commits/"
	_, err := s.Run(ctx, RunOptions{Password: []byte("pw"), Output: filepath.Join(t.TempDir(), "abandoned.gpg")})
	if !errors.Is(err, ErrPending) {
		t.Fatal("missing pending commit", err)
	}
	count := 0
	if err = s.Discover(ctx, s.Set.CatalogTo, func(Descriptor) error { count++; return nil }); err != nil || count != 0 {
		t.Fatal("unfinished proposal was discoverable", err)
	}
	if err = s.Abandon(ctx); err != nil {
		t.Fatal(err)
	}
	store.fail = ""
	v, err := s.Run(ctx, RunOptions{Password: []byte("pw"), Output: filepath.Join(t.TempDir(), "committed.gpg")})
	if err != nil {
		t.Fatal("abandoned proposal blocked publication", err)
	}
	peer := *s
	peer.Directory = t.TempDir()
	if err = peer.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := peer.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	latest, err := c.Latest(ctx)
	if err != nil || latest.ID != v.ID {
		t.Fatal("wrong committed snapshot", err)
	}
	if err = peer.Discover(ctx, s.Set.CatalogTo, func(Descriptor) error { count++; return nil }); err != nil || count != 1 {
		t.Fatal("committed set was not discoverable", err)
	}
}
