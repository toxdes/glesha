package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"glesha/cloud"
	"glesha/database/model"
)

func TestSpoolSize(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int64
	}{{"128M", 128000000}, {"1.1G", 1100000000}, {"128MiB", 128 << 20}, {"1.5GiB", 3 << 29}, {"12345", 12345}} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseSpoolSize(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("%d %v", got, err)
			}
		})
	}
	for _, input := range []string{"NaNG", "InfG", "-1M", "0", "1.2what", "99999999999999999999G"} {
		if _, err := ParseSpoolSize(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
func TestChunkedFullAndMixedIncrementalRestore(t *testing.T) {
	for _, compression := range []string{"gzip", "bzip2", "xz"} {
		t.Run(compression, func(t *testing.T) {
			ctx := context.Background()
			s, store, source := setupService(t)
			pw := []byte("chunk password")
			data := make([]byte, 3<<20)
			if _, err := rand.Read(data); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "large.bin"), data, 0600); err != nil {
				t.Fatal(err)
			}
			v, err := s.Run(ctx, RunOptions{Chunked: true, SpoolMax: 2 << 20, Compression: compression, Password: pw})
			if err != nil {
				t.Fatal(err)
			}
			if v.Layout != "chunked" || len(v.Chunks) < 3 || v.File != "" {
				t.Fatalf("unexpected chunked snapshot: %+v", v)
			}
			var total int64
			for _, c := range v.Chunks {
				if c.Size > 2<<20 || c.File != "" {
					t.Fatalf("spool not bounded/cleaned: %+v", c)
				}
				total += c.Size
				if len(c.Locations) != 1 {
					t.Fatal("missing location")
				}
				if store.puts[c.Key] != 1 {
					t.Fatal("chunk not uploaded once")
				}
			}
			if v.Size <= total {
				t.Fatal("manifest size missing")
			}
			out := filepath.Join(t.TempDir(), "restored")
			if err = s.Restore(ctx, RestoreOptions{ID: v.ID, Output: out, Password: func(model.Snapshot) ([]byte, error) { return bytes.Clone(pw), nil }}); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(out, "docs", "large.bin"))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("restore differs", err)
			}
			if err = os.WriteFile(filepath.Join(source, "new.txt"), []byte("delta"), 0600); err != nil {
				t.Fatal(err)
			}
			inc, err := s.Run(ctx, RunOptions{Incremental: true, Compression: "gzip", Output: filepath.Join(t.TempDir(), "single.gpg"), Password: pw})
			if err != nil {
				t.Fatal(err)
			}
			out = filepath.Join(t.TempDir(), "mixed")
			if err = s.Restore(ctx, RestoreOptions{ID: inc.ID, Output: out, Password: func(model.Snapshot) ([]byte, error) { return bytes.Clone(pw), nil }}); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(out, "docs", "new.txt")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestChunkedResumeRetainsCompletedUploads(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	pw := []byte("password")
	data := make([]byte, 3<<20)
	rand.Read(data)
	os.WriteFile(filepath.Join(source, "large.bin"), data, 0600)
	store.fail = "/chunks/000001-"
	v, err := s.Run(ctx, RunOptions{Chunked: true, SpoolMax: 2 << 20, Compression: "gzip", Password: pw})
	if !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	if len(v.Chunks) != 2 || len(v.Chunks[0].Locations) != 1 || v.Chunks[1].File == "" {
		t.Fatal("missing resume checkpoint")
	}
	if _, err = os.Stat(v.Chunks[1].File); err != nil {
		t.Fatal("unfinished chunk lost", err)
	}
	store.fail = ""
	if _, err = s.Run(ctx, RunOptions{Password: []byte("wrong")}); err == nil {
		t.Fatal("wrong resume passphrase accepted")
	}
	resumed, err := s.Run(ctx, RunOptions{Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != v.ID || store.puts[v.Chunks[0].Key] != 1 {
		t.Fatal("completed chunk reuploaded")
	}
	for _, ch := range resumed.Chunks {
		if store.puts[ch.Key] != 1 {
			t.Fatal("unexpected uploads")
		}
	}
}
func TestChunkedChangedSourceAndConflictingOptions(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	pw := []byte("password")
	os.WriteFile(filepath.Join(source, "a"), bytes.Repeat([]byte("a"), 3<<20), 0600)
	store.fail = "/chunks/000001-"
	_, err := s.Run(ctx, RunOptions{Chunked: true, SpoolMax: 2 << 20, Compression: "gzip", Password: pw})
	if !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(source, "a"), bytes.Repeat([]byte("b"), 3<<20), 0600)
	store.fail = ""
	if _, err = s.Run(ctx, RunOptions{Password: pw}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("accepted changed source: %v", err)
	}
	for _, o := range []RunOptions{{Chunked: true, KeepArchive: true}, {Chunked: true, Output: "x"}, {Chunked: true, SpoolMax: 1}, {SpoolMax: 128000000}} {
		clean, _, _ := setupService(t)
		if _, err = clean.Run(ctx, o); err == nil {
			t.Fatalf("accepted conflicting options: %+v", o)
		}
	}
}

type spoolCheckingStore struct {
	*memoryStore
	directory string
	maximum   int64
	highWater int64
	cancel    context.CancelFunc
}

func (s *spoolCheckingStore) PutNew(ctx context.Context, key, file, class string) (cloud.Object, error) {
	if strings.Contains(key, "/chunks/") || strings.HasSuffix(key, "/manifest.json.gpg") {
		var size int64
		err := filepath.WalkDir(s.directory, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.Contains(path, "chunks-") {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				size += info.Size()
			}
			return nil
		})
		if err != nil {
			return cloud.Object{}, err
		}
		if size > s.maximum {
			return cloud.Object{}, fmt.Errorf("spool cap exceeded: %d", size)
		}
		if size > s.highWater {
			s.highWater = size
		}
	}
	object, err := s.memoryStore.PutNew(ctx, key, file, class)
	if err == nil && s.cancel != nil && strings.Contains(key, "/chunks/") {
		s.cancel()
		s.cancel = nil
	}
	return object, err
}
func TestChunkedBoundedSpoolAndAcknowledgmentRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, store, source := setupService(t)
	data := make([]byte, 3<<20)
	rand.Read(data)
	os.WriteFile(filepath.Join(source, "big"), data, 0600)
	checked := &spoolCheckingStore{memoryStore: store, maximum: 2 << 20, cancel: cancel}
	// the generated directory is discovered when the first upload begins
	for id := range s.Stores {
		s.Stores[id] = checked
	}
	checked.directory = s.Directory
	v, err := s.Run(ctx, RunOptions{Chunked: true, SpoolMax: 2 << 20, Compression: "gzip", Password: []byte("password")})
	if err == nil || !s.Pending() {
		t.Fatal("interruption did not preserve pending work", err)
	}
	// metadata databases are outside payload spool, so inspect the dedicated directory on resume
	checked.directory = filepath.Join(s.Directory, "chunks-"+v.ID)
	store.mu.Lock()
	already := map[string]int{}
	for key, n := range store.puts {
		already[key] = n
	}
	store.mu.Unlock()
	_, err = s.Run(context.Background(), RunOptions{Password: []byte("password")})
	if err != nil {
		t.Fatal(err)
	}
	for key, n := range already {
		if strings.Contains(key, "/chunks/") && store.puts[key] != n {
			t.Fatal("acknowledged part uploaded again")
		}
	}
	if checked.highWater > checked.maximum {
		t.Fatal("spool exceeded")
	}
}

func TestChunkedIncrementalDeletionAndNoChanges(t *testing.T) {
	ctx := context.Background()
	s, _, source := setupService(t)
	pw := []byte("password")
	os.WriteFile(filepath.Join(source, "removed"), []byte("old"), 0600)
	_, err := s.Run(ctx, RunOptions{Password: pw, Output: filepath.Join(t.TempDir(), "baseline.gpg")})
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(source, "removed"))
	os.WriteFile(filepath.Join(source, "added"), []byte("new"), 0600)
	v, err := s.Run(ctx, RunOptions{Incremental: true, Chunked: true, SpoolMax: 2 << 20, Password: pw, Confirm: func(context.Context, string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "restore")
	if err = s.Restore(ctx, RestoreOptions{ID: v.ID, Output: out, Password: func(model.Snapshot) ([]byte, error) { return bytes.Clone(pw), nil }}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(out, "docs", "removed")); !os.IsNotExist(err) {
		t.Fatal("deleted file retained")
	}
	if _, err = os.Stat(filepath.Join(out, "docs", "added")); err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.Run(ctx, RunOptions{Incremental: true, Chunked: true, SpoolMax: 2 << 20, Password: pw})
	if err != nil || unchanged.ID != "" {
		t.Fatal("unexpected no-change payload", err)
	}
}

func TestAbandonChunkedClearsOnlyLocalSpool(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	os.WriteFile(filepath.Join(source, "file"), bytes.Repeat([]byte("a"), 3<<20), 0600)
	store.fail = "/chunks/000001-"
	v, err := s.Run(ctx, RunOptions{Chunked: true, SpoolMax: 2 << 20, Password: []byte("pw")})
	if !errors.Is(err, ErrPending) {
		t.Fatal(err)
	}
	if err = s.Abandon(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.Directory, "chunks-"+v.ID)); !os.IsNotExist(err) {
		t.Fatal("abandoned local spool retained")
	}
	if _, ok := store.objects[v.Chunks[0].Key]; !ok {
		t.Fatal("uploaded chunks deleted implicitly")
	}
	if s.Pending() {
		t.Fatal("still pending")
	}
}

func TestImportIndividualChunkRejectsRegistration(t *testing.T) {
	ctx := context.Background()
	s, store, source := setupService(t)
	os.WriteFile(filepath.Join(source, "file"), []byte("small"), 0600)
	v, err := s.Run(ctx, RunOptions{Chunked: true, Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Chunks) != 1 {
		t.Fatal("expected complete one-fragment tar")
	}
	file := filepath.Join(t.TempDir(), "chunk.gpg")
	if err = os.WriteFile(file, store.files[v.Chunks[0].Key], 0600); err != nil {
		t.Fatal(err)
	}
	peer, _, _ := setupService(t)
	if _, err = peer.Import(ctx, ImportOptions{File: file, Password: []byte("pw")}); err == nil || !strings.Contains(err.Error(), "individual chunked") {
		t.Fatal("individual chunk accepted", err)
	}
	if peer.Pending() {
		t.Fatal("invalid import left pending work")
	}
}
