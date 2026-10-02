package archive

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"glesha/database/model"
	"glesha/database/repository"
	L "glesha/logger"
	"glesha/memory"
)

func TestArchiveInputBytesSelectsPayloadBodies(t *testing.T) {
	parent := []model.Entry{
		{Path: "same", Type: "file", Size: 17, Hash: "aa"},
		{Path: "changed", Type: "file", Size: 11, Hash: "bb"},
		{Path: "removed", Type: "file", Size: 100, Hash: "cc"},
	}
	current := []model.Entry{
		{Path: "same", Type: "file", Size: 17, Hash: "aa"},
		{Path: "changed", Type: "file", Size: 23, Hash: "dd"},
		{Path: "new", Type: "file", Size: 7, Hash: "ee"},
		{Path: "empty", Type: "directory"},
		{Path: "symlink", Type: "symlink", Link: "same"},
	}
	for _, test := range []struct {
		name    string
		parent  string
		entries []model.Entry
		want    int64
	}{
		{"full", "", current, 47},
		{"incremental", "parent", current, 30},
		{"new hardlink forces unchanged target", "parent", append(append([]model.Entry{}, current...), model.Entry{Path: "alias", Type: "hardlink", Size: 17, Link: "same"}), 47},
		{"unchanged", "parent", parent, 0},
		{"metadata change includes content", "parent", []model.Entry{{Path: "same", Type: "file", Size: 17, Hash: "aa", Mode: 0600}}, 17},
		{"empty", "", nil, 0},
		{"deletions only", "parent", nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			catalog, err := repository.NewCatalogRepository(ctx, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer catalog.Close()
			if err = catalog.PutEntries(ctx, "parent", parent); err != nil {
				t.Fatal(err)
			}
			if err = catalog.PutEntries(ctx, "current", test.entries); err != nil {
				t.Fatal(err)
			}
			options := Options{Catalog: catalog, Manifest: model.Manifest{ID: "current", Parent: test.parent}}
			if err = prepareHardlinks(ctx, options); err != nil {
				t.Fatal(err)
			}
			total, err := archiveInputBytes(ctx, options)
			if err != nil || total != test.want {
				t.Fatal("incorrect payload byte total", total, test.want, err)
			}
		})
	}
}

type slowProgressEncoder struct{}

func (slowProgressEncoder) Compress(ctx context.Context, output io.Writer, format string, level int) (io.WriteCloser, error) {
	w, err := NewEncoder().Compress(ctx, output, format, level)
	if err != nil {
		return nil, err
	}
	return &slowProgressCompressor{WriteCloser: w}, nil
}

type slowProgressCompressor struct {
	io.WriteCloser
	once sync.Once
}

func (w *slowProgressCompressor) Write(data []byte) (int, error) {
	if len(data) >= 64<<10 {
		w.once.Do(func() { time.Sleep(150 * time.Millisecond) })
	}
	return w.WriteCloser.Write(data)
}

func TestArchiveCreationReportsActualInputTotal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "docs")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), bytes.Repeat([]byte("x"), 256<<10), 0600); err != nil {
		t.Fatal(err)
	}
	roots, err := Roots([]string{source})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := repository.NewCatalogRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	if err = Inventory(ctx, catalog, "snapshot", roots, nil, 1); err != nil {
		t.Fatal(err)
	}
	budget, err := memory.Resolve("128MiB", 1, 1, func() (int64, error) { t.Fatal("probe called"); return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	renderer := L.NewProgressRenderer(&output, false)
	defer renderer.Close()
	ctx = L.WithProgress(ctx, renderer)
	_, _, err = Create(ctx, Options{Encoder: slowProgressEncoder{}, Output: filepath.Join(root, "archive.tar.gz.gpg"), Compression: "gzip", Level: 6, Mode: "stream", Budget: budget, Password: []byte("test password"), Catalog: catalog, Manifest: model.Manifest{ID: "snapshot", Version: 1, Full: true, Roots: roots}})
	renderer.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "64.00 KiB / 256.00 KiB 25.00%") || !strings.Contains(output.String(), "Finalizing archive") {
		t.Fatal("archive did not expose its byte total", output.String())
	}
}

func TestArchiveInputBytesRejectsInvalidTotals(t *testing.T) {
	for _, entries := range [][]model.Entry{
		{{Path: "negative", Type: "file", Size: -1}},
		{{Path: "a", Type: "file", Size: math.MaxInt64}, {Path: "b", Type: "file", Size: 1}},
	} {
		ctx := context.Background()
		catalog, err := repository.NewCatalogRepository(ctx, ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		if err = catalog.PutEntries(ctx, "current", entries); err != nil {
			catalog.Close()
			t.Fatal(err)
		}
		_, err = archiveInputBytes(ctx, Options{Catalog: catalog, Manifest: model.Manifest{ID: "current"}})
		catalog.Close()
		if err == nil || !strings.Contains(err.Error(), "invalid total source size") {
			t.Fatal("invalid total accepted", err)
		}
	}
}
