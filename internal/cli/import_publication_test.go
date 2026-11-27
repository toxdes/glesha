package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"glesha/internal/backup"
	"glesha/internal/config"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
	"glesha/internal/memory"
)

func TestImportCommandPublishesWithoutReuploadingPayload(t *testing.T) {
	ctx := context.Background()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	data, err := os.ReadFile(filepath.Join("..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg"))
	if err != nil {
		t.Fatal(err)
	}
	f := newImportRemote(t, map[string][]byte{"legacy.gpg": data, "copy.gpg": data})
	server := f.server
	defer server.Close()
	cfg := config.Defaults()
	cfg.Catalog.Encrypted = false
	cfg.AWS = config.Provider{Bucket: "bucket", Region: "us-east-1", Endpoint: server.URL}
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	root := t.TempDir()
	password := filepath.Join(root, "password")
	os.WriteFile(password, []byte("encb fixture password"), 0600)
	fixture, err := filepath.Abs(filepath.Join("..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg"))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	budget, err := memory.Resolve("128MiB", 1, 1, func() (int64, error) { panic("unexpected memory probe") })
	if err != nil {
		t.Fatal(err)
	}
	r := &runtime{ctx: ctx, registry: registry, config: cfg, root: root, budget: budget, env: &AppCmdEnv{File: fixture, Key: "legacy.gpg", From: destinations{"aws"}, PassphraseFile: password}, out: &output}
	if err = importArchive(r, []string{"old-docs"}); err != nil {
		t.Fatal(err)
	}
	if f.payloadWrites != 0 || f.metadataWrites == 0 {
		t.Fatal("wrong objects uploaded", f.payloadWrites, f.metadataWrites)
	}
	c, err := r.service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if revision, _ := c.GetMeta(ctx, "remote_revision"); revision == "" {
		t.Fatal("catalog not published")
	}
	if r.service.Pending() {
		t.Fatal("import remains pending")
	}
	c.Close()
	r.env.File = ""
	// one archive in two sets would break the other set when one is deleted
	if err = importArchive(r, []string{"shared-docs"}); err == nil || !strings.Contains(err.Error(), "legacy.gpg is already in set old-docs") {
		t.Fatalf("archive imported into a second set: %v", err)
	}
	r.env.Key = "copy.gpg"
	if err = importArchive(r, []string{"downloaded-docs"}); err != nil {
		t.Fatal(err)
	}
	c, err = r.service.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var localFile string
	if err = c.EachSnapshot(ctx, func(v model.Snapshot) error { localFile = v.File; return nil }); err != nil {
		t.Fatal(err)
	}
	c.Close()
	r.env.DryRun = true
	if err = cleanup(r, []string{"downloaded-docs"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(localFile); err != nil {
		t.Fatal("dry run removed an archive", err)
	}
	r.env.DryRun = false
	if err = cleanup(r, []string{"downloaded-docs"}); !errors.Is(err, backup.ErrConfirmationRequired) {
		t.Fatalf("deletion lacked confirmation: %v", err)
	}
	if _, err = os.Stat(localFile); err != nil {
		t.Fatal("unconfirmed removal", err)
	}
	r.env.AssumeYes = true
	if err = cleanup(r, []string{"downloaded-docs"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(localFile); !os.IsNotExist(err) {
		t.Fatal("archive not removed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !bytes.Equal(f.objects["legacy.gpg"], data) || f.payloadWrites != 0 {
		t.Fatal("remote archive modified")
	}
}

// importRemote is an S3 bucket in memory that counts payload and metadata writes.
type importRemote struct {
	mu             sync.Mutex
	server         *httptest.Server
	objects        map[string][]byte
	payloadWrites  int
	metadataWrites int
}

func newImportRemote(t *testing.T, objects map[string][]byte) *importRemote {
	t.Helper()
	f := &importRemote{objects: objects}
	type objectXML struct {
		Key  string
		Size int
	}
	type listingXML struct {
		XMLName     xml.Name `xml:"ListBucketResult"`
		Contents    []objectXML
		IsTruncated bool
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/bucket/")
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			listing := listingXML{}
			for key, data := range f.objects {
				if strings.HasPrefix(key, r.URL.Query().Get("prefix")) {
					listing.Contents = append(listing.Contents, objectXML{key, len(data)})
				}
			}
			w.Header().Set("Content-Type", "application/xml")
			xml.NewEncoder(w).Encode(listing)
			return
		}
		switch r.Method {
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if key == "legacy.gpg" {
				f.payloadWrites++
			} else {
				f.metadataWrites++
			}
			f.objects[key] = body
			digest := sha256.Sum256(body)
			w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
			w.Header().Set("x-amz-version-id", "version")
		case http.MethodHead, http.MethodGet:
			body, ok := f.objects[key]
			if !ok {
				w.WriteHeader(404)
				return
			}
			digest := sha256.Sum256(body)
			w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
			w.Header().Set("x-amz-version-id", "version")
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.Header().Set("x-amz-meta-sha256", hex.EncodeToString(digest[:]))
			if r.Method == http.MethodGet {
				w.Write(body)
			}
		default:
			t.Errorf("unexpected operation: %s", r.Method)
			w.WriteHeader(400)
		}
	}))
	return f
}

func TestImportPublishesCatalogWithCatalogPassphrase(t *testing.T) {
	ctx := context.Background()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("GLESHA_CATALOG_PASSPHRASE", "catalog secret")
	fixture, err := filepath.Abs(filepath.Join("..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	f := newImportRemote(t, map[string][]byte{"legacy.gpg": data})
	defer f.server.Close()
	cfg := config.Defaults()
	cfg.Catalog.Encrypted = true
	cfg.AWS = config.Provider{Bucket: "bucket", Region: "us-east-1", Endpoint: f.server.URL}
	registry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	root := t.TempDir()
	password := filepath.Join(root, "password")
	os.WriteFile(password, []byte("encb fixture password"), 0600)
	budget, err := memory.Resolve("128MiB", 1, 1, func() (int64, error) { panic("unexpected memory probe") })
	if err != nil {
		t.Fatal(err)
	}
	r := &runtime{ctx: ctx, registry: registry, config: cfg, root: root, budget: budget, env: &AppCmdEnv{File: fixture, Key: "legacy.gpg", From: destinations{"aws"}, PassphraseFile: password}, out: io.Discard}
	if err = importArchive(r, []string{"docs"}); err != nil {
		t.Fatal(err)
	}

	// a second machine must open the published catalog with the catalog passphrase
	peerRegistry, err := repository.NewRegistryRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer peerRegistry.Close()
	remote, err := registry.Remote(ctx, r.service.Set.CatalogTo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = peerRegistry.BindRemote(ctx, remote); err != nil {
		t.Fatal(err)
	}
	peer := &backup.Service{Config: r.service.Config, Budget: budget, Registry: peerRegistry, Directory: t.TempDir(), Stores: r.service.Stores}
	if err = peer.DiscoverState(ctx, r.service.Set.CatalogTo); err != nil {
		t.Fatal(err)
	}
	if err = peer.OpenSet(ctx, r.service.Set.ID, peer.Directory); err != nil {
		t.Fatal(err)
	}
	if err = peer.CacheCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	peer.CatalogPassword = []byte("catalog secret")
	if err = peer.UnlockCached(ctx); err != nil {
		t.Fatal("published catalog does not open with the catalog passphrase:", err)
	}
	if peer.Set.Name != "docs" {
		t.Fatalf("unlocked set name %q, want docs", peer.Set.Name)
	}
}
