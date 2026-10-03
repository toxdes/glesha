package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"glesha/archive"
	"glesha/cloud"
	"glesha/crypt"
	"glesha/database/model"
)

func chunkDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func chunkFixture(t *testing.T, format string) (*chunkStream, []byte, *memoryStore) {
	t.Helper()
	ctx := context.Background()
	store := newStore()
	input := []byte("first tar fragment then second fragment")
	v := model.Snapshot{Manifest: model.Manifest{Layout: "chunked", Compression: format}}
	offset := 0
	for i, part := range [][]byte{input[:17], input[17:]} {
		var encrypted bytes.Buffer
		cipher, err := crypt.Encrypt(ctx, &encrypted, []byte("test passphrase"))
		if err != nil {
			t.Fatal(err)
		}
		compressor, err := archive.Compress(cipher, format, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = compressor.Write(part); err != nil {
			t.Fatal(err)
		}
		if err = compressor.Close(); err != nil {
			t.Fatal(err)
		}
		if err = cipher.Close(); err != nil {
			t.Fatal(err)
		}
		key := string(rune('a' + i))
		b := encrypted.Bytes()
		c := model.Chunk{Index: i, Offset: int64(offset), PlainSize: int64(len(part)), Size: int64(len(b)), Hash: chunkDigest(b), PlainHash: chunkDigest(part), Locations: []model.Location{{Provider: "b2", Key: key, Status: model.STATUS_COMPLETED}}}
		store.files[key] = append([]byte(nil), b...)
		store.objects[key] = cloud.Object{Size: c.Size, Hash: c.Hash}
		v.Chunks = append(v.Chunks, c)
		offset += len(part)
	}
	stream := &chunkStream{ctx: ctx, service: &Service{Stores: map[string]cloud.Store{"missing": newStore(), "b2": store}}, snapshot: v, from: []string{"missing", "b2"}, password: []byte("test passphrase")}
	return stream, input, store
}
func TestChunkStreamFormats(t *testing.T) {
	for _, format := range []string{"gzip", "bzip2", "xz"} {
		t.Run(format, func(t *testing.T) {
			stream, want, _ := chunkFixture(t, format)
			defer stream.Close()
			if err := validateChunks(stream.snapshot); err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(stream)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("got %q", got)
			}
		})
	}
}
func TestChunkStreamRejectsCorruption(t *testing.T) {
	for _, kind := range []string{"plaintext checksum", "encrypted checksum", "integrity", "compression", "size"} {
		t.Run(kind, func(t *testing.T) {
			stream, _, store := chunkFixture(t, "gzip")
			defer stream.Close()
			c := &stream.snapshot.Chunks[0]
			switch kind {
			case "plaintext checksum":
				c.PlainHash = strings.Repeat("0", 64)
			case "encrypted checksum":
				c.Hash = strings.Repeat("0", 64)
				h := store.objects["a"]
				h.Hash = ""
				store.objects["a"] = h
			case "integrity":
				b := store.files["a"]
				b[len(b)-1] ^= 1
				c.Hash = chunkDigest(b)
				h := store.objects["a"]
				h.Hash = c.Hash
				store.objects["a"] = h
			case "compression":
				stream.snapshot.Compression = "xz"
			case "size":
				c.PlainSize--
			}
			if _, err := io.ReadAll(stream); err == nil {
				t.Fatal("corrupt chunk accepted")
			}
		})
	}
}
func TestValidateChunksRejectsSequence(t *testing.T) {
	stream, _, _ := chunkFixture(t, "gzip")
	stream.snapshot.Chunks[1].Offset++
	if err := validateChunks(stream.snapshot); err == nil {
		t.Fatal("gap accepted")
	}
}

