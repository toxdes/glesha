package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnknownManifestLayoutLeavesOutputUnpublished(t *testing.T) {
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	w := tar.NewWriter(z)
	manifest := []byte(`{"version":2,"id":"future","layout":"unknown"}`)
	if err := w.WriteHeader(&tar.Header{Name: "root/" + ManifestName, Mode: 0600, Size: int64(len(manifest))}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "unknown.tar.gz")
	if err := os.WriteFile(file, compressed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "out")
	if _, _, err := Extract(context.Background(), ExtractOptions{Input: file, Output: output}); err == nil || !strings.Contains(err.Error(), "unsupported manifest layout") {
		t.Fatal("unknown layout accepted", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("output published")
	}
}
