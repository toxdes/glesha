package file_io

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func Expand(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		h, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		return filepath.Join(h, strings.TrimPrefix(strings.TrimPrefix(p, "~"), string(filepath.Separator))), nil
	}
	return p, nil
}
func IsReadable(p string) bool {
	f, e := os.Open(p)
	if e != nil {
		return false
	}
	return f.Close() == nil
}
func WriteToFile(p string, b []byte) error { return os.WriteFile(p, b, 0600) }
func Walk(ctx context.Context, p string, fn func(string, fs.FileInfo) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	info, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if e = fn(p, info); e == fs.SkipDir {
		return nil
	} else if e != nil {
		return e
	}
	if !info.IsDir() {
		return nil
	}
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	for {
		entries, e := f.ReadDir(128)
		if e != nil && e != io.EOF {
			return e
		}
		for _, entry := range entries {
			if e := Walk(ctx, filepath.Join(p, entry.Name()), fn); e != nil {
				return e
			}
		}
		if e == io.EOF {
			return nil
		}
	}
}

type ContextReader struct {
	Ctx    context.Context
	Reader io.Reader
}

func (r ContextReader) Read(p []byte) (int, error) {
	if e := r.Ctx.Err(); e != nil {
		return 0, e
	}
	return r.Reader.Read(p)
}
func Publish(temp, dest string) error {
	// hard-link publication cannot overwrite an existing output
	if e := os.Link(temp, dest); e != nil {
		return fmt.Errorf("file_io: publish %s: %w", dest, e)
	}
	return os.Remove(temp)
}
func Temp(dir string) (*os.File, error) { return os.CreateTemp(dir, ".glesha-*") }
