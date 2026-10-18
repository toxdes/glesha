package app_cmd

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

	"glesha/backup"
	"glesha/config"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/memory"
)

func TestImportCommandPublishesWithoutReuploadingPayload(t *testing.T) {
	ctx := context.Background()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	data, err := os.ReadFile(filepath.Join("..", "..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	objects := map[string][]byte{"legacy.gpg": data}
	payloadWrites := 0
	metadataWrites := 0
	type objectXML struct {
		Key  string
		Size int
	}
	type listingXML struct {
		XMLName     xml.Name `xml:"ListBucketResult"`
		Contents    []objectXML
		IsTruncated bool
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/bucket/")
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			listing := listingXML{}
			for key, data := range objects {
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
				payloadWrites++
			} else {
				metadataWrites++
			}
			objects[key] = body
			digest := sha256.Sum256(body)
			w.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
			w.Header().Set("x-amz-version-id", "version")
		case http.MethodHead, http.MethodGet:
			body, ok := objects[key]
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
	fixture, err := filepath.Abs(filepath.Join("..", "..", "archive", "testdata", "encb-fixture-stream-20260704120000.tar.gz.gpg"))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	budget, err := memory.Resolve("128MiB", 1, 1, func() (int64, error) { panic("unexpected memory probe") })
	if err != nil {
		t.Fatal(err)
	}
	r := &runtime{ctx: ctx, registry: registry, config: cfg, root: root, budget: budget, env: &AppCmdEnv{File: fixture, Key: "legacy.gpg", From: destinations{"aws"}, PasswordFile: password}, out: &output}
	if err = importArchive(r, []string{"old-docs"}); err != nil {
		t.Fatal(err)
	}
	if payloadWrites != 0 || metadataWrites == 0 {
		t.Fatal("wrong objects uploaded", payloadWrites, metadataWrites)
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
	mu.Lock()
	defer mu.Unlock()
	if !bytes.Equal(objects["legacy.gpg"], data) || payloadWrites != 0 {
		t.Fatal("remote archive modified")
	}
}
