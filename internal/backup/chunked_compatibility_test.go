package backup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChunkedRecoveryWithGPGAndTar(t *testing.T) {
	for _, name := range []string{"gpg", "tar"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip(name + " unavailable")
		}
	}
	for _, format := range []string{"gzip", "bzip2", "xz"} {
		t.Run(format, func(t *testing.T) {
			if _, err := exec.LookPath(format); err != nil {
				t.Skip(format + " unavailable")
			}
			s, store, source := setupService(t)
			data := bytes.Repeat([]byte("independent recovery\n"), 100000)
			if err := os.WriteFile(filepath.Join(source, "file"), data, 0600); err != nil {
				t.Fatal(err)
			}
			v, err := s.Run(context.Background(), RunOptions{Chunked: true, SpoolMax: 2 << 20, Compression: format, Password: []byte("interop")})
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			var tarBytes bytes.Buffer
			for i, ch := range v.Chunks {
				file := filepath.Join(t.TempDir(), "chunk.gpg")
				if err = os.WriteFile(file, store.files[ch.Key], 0600); err != nil {
					t.Fatal(err)
				}
				decrypt := exec.Command("gpg", "--homedir", home, "--batch", "--pinentry-mode", "loopback", "--passphrase-fd", "0", "--decrypt", file)
				decrypt.Stdin = strings.NewReader("interop\n")
				compressed, err := decrypt.Output()
				if err != nil {
					t.Fatalf("decrypt chunk %d: %v", i, err)
				}
				decompress := exec.Command(format, "-dc")
				decompress.Stdin = bytes.NewReader(compressed)
				decompress.Stdout = &tarBytes
				if err = decompress.Run(); err != nil {
					t.Fatalf("decompress chunk %d: %v", i, err)
				}
			}
			out := t.TempDir()
			extract := exec.Command("tar", "-xf", "-", "-C", out)
			extract.Stdin = &tarBytes
			if b, err := extract.CombinedOutput(); err != nil {
				t.Fatalf("tar: %v: %s", err, b)
			}
			roots, err := os.ReadDir(out)
			if err != nil || len(roots) != 1 {
				t.Fatal("unexpected wrapper", err)
			}
			got, err := os.ReadFile(filepath.Join(out, roots[0].Name(), "docs", "file"))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("external recovery differs", err)
			}
		})
	}
}
