package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"glesha/database/model"
	"glesha/database/repository"
	"glesha/memory"
)

func TestEncbCompatibilityWithoutExecutables(t *testing.T) {
	t.Setenv("PATH", "")
	for _, name := range []string{"encb-fixture-stream-20260704120000.tar.gz.gpg", "encb-fixture-mem-20260704120000.tar.gz.gpg"} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "extract")
			_, format, err := Extract(context.Background(), ExtractOptions{Input: filepath.Join("testdata", name), Output: out, Password: []byte("encb fixture password")})
			if err != nil || format != "gzip" {
				t.Fatal(format, err)
			}
			count := 0
			filepath.WalkDir(out, func(_ string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				count++
				return nil
			})
			if count < 5 {
				t.Fatal("fixture inventory incomplete")
			}
		})
	}
}
func makeTestArchive(t *testing.T, format, prefix string) (string, []byte) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "docs")
	os.Mkdir(source, 0700)
	os.Mkdir(filepath.Join(source, "empty"), 0700)
	os.WriteFile(filepath.Join(source, "file"), []byte("archive content"), 0600)
	os.Link(filepath.Join(source, "file"), filepath.Join(source, "hard"))
	os.Symlink("file", filepath.Join(source, "sym"))
	roots, err := Roots([]string{source})
	if err != nil {
		t.Fatal(err)
	}
	c, err := repository.NewCatalogRepository(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = Inventory(ctx, c, "snapshot", roots, nil, 2); err != nil {
		t.Fatal(err)
	}
	b, err := memory.Resolve("128MiB", 2, 2, func() (int64, error) { panic("probe") })
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, prefix+"-test"+Extension(format)+".gpg")
	pw := []byte("interop password")
	_, _, err = Create(ctx, Options{Output: file, Compression: format, Level: 6, Mode: "stream", Budget: b, Password: pw, Manifest: model.Manifest{Version: 1, ID: "snapshot", Full: true, Roots: roots, Compression: format}, Catalog: c})
	if err != nil {
		t.Fatal(err)
	}
	return file, pw
}
func TestArchiveFormatsAndIntegrity(t *testing.T) {
	for _, format := range []string{"gzip", "bzip2", "xz"} {
		for _, prefix := range []string{"glesha", "encb"} {
			t.Run(format+"/"+prefix, func(t *testing.T) {
				file, pw := makeTestArchive(t, format, prefix)
				ctx := context.Background()
				out := filepath.Join(t.TempDir(), "out")
				_, got, err := Extract(ctx, ExtractOptions{Input: file, Output: out, Password: pw})
				if err != nil || got != format {
					t.Fatal(got, err)
				}
				a, _ := os.Stat(filepath.Join(out, Stem(file), "docs", "file"))
				b, _ := os.Stat(filepath.Join(out, Stem(file), "docs", "hard"))
				if a == nil || b == nil || !os.SameFile(a, b) {
					t.Fatal("hardlink lost")
				}
				data, _ := os.ReadFile(file)
				data[len(data)-1] ^= 0xff
				corrupt := filepath.Join(t.TempDir(), "corrupt.gpg")
				os.WriteFile(corrupt, data, 0600)
				failed := filepath.Join(t.TempDir(), "failed")
				if _, _, err = Extract(ctx, ExtractOptions{Input: corrupt, Output: failed, Password: pw}); err == nil {
					t.Fatal("corrupt encryption accepted")
				}
				if _, err = os.Stat(failed); !os.IsNotExist(err) {
					t.Fatal("partial output published")
				}
			})
		}
	}
}
func TestIndependentGPGAndTar(t *testing.T) {
	gpg, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("GPG unavailable")
	}
	tarTool, err := exec.LookPath("tar")
	if err != nil {
		t.Skip("tar unavailable")
	}
	for _, format := range []string{"gzip", "bzip2", "xz"} {
		t.Run(format, func(t *testing.T) {
			file, pw := makeTestArchive(t, format, "glesha")
			home := t.TempDir()
			os.Chmod(home, 0700)
			cmd := exec.Command(gpg, "--homedir", home, "--batch", "--no-symkey-cache", "--pinentry-mode", "loopback", "--passphrase-fd", "0", "--decrypt", file)
			cmd.Stdin = bytes.NewReader(append(pw, '\n'))
			plain, err := cmd.Output()
			if err != nil {
				t.Fatal("GPG compatibility", err)
			}
			option := "-tzf"
			if format == "bzip2" {
				option = "-tjf"
			}
			if format == "xz" {
				option = "-tJf"
			}
			cmd = exec.Command(tarTool, option, "-")
			cmd.Stdin = bytes.NewReader(plain)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("tar compatibility: %v %s", err, out)
			}
		})
	}
}
func TestExtractionTraversalAndCancellation(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:/escape"} {
		t.Run(name, func(t *testing.T) {
			var compressed bytes.Buffer
			z := gzip.NewWriter(&compressed)
			w := tar.NewWriter(z)
			w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 1})
			w.Write([]byte("x"))
			w.Close()
			z.Close()
			file := filepath.Join(t.TempDir(), "unsafe.tar.gz")
			os.WriteFile(file, compressed.Bytes(), 0600)
			out := filepath.Join(t.TempDir(), "out")
			if _, _, err := Extract(context.Background(), ExtractOptions{Input: file, Output: out}); err == nil {
				t.Fatal("unsafe path accepted")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("unsafe output published")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := NewDecoder().Decompress(ctx, bytes.NewReader(nil)); err == nil {
		t.Fatal("cancellation ignored")
	}
}
func TestSpillBudget(t *testing.T) {
	for _, mode := range []string{"auto", "memory"} {
		t.Run(mode, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "spill")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			s := spill{file: f, limit: 4, mode: mode}
			_, err = s.Write([]byte("0123456789"))
			if mode == "memory" {
				if err == nil {
					t.Fatal("memory limit ignored")
				}
			} else {
				if err != nil || !s.streamed {
					t.Fatal("auto failed to spill", err)
				}
				f.Seek(0, 0)
				b, _ := io.ReadAll(f)
				if string(b) != "0123456789" {
					t.Fatal("spill lost bytes")
				}
			}
		})
	}
}

