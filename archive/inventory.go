package archive

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
)

func Roots(paths []string) ([]model.Root, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("archive: at least one source is required")
	}
	var out []model.Root
	for _, p := range paths {
		p, e := file_io.Expand(p)
		if e != nil {
			return nil, e
		}
		p, e = filepath.Abs(p)
		if e != nil {
			return nil, e
		}
		p, e = filepath.EvalSymlinks(p)
		if e != nil {
			return nil, e
		}
		if !file_io.IsReadable(p) {
			return nil, fmt.Errorf("archive: source is unreadable %s", p)
		}
		if !utf8.ValidString(p) {
			return nil, fmt.Errorf("archive: source path is not valid UTF-8")
		}
		name := filepath.Base(p)
		if _, e = SafeName(name); e != nil {
			return nil, e
		}
		if name == ManifestName || name == DeletionsName {
			return nil, fmt.Errorf("archive: source basename %s is reserved", name)
		}
		if name == "." || name == string(filepath.Separator) {
			return nil, fmt.Errorf("archive: filesystem roots are unsupported")
		}
		for _, r := range out {
			if r.Name == name {
				return nil, fmt.Errorf("archive: colliding basename %s", name)
			}
			if Within(p, r.Path) || Within(r.Path, p) {
				return nil, fmt.Errorf("archive: overlapping roots")
			}
		}
		out = append(out, model.Root{Path: p, Name: name})
	}
	return out, nil
}
func Within(p, root string) bool {
	rel, e := filepath.Rel(root, p)
	return e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func statIdentity(info fs.FileInfo) string {
	v := reflect.ValueOf(info.Sys())
	if !v.IsValid() {
		return ""
	}
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	dev, ino := v.FieldByName("Dev"), v.FieldByName("Ino")
	if !dev.IsValid() || !ino.IsValid() {
		return ""
	}
	return fmt.Sprintf("%v:%v", dev.Interface(), ino.Interface())
}
func identity(info fs.FileInfo) string {
	v := reflect.ValueOf(info.Sys())
	if !v.IsValid() {
		return ""
	}
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	n := v.FieldByName("Nlink")
	if !n.IsValid() || n.Uint() < 2 {
		return ""
	}
	return statIdentity(info)
}

func metadata(ctx context.Context, sources file_io.SourceReader, p, rel string, info fs.FileInfo) (model.Entry, error) {
	if _, err := SafeName(rel); err != nil {
		return model.Entry{}, err
	}
	link := ""
	var e error
	if info.Mode()&os.ModeSymlink != 0 {
		link, e = sources.Readlink(ctx, p)
		if e != nil {
			return model.Entry{}, e
		}
		if e = safeSymlink(rel, link); e != nil {
			return model.Entry{}, e
		}
	}
	h, e := tar.FileInfoHeader(info, link)
	if e != nil {
		return model.Entry{}, e
	}
	t := "file"
	switch {
	case info.IsDir():
		t = "directory"
	case info.Mode()&os.ModeSymlink != 0:
		t = "symlink"
	case !info.Mode().IsRegular():
		return model.Entry{}, fmt.Errorf("archive: unsupported file type %s", p)
	}
	parent := path.Dir(rel)
	if parent == "." {
		parent = ""
	}
	size := info.Size()
	if t != "file" {
		size = 0
	}
	return model.Entry{SourceIdentity: statIdentity(info), Path: rel, Name: path.Base(rel), Parent: parent, Type: t, Size: size, Mode: h.Mode, ModTime: info.ModTime().UnixNano(), UID: h.Uid, GID: h.Gid, Link: link, Source: p, Identity: identity(info), PAX: h.PAXRecords}, nil
}
func Hash(ctx context.Context, p string) (string, int64, error) {
	f, err := file_io.NewSourceReader().Open(ctx, p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	progress := L.StartProgress(ctx, "Verifying archive", info.Size())
	defer progress.Finish()
	return hashReader(ctx, f, progress)
}

func hashSource(ctx context.Context, sources file_io.SourceReader, p string, progress *L.Progress) (string, int64, error) {
	f, e := sources.Open(ctx, p)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	return hashReader(ctx, f, progress)
}

func hashReader(ctx context.Context, r io.Reader, progress *L.Progress) (string, int64, error) {
	h := sha256.New()
	n, e := io.CopyBuffer(h, file_io.ContextReader{Ctx: ctx, Reader: progress.Reader(r)}, make([]byte, 64*1024))
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func unchanged(v model.Entry, info fs.FileInfo) bool {
	header, err := tar.FileInfoHeader(info, "")
	if err != nil || header.Uid != v.UID || header.Gid != v.GID {
		return false
	}
	kindOK := v.Type == "directory" && info.IsDir() || v.Type == "symlink" && info.Mode()&os.ModeSymlink != 0 || (v.Type == "file" || v.Type == "hardlink") && info.Mode().IsRegular()
	return (v.SourceIdentity == "" || v.SourceIdentity == statIdentity(info)) && kindOK && (v.Type != "file" && v.Type != "hardlink" || info.Size() == v.Size) && info.ModTime().UnixNano() == v.ModTime && info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == file_io.Permissions(v.Mode)
}
func Inventory(ctx context.Context, c repository.Cataloger, id string, roots []model.Root, exclude []string, workers int) error {
	return InventorySources(ctx, file_io.NewSourceReader(), c, id, roots, exclude, workers)
}

func InventorySources(ctx context.Context, sources file_io.SourceReader, c repository.Cataloger, id string, roots []model.Root, exclude []string, workers int) error {
	if workers < 1 {
		return fmt.Errorf("archive: hashing workers must be positive")
	}
	for _, root := range roots {
		for _, artifact := range exclude {
			if excluded(root.Path, artifact) {
				return fmt.Errorf("archive: source root %s is excluded application state or output", root.Path)
			}
		}
	}
	progress := L.StartWorkerProgress(ctx, "Scanning", -1, workers)
	defer progress.Finish()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan model.Entry, workers)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	fail := func(e error) { once.Do(func() { first = e; cancel() }) }
	results := make(chan model.Entry, workers)
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		batch := make([]model.Entry, 0, 128)
		flush := func() {
			if len(batch) > 0 && ctx.Err() == nil {
				if err := c.PutEntries(ctx, id, batch); err != nil {
					fail(err)
				}
			}
			batch = batch[:0]
		}
		for entry := range results {
			if ctx.Err() != nil {
				continue
			}
			batch = append(batch, entry)
			if len(batch) == cap(batch) {
				flush()
			}
		}
		flush()
	}()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range jobs {
				if ctx.Err() != nil {
					continue
				}
				if v.Type == "file" {
					h, n, e := hashSource(ctx, sources, v.Source, progress)
					if e != nil {
						fail(e)
						continue
					}
					info, e := sources.Lstat(ctx, v.Source)
					if e != nil || !unchanged(v, info) || n != v.Size {
						fail(fmt.Errorf("archive: source changed while hashing %s", v.Source))
						continue
					}
					v.Hash = h
				}
				select {
				case results <- v:
				case <-ctx.Done():
				}
			}
		}()
	}
	for _, r := range roots {
		e := sources.Walk(ctx, r.Path, func(p string, info fs.FileInfo) error {
			for _, x := range exclude {
				if excluded(p, x) {
					if info.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
			}
			rel, e := filepath.Rel(r.Path, p)
			if e != nil {
				return e
			}
			if r.Name == "" && rel == "." {
				return nil
			}
			rel = path.Join(r.Name, filepath.ToSlash(rel))
			v, e := metadata(ctx, sources, p, rel, info)
			if e != nil {
				return e
			}
			select {
			case jobs <- v:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if e != nil {
			fail(e)
			break
		}
	}
	close(jobs)
	wg.Wait()
	close(results)
	writer.Wait()
	if first != nil {
		return fmt.Errorf("archive: inventory: %w", first)
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	return c.EachEntry(ctx, id, func(v model.Entry) error {
		if v.Type != "file" || v.Identity == "" {
			return nil
		}
		first, e := c.FirstIdentity(ctx, id, v.Identity)
		if e != nil {
			return e
		}
		if first.Path == v.Path {
			return nil
		}
		v.Type = "hardlink"
		v.Link = first.Path
		return c.PutEntry(ctx, id, v)
	})
}

func excluded(p, artifact string) bool {
	return artifact != "" && (Within(p, artifact) || p == artifact+"-journal" || p == artifact+"-wal" || p == artifact+"-shm")
}

func ValidateTree(ctx context.Context, c repository.Cataloger, id string, roots []model.Root, exclusions []string) error {
	return validateTree(ctx, file_io.NewSourceReader(), c, id, roots, exclusions)
}

func validateTree(ctx context.Context, sources file_io.SourceReader, c repository.Cataloger, id string, roots []model.Root, exclusions []string) error {
	for _, root := range roots {
		if err := sources.Walk(ctx, root.Path, func(p string, info fs.FileInfo) error {
			for _, artifact := range exclusions {
				if excluded(p, artifact) {
					if info.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
			}
			rel, err := filepath.Rel(root.Path, p)
			if err != nil {
				return err
			}
			if root.Name == "" && rel == "." {
				return nil
			}
			name := path.Join(root.Name, filepath.ToSlash(rel))
			if _, err = c.Entry(ctx, id, name); err != nil {
				return fmt.Errorf("archive: source tree changed at %s: %w", p, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
func Equal(a, b model.Entry) bool {
	return a.Path == b.Path && a.Type == b.Type && a.Size == b.Size && a.Mode == b.Mode && a.ModTime == b.ModTime && a.UID == b.UID && a.GID == b.GID && a.Link == b.Link && a.Hash == b.Hash
}
func Changed(ctx context.Context, c repository.Cataloger, current, parent string, v model.Entry) (bool, error) {
	if parent == "" {
		return true, nil
	}
	old, e := c.Entry(ctx, parent, v.Path)
	if e == sql.ErrNoRows {
		return true, nil
	}
	if e != nil {
		return false, e
	}
	return !Equal(old, v), nil
}

func ValidateSources(ctx context.Context, c repository.Cataloger, id string, ownOutputs ...string) error {
	return validateSources(ctx, file_io.NewSourceReader(), c, id, ownOutputs...)
}

func validateSources(ctx context.Context, sources file_io.SourceReader, c repository.Cataloger, id string, ownOutputs ...string) error {
	return c.EachEntry(ctx, id, func(v model.Entry) error {
		info, e := sources.Lstat(ctx, v.Source)
		if e != nil {
			return e
		}
		check := v
		for _, output := range ownOutputs {
			if v.Type == "directory" && Within(filepath.Dir(output), v.Source) {
				check.ModTime = info.ModTime().UnixNano()
			}
		}
		if !unchanged(check, info) {
			return fmt.Errorf("archive: source changed during operation %s", v.Source)
		}
		if v.Type == "symlink" {
			link, e := sources.Readlink(ctx, v.Source)
			if e != nil {
				return e
			}
			if link != v.Link {
				return fmt.Errorf("archive: source link changed %s", v.Source)
			}
		}
		return nil
	})
}
