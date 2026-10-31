package app_cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"glesha/config"
	"glesha/database/repository"
	"glesha/memory"
)

type testBucket struct {
	mu              sync.Mutex
	objects         map[string][]byte
	requests        int
	payloadReads    int
	payloadWrites   int
	metadataWrites  int
	catalogReads    int
	rejectDirectory bool
}

func newTestBucket(t *testing.T) (*testBucket, *httptest.Server) {
	t.Helper()
	bucket := &testBucket{objects: map[string][]byte{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucket.mu.Lock()
		defer bucket.mu.Unlock()
		bucket.requests++
		key := strings.TrimPrefix(r.URL.Path, "/bucket/")
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			type objectXML struct {
				Key  string
				Size int
			}
			listing := struct {
				XMLName     xml.Name `xml:"ListBucketResult"`
				Contents    []objectXML
				IsTruncated bool
			}{}
			for key, data := range bucket.objects {
				if strings.HasPrefix(key, r.URL.Query().Get("prefix")) && key > r.URL.Query().Get("start-after") {
					listing.Contents = append(listing.Contents, objectXML{key, len(data)})
				}
			}
			sort.Slice(listing.Contents, func(i, j int) bool { return listing.Contents[i].Key < listing.Contents[j].Key })
			w.Header().Set("Content-Type", "application/xml")
			if err := xml.NewEncoder(w).Encode(listing); err != nil {
				t.Error(err)
			}
			return
		}
		payload := strings.HasSuffix(key, ".gpg") && !strings.Contains(key, "/batches/") && !strings.Contains(key, "/descriptors/")
		switch r.Method {
		case http.MethodPut:
			if bucket.rejectDirectory && strings.Contains(key, "/directory/") {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if _, exists := bucket.objects[key]; exists && r.Header.Get("If-None-Match") == "*" {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			bucket.objects[key] = body
			if payload {
				bucket.payloadWrites++
			} else {
				bucket.metadataWrites++
			}
		case http.MethodHead, http.MethodGet:
			if _, exists := bucket.objects[key]; !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if payload && r.Method == http.MethodGet {
				bucket.payloadReads++
			}
			if r.Method == http.MethodGet && (strings.Contains(key, "/batches/") || strings.Contains(key, "/descriptors/")) {
				bucket.catalogReads++
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body := bucket.objects[key]
		digest := sha256.Sum256(body)
		w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
		w.Header().Set("x-amz-version-id", "version")
		w.Header().Set("x-amz-meta-sha256", hex.EncodeToString(digest[:]))
		if r.Method == http.MethodPut {
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if r.Method == http.MethodGet {
			if _, err := w.Write(body); err != nil {
				t.Error(err)
			}
		}
	}))
	t.Cleanup(server.Close)
	return bucket, server
}

func portableRuntime(t *testing.T, endpoint string) *runtime {
	t.Helper()
	root := t.TempDir()
	registry, err := repository.NewRegistryRepository(context.Background(), filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })
	cfg := config.Defaults()
	cfg.Catalog.Encrypted = false
	cfg.AWS = config.Provider{Bucket: "bucket", Region: "us-east-1", Endpoint: endpoint}
	budget, err := memory.Resolve("128MiB", 1, 1, func() (int64, error) { return 1 << 30, nil })
	if err != nil {
		t.Fatal(err)
	}
	return &runtime{ctx: context.Background(), root: root, registry: registry, config: cfg, budget: budget, limit: make(chan struct{}, 1), env: &AppCmdEnv{}, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
}

func TestPortableCommandsRecoverAndPublishState(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	bucket, server := newTestBucket(t)
	first := portableRuntime(t, server.URL)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "song.txt"), []byte("music"), 0600); err != nil {
		t.Fatal(err)
	}
	first.env.To = destinations{"aws"}
	if err := create(first, []string{"music", source}); err != nil {
		t.Fatal(err)
	}
	password := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(password, []byte("archive secret"), 0600); err != nil {
		t.Fatal(err)
	}
	first.env = &AppCmdEnv{PasswordFile: password, Output: filepath.Join(t.TempDir(), "full.gpg")}
	if err := run(first, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	bucket.mu.Lock()
	reads, writes := bucket.payloadReads, bucket.payloadWrites
	bucket.mu.Unlock()
	second := portableRuntime(t, server.URL)
	second.env.Format = "json"
	if err := catalogSync(second, nil); err != nil {
		t.Fatal(err)
	}
	var discovery catalogSyncResult
	var envelope struct {
		Version int
		Kind    string
		Data    json.RawMessage
	}
	if err := json.NewDecoder(bytes.NewReader(second.out.(*bytes.Buffer).Bytes())).Decode(&envelope); err != nil {
		t.Fatal("catalog JSON contains human output", err)
	}
	if err := json.Unmarshal(envelope.Data, &discovery); err != nil || envelope.Version != 2 || envelope.Kind != "catalog_state" || discovery.State.String() != "SYNCED" {
		t.Fatal("unexpected catalog JSON", err)
	}
	second.env.Format = "plain"
	if err := history(second, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	if second.service.Set.Roots[0].Path != "" {
		t.Fatal("source path copied from another machine")
	}
	second.env.Compression = "gzip"
	if err := configure(second, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	first.env = &AppCmdEnv{Compression: "bzip2"}
	if err := configure(first, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	if first.service.Set.Roots[0].Path != source {
		t.Fatal("sync replaced this machine's source binding")
	}
	bucket.mu.Lock()
	if reads != bucket.payloadReads || writes != bucket.payloadWrites {
		t.Fatal("state operations transferred archive payloads")
	}
	bucket.mu.Unlock()
	fixture := filepath.Join("..", "..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg")
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	bucket.mu.Lock()
	bucket.objects["legacy.gpg"] = data
	bucket.mu.Unlock()
	legacyPassword := filepath.Join(t.TempDir(), "legacy-password")
	if err = os.WriteFile(legacyPassword, []byte("encb fixture password"), 0600); err != nil {
		t.Fatal(err)
	}
	first.env = &AppCmdEnv{File: fixture, Key: "legacy.gpg", From: destinations{"aws"}, PasswordFile: legacyPassword}
	if err = importArchive(first, []string{"legacy"}); err != nil {
		t.Fatal(err)
	}
	third := portableRuntime(t, server.URL)
	if err = catalogSync(third, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = third.registry.Set(third.ctx, "legacy"); err != nil {
		t.Fatal("imported set not recoverable on another machine", err)
	}
	bucket.mu.Lock()
	if bucket.payloadWrites != writes || !bytes.Equal(bucket.objects["legacy.gpg"], data) {
		t.Fatal("legacy archive was reuploaded or modified")
	}
	bucket.mu.Unlock()
	t.Run("old-writer", func(t *testing.T) {
		binary := os.Getenv("GLESHA_LEGACY_BINARY")
		if binary == "" {
			t.Skip("set GLESHA_LEGACY_BINARY to test an older executable")
		}
		cfg := filepath.Join(t.TempDir(), "config.toml")
		text := fmt.Sprintf("[aws]\nbucket='bucket'\nregion='us-east-1'\nendpoint=%q\n[catalog]\nencrypted=false\ndirectory=%q\n", server.URL, first.root)
		if err := os.WriteFile(cfg, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(t.TempDir(), "must-not-exist.gpg")
		command := exec.Command(binary, "run", "music", "--config", cfg, "--passphrase-file", password, "--output", output)
		result, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(result), "invalid remote revision") {
			t.Fatalf("older writer did not reject portable state: %v\n%s", err, result)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("older writer created an archive before rejecting state")
		}
	})
}

func TestRegistryRepairRetriesWithoutRepublishingCatalog(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	bucket, server := newTestBucket(t)
	r := portableRuntime(t, server.URL)
	r.env.To = destinations{"aws"}
	bucket.mu.Lock()
	bucket.rejectDirectory = true
	bucket.mu.Unlock()
	if err := create(r, []string{"music", t.TempDir()}); err == nil {
		t.Fatal("replica failure reported success")
	}
	if pending, err := r.service.DirectoryRepairPending(r.ctx); err != nil || !pending {
		t.Fatal("repair was not journaled", err)
	}
	bucket.mu.Lock()
	batches := 0
	for key := range bucket.objects {
		if strings.Contains(key, "/batches/") {
			batches++
		}
	}
	bucket.rejectDirectory = false
	bucket.mu.Unlock()
	r.env = &AppCmdEnv{}
	if err := retry(r, []string{"music"}); err != nil {
		t.Fatal(err)
	}
	if pending, err := r.service.DirectoryRepairPending(r.ctx); err != nil || pending {
		t.Fatal("repair journal not cleared", err)
	}
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	after := 0
	for key := range bucket.objects {
		if strings.Contains(key, "/batches/") {
			after++
		}
	}
	if after != batches || bucket.payloadWrites != 0 {
		t.Fatal("registry repair republished catalog or uploaded data")
	}
}