func TestLinkEscapesAndCompressionChecksum(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []*tar.Header
	}{{"symlink escape", []*tar.Header{{Name: "wrapper/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside", Mode: 0777}}}, {"hardlink escape", []*tar.Header{{Name: "wrapper/link", Typeflag: tar.TypeLink, Linkname: "../outside", Mode: 0600}}}, {"symlink parent", []*tar.Header{{Name: "wrapper/link", Typeflag: tar.TypeSymlink, Linkname: "target", Mode: 0777}, {Name: "wrapper/link/file", Mode: 0600, Size: 1}}}} {
		t.Run(tc.name, func(t *testing.T) {
			var buffer bytes.Buffer
			z := gzip.NewWriter(&buffer)
			w := tar.NewWriter(z)
			for _, h := range tc.headers {
				if err := w.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				if h.Size > 0 {
					w.Write([]byte("x"))
				}
			}
			w.Close()
			z.Close()
			file := filepath.Join(t.TempDir(), "unsafe.gz")
			os.WriteFile(file, buffer.Bytes(), 0600)
			out := filepath.Join(t.TempDir(), "out")
			if _, _, err := Extract(context.Background(), ExtractOptions{Input: file, Output: out}); err == nil {
				t.Fatal("link escape accepted")
			}
		})
	}
	var buffer bytes.Buffer
	z := gzip.NewWriter(&buffer)
	w := tar.NewWriter(z)
	w.WriteHeader(&tar.Header{Name: "file", Size: 1, Mode: 0600})
	w.Write([]byte("x"))
	w.Close()
	z.Close()
	data := buffer.Bytes()
	data[len(data)-8] ^= 0xff
	file := filepath.Join(t.TempDir(), "bad.gz")
	os.WriteFile(file, data, 0600)
	out := filepath.Join(t.TempDir(), "out")
	if _, _, err := Extract(context.Background(), ExtractOptions{Input: file, Output: out}); err == nil {
		t.Fatal("bad compression checksum accepted")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("checksum failure published output")
	}
}
func TestChangingInputsFailWithoutPublishing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "docs")
	os.Mkdir(source, 0700)
	file := filepath.Join(source, "f")
	os.WriteFile(file, []byte("old"), 0600)
	roots, _ := Roots([]string{source})
	c, _ := repository.NewCatalogRepository(ctx, ":memory:")
	defer c.Close()
	if err := Inventory(ctx, c, "snapshot", roots, nil, 2); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(file, []byte("changed after hashing"), 0600)
	budget, _ := memory.Resolve("128MiB", 2, 2, func() (int64, error) { panic("probe") })
	out := filepath.Join(dir, "changed.gpg")
	_, _, err := Create(ctx, Options{Output: out, Compression: "bzip2", Level: 6, Mode: "stream", Password: []byte("pw"), Budget: budget, Catalog: c, Manifest: model.Manifest{Version: 1, ID: "snapshot", Full: true, Roots: roots}})
	if err == nil {
		t.Fatal("changed input accepted")
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("partial archive published")
	}
}