func TestPrepareChunkedRestoreChecksEveryChunk(t *testing.T) {
	ctx := context.Background()
	s, store, _ := setupService(t)
	stream, _, fixture := chunkFixture(t, "gzip")
	v := stream.snapshot
	v.ID = "chunked-test"
	v.Full = true
	v.Status = model.STATUS_COMPLETED
	v.Hash = strings.Repeat("0", 64)
	v.Size = 123456789
	remote := s.Set.To[0]
	s.Kinds[remote] = "aws"
	for i := range v.Chunks {
		chunk := &v.Chunks[i]
		chunk.Locations[0].Provider = remote
		key := chunk.Locations[0].Key
		store.files[key] = fixture.files[key]
		h := fixture.objects[key]
		h.Version = "pinned"
		if i == 1 {
			h.Cold = true
			h.Class = "DEEP_ARCHIVE"
		}
		store.objects[key] = h
	}
	catalog, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = catalog.PutSnapshot(ctx, v); err != nil {
		t.Fatal(err)
	}
	catalog.Close()
	output := filepath.Join(t.TempDir(), "restored")
	_, _, err = s.PrepareRestore(ctx, v.ID, output, s.Set.To, RetrievalOptions{})
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("expected explicit retrieval consent, got %v", err)
	}
	_, _, err = s.PrepareRestore(ctx, v.ID, output, s.Set.To, RetrievalOptions{Request: true})
	if !errors.Is(err, ErrPending) {
		t.Fatalf("expected retrieval pending, got %v", err)
	}
	if store.puts["retrieval:a"] != 0 || store.puts["retrieval:b"] != 1 {
		t.Fatal("retrieved wrong chunks")
	}
	_, _, err = s.PrepareRestore(ctx, v.ID, output, s.Set.To, RetrievalOptions{Request: true})
	if !errors.Is(err, ErrPending) || store.puts["retrieval:b"] != 1 {
		t.Fatal("retrieval repeated", err)
	}
	h := store.objects["b"]
	h.Cold = false
	store.objects["b"] = h
	if _, _, err = s.PrepareRestore(ctx, v.ID, output, s.Set.To, RetrievalOptions{Request: true}); err != nil {
		t.Fatal(err)
	}
}

func TestChunkRefreshTracksIndependentLifecycleChanges(t *testing.T) {
	ctx := context.Background()
	s, store, _ := setupService(t)
	remote := s.Set.To[0]
	initial := "STANDARD"
	v := model.Snapshot{Manifest: model.Manifest{ID: "chunk-refresh", Full: true, Layout: "chunked"}, Size: 30, Hash: strings.Repeat("0", 64), Status: model.STATUS_COMPLETED}
	for i, key := range []string{"first-chunk", "second-chunk"} {
		c := model.Chunk{Index: i, Size: 10, Hash: strings.Repeat("0", 64), Locations: []model.Location{{Provider: remote, Key: key, Version: "v", InitialClass: initial, CurrentClass: initial, Status: model.STATUS_COMPLETED}}}
		v.Chunks = append(v.Chunks, c)
		h := cloud.Object{Key: key, Size: 10, Hash: c.Hash, Version: "v", Class: "STANDARD_IA"}
		if i == 1 {
			h.Class = "DEEP_ARCHIVE"
			h.Cold = true
		}
		store.objects[key] = h
	}
	catalog, err := s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = catalog.PutSnapshot(ctx, v); err != nil {
		t.Fatal(err)
	}
	descriptor := model.Location{Snapshot: v.ID, Provider: remote, Key: "descriptor", Version: "v", InitialClass: initial, CurrentClass: initial, Status: model.STATUS_COMPLETED}
	if err = catalog.PutLocation(ctx, descriptor); err != nil {
		t.Fatal(err)
	}
	catalog.Close()
	store.objects[descriptor.Key] = cloud.Object{Key: descriptor.Key, Size: 10, Hash: v.Hash, Version: "v", Class: initial}
	if err = s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.History(ctx, func(got model.Snapshot, locations []model.Location) error {
		if len(locations) != 1 || locations[0].CurrentClass != initial {
			t.Fatal("descriptor observation overwritten")
		}
		a, b := got.Chunks[0].Locations[0], got.Chunks[1].Locations[0]
		if a.CurrentClass != "STANDARD_IA" || b.CurrentClass != "DEEP_ARCHIVE" || !b.Cold || a.InitialClass != initial || b.InitialClass != initial {
			t.Fatalf("lifecycle observations conflated: %+v %+v", a, b)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	catalog, err = s.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	saved, err := catalog.Snapshot(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Chunks[0].Locations[0].CurrentClass != initial {
		t.Fatal("refresh overwrote committed catalog identity")
	}
}
