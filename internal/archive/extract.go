package archive

import (
	"archive/tar"
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"glesha/internal/crypt"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
	"glesha/internal/fsutil"
	"glesha/internal/logger"
)

func Plain(ctx context.Context, r io.Reader, password []byte) (io.Reader, error) {
	return plainWith(ctx, crypt.NewCipher(), r, password)
}

func IsCompressed(signature []byte) bool {
	return len(signature) >= 3 && (signature[0] == 0x1f && signature[1] == 0x8b || string(signature[:3]) == "BZh" || string(signature[:3]) == "\xfd7z")
}

func plainWith(ctx context.Context, decryptor crypt.Decryptor, r io.Reader, password []byte) (io.Reader, error) {
	b := bufio.NewReader(fsutil.ContextReader{Ctx: ctx, Reader: r})
	sig, e := b.Peek(3)
	if e != nil {
		return nil, e
	}
	if IsCompressed(sig) {
		return b, nil
	}
	return decryptor.Decrypt(ctx, b, password)
}

type Decoder interface {
	Decompress(context.Context, io.Reader) (io.Reader, string, error)
}

type decoder struct{}

func NewDecoder() Decoder { return decoder{} }

func Decompress(r io.Reader) (io.Reader, string, error) {
	return NewDecoder().Decompress(context.Background(), r)
}

func (decoder) Decompress(ctx context.Context, r io.Reader) (io.Reader, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	b := bufio.NewReader(r)
	sig, e := b.Peek(3)
	if e != nil {
		return nil, "", e
	}
	if sig[0] == 0x1f && sig[1] == 0x8b {
		z, e := gzip.NewReader(b)
		return z, "gzip", e
	}
	if string(sig) == "BZh" {
		return bzip2.NewReader(b), "bzip2", nil
	}
	if string(sig) == "\xfd7z" {
		z, err := newXZReader(ctx, b)
		return z, "xz", err
	}
	return nil, "", fmt.Errorf("archive: unsupported compression signature")
}
func SafeName(n string) (string, error) {
	if !utf8.ValidString(n) || strings.Contains(n, "\\") || strings.Contains(n, ":") || strings.ContainsRune(n, 0) || strings.HasPrefix(n, "/") {
		return "", fmt.Errorf("archive: unsafe member %q", n)
	}
	n = path.Clean(n)
	if n == ".." || strings.HasPrefix(n, "../") || n == "." {
		return "", fmt.Errorf("archive: unsafe member %q", n)
	}
	return n, nil
}

func safeSymlink(name, link string) error {
	if link == "" || !utf8.ValidString(link) || path.IsAbs(link) || strings.ContainsAny(link, "\\:\x00") {
		return fmt.Errorf("archive: unsafe symlink %s", name)
	}
	target := path.Join(path.Dir(name), link)
	if target == "." {
		return nil
	}
	if _, err := SafeName(target); err != nil {
		return fmt.Errorf("archive: unsafe symlink %s: %w", name, err)
	}
	return nil
}
func safeParents(root *os.Root, n string) error {
	for parent := path.Dir(n); parent != "."; parent = path.Dir(parent) {
		info, e := root.Lstat(parent)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if !info.IsDir() {
			return fmt.Errorf("archive: non-directory ancestor %s", parent)
		}
	}
	return nil
}
func drain(r io.Reader) error {
	buf := make([]byte, 64*1024)
	for {
		n, e := r.Read(buf)
		for _, b := range buf[:n] {
			if b != 0 {
				return fmt.Errorf("archive: unexpected bytes after tar terminator")
			}
		}
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
	}
}

// Scan consumes every encryption and compression layer before returning success.
func Scan(ctx context.Context, r io.Reader, password []byte, fn func(*tar.Header, io.Reader) error) (string, error) {
	return scanWith(ctx, NewDecoder(), crypt.NewCipher(), r, password, fn)
}

func scanWith(ctx context.Context, decoder Decoder, decryptor crypt.Decryptor, r io.Reader, password []byte, fn func(*tar.Header, io.Reader) error) (string, error) {
	if decoder == nil {
		decoder = NewDecoder()
	}
	if decryptor == nil {
		decryptor = crypt.NewCipher()
	}
	plain, e := plainWith(ctx, decryptor, r, password)
	if e != nil {
		return "", e
	}
	z, format, e := decoder.Decompress(ctx, plain)
	if e != nil {
		return "", e
	}
	if closer, ok := z.(io.Closer); ok {
		defer closer.Close()
	}
	tr := tar.NewReader(z)
	for {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
		if _, e = SafeName(h.Name); e != nil {
			return "", e
		}
		if e = fn(h, tr); e != nil {
			return "", e
		}
	}
	if e = drain(z); e != nil {
		return "", e
	}
	if _, e = io.Copy(io.Discard, plain); e != nil {
		return "", e
	}
	return format, nil
}
func Validate(ctx context.Context, p string, password []byte) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return "", e
	}
	progress := logger.StartProgress(ctx, "Validating archive", info.Size())
	defer progress.Finish()
	return Scan(ctx, progress.Reader(f), password, func(h *tar.Header, r io.Reader) error { _, e := io.Copy(io.Discard, r); return e })
}
func DecryptFile(ctx context.Context, input, output string, password []byte) (string, error) {
	in, e := os.Open(input)
	if e != nil {
		return "", e
	}
	defer in.Close()
	info, e := in.Stat()
	if e != nil {
		return "", e
	}
	progress := logger.StartProgress(ctx, "Decrypting", info.Size())
	defer progress.Finish()
	r, e := crypt.Decrypt(ctx, progress.Reader(in), password)
	if e != nil {
		return "", e
	}
	tempDir := filepath.Dir(output)
	if output == "" {
		tempDir = filepath.Dir(input)
	}
	out, e := fsutil.Temp(tempDir)
	if e != nil {
		return "", e
	}
	defer func() { out.Close(); os.Remove(out.Name()) }()
	if _, e = io.Copy(out, r); e != nil {
		return "", e
	}
	if e = out.Close(); e != nil {
		return "", e
	}
	format, e := Validate(ctx, out.Name(), nil)
	if e != nil {
		return "", e
	}
	if output == "" {
		output = filepath.Join(filepath.Dir(input), Stem(input)+Extension(format))
	}
	if e = fsutil.Publish(out.Name(), output); e != nil {
		return "", e
	}
	return format, nil
}

type ExtractOptions struct {
	Decoder                Decoder
	Decryptor              crypt.Decryptor
	Input, Output          string
	Password               []byte
	StripWrapper           bool
	Overlay                bool
	DeferDirectoryMetadata bool
	Catalog                repository.Cataloger
	Snapshot               string
}

func Extract(ctx context.Context, o ExtractOptions) (model.Manifest, string, error) {
	if o.Decoder == nil {
		o.Decoder = NewDecoder()
	}
	if o.Decryptor == nil {
		o.Decryptor = crypt.NewCipher()
	}
	var manifest model.Manifest
	if _, e := os.Lstat(o.Output); !os.IsNotExist(e) {
		return manifest, "", fmt.Errorf("archive: extraction output already exists")
	}
	stage, e := os.MkdirTemp(filepath.Dir(o.Output), ".glesha-extract-*")
	if e != nil {
		return manifest, "", e
	}
	defer os.RemoveAll(stage)
	format, e := ExtractInto(ctx, o, stage, &manifest)
	if e != nil {
		return manifest, "", e
	}
	if _, e = os.Lstat(o.Output); !os.IsNotExist(e) {
		return manifest, "", fmt.Errorf("archive: output appeared during extraction")
	}
	if e = fsutil.PublishDir(stage, o.Output); e != nil {
		return manifest, "", e
	}
	return manifest, format, nil
}
func ExtractInto(ctx context.Context, o ExtractOptions, stage string, manifest *model.Manifest) (string, error) {
	in, e := os.Open(o.Input)
	if e != nil {
		return "", e
	}
	defer in.Close()
	info, e := in.Stat()
	if e != nil {
		return "", e
	}
	progress := logger.StartProgress(ctx, "Extracting", info.Size())
	defer progress.Finish()
	return extractWith(ctx, o, stage, manifest, func(fn func(*tar.Header, io.Reader) error) (string, error) {
		return scanWith(ctx, o.Decoder, o.Decryptor, progress.Reader(in), o.Password, fn)
	})
}

func ExtractTarInto(ctx context.Context, o ExtractOptions, r io.Reader, stage string, manifest *model.Manifest) error {
	_, err := extractWith(ctx, o, stage, manifest, func(fn func(*tar.Header, io.Reader) error) (string, error) {
		tr := tar.NewReader(fsutil.ContextReader{Ctx: ctx, Reader: r})
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			if err = fn(h, tr); err != nil {
				return "", err
			}
		}
		return "", drain(r)
	})
	return err
}

func extractWith(ctx context.Context, o ExtractOptions, stage string, manifest *model.Manifest, scan func(func(*tar.Header, io.Reader) error) (string, error)) (string, error) {
	root, err := os.OpenRoot(stage)
	if err != nil {
		return "", err
	}
	defer root.Close()
	tmp, err := fsutil.Temp(filepath.Dir(stage))
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	scratch, err := repository.NewCatalogRepository(ctx, tmp.Name())
	if err != nil {
		return "", err
	}
	defer scratch.Close()
	x := &extractor{o: o, root: root, stage: stage, manifest: manifest, scratch: scratch}
	format, err := scan(func(h *tar.Header, r io.Reader) error { return x.entry(ctx, h, r) })
	if err != nil {
		return "", err
	}
	if err = x.linkHardlinks(ctx); err != nil {
		return "", err
	}
	// apply directory metadata after creating descendants
	if o.DeferDirectoryMetadata {
		return format, nil
	}
	return format, x.applyDirectoryMetadata(ctx)
}

// extractor unpacks one tar stream into stage. Hard links are created in a
// second pass, once their targets exist.
type extractor struct {
	o        ExtractOptions
	root     *os.Root
	stage    string
	manifest *model.Manifest
	// scratch records every extracted entry for the later passes
	scratch repository.Cataloger
	// wrapper is the top-level directory stripped with StripWrapper
	wrapper string
}

// entry extracts one tar member.
func (x *extractor) entry(ctx context.Context, h *tar.Header, r io.Reader) error {
	n, skip, err := x.memberName(h)
	if err != nil || skip {
		return err
	}
	if x.isSpecial(n, DeletionsName) {
		_, err := io.Copy(io.Discard, r)
		return err
	}
	if x.isSpecial(n, ManifestName) {
		return x.readManifest(h, r)
	}
	if err = safeParents(x.root, n); err != nil {
		return err
	}
	if err = x.root.MkdirAll(path.Dir(n), 0700); err != nil {
		return err
	}
	if x.o.Overlay {
		info, statErr := x.root.Lstat(n)
		if statErr == nil && !(h.Typeflag == tar.TypeDir && info.IsDir()) {
			if err = x.root.RemoveAll(n); err != nil {
				return err
			}
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
	}
	v, err := x.write(n, h, r)
	if err != nil {
		return err
	}
	return x.scratch.PutEntry(ctx, "scan", v)
}

// memberName returns the path of h inside the archive, without the wrapper
// directory when StripWrapper is set; skip reports members that are the wrapper itself.
func (x *extractor) memberName(h *tar.Header) (n string, skip bool, err error) {
	n, err = SafeName(h.Name)
	if err != nil || !x.o.StripWrapper {
		return n, false, err
	}
	first, rest, found := strings.Cut(n, "/")
	if x.wrapper == "" {
		x.wrapper = first
	}
	if first != x.wrapper {
		return "", false, fmt.Errorf("archive: inconsistent archive wrapper")
	}
	return rest, !found || rest == "", nil
}

// isSpecial reports whether n is the glesha member called name, at the archive
// root or, without StripWrapper, directly inside the wrapper.
func (x *extractor) isSpecial(n, name string) bool {
	return n == name || !x.o.StripWrapper && strings.Count(n, "/") == 1 && path.Base(n) == name
}

func (x *extractor) readManifest(h *tar.Header, r io.Reader) error {
	if h.Size > 4<<20 {
		return fmt.Errorf("archive: manifest exceeds limit")
	}
	if err := json.NewDecoder(io.LimitReader(r, 4<<20)).Decode(x.manifest); err != nil {
		return err
	}
	if x.manifest.Version != 1 && x.manifest.Version != 2 {
		return fmt.Errorf("archive: unsupported manifest version")
	}
	if x.manifest.Layout != "" && x.manifest.Layout != "single" && x.manifest.Layout != "chunked" {
		return fmt.Errorf("archive: unsupported manifest layout")
	}
	return nil
}

// write creates member n from h and r, except hard links, and describes it.
func (x *extractor) write(n string, h *tar.Header, r io.Reader) (model.Entry, error) {
	root := x.root
	parent := path.Dir(n)
	if parent == "." {
		parent = ""
	}
	v := model.Entry{Path: n, Name: path.Base(n), Parent: parent, Mode: h.Mode, Size: h.Size, ModTime: h.ModTime.UnixNano(), UID: h.Uid, GID: h.Gid, PAX: h.PAXRecords, Archive: x.o.Snapshot, Member: h.Name}
	switch h.Typeflag {
	case tar.TypeDir:
		v.Type = "directory"
		v.Size = 0
		return v, root.MkdirAll(n, 0700)
	case tar.TypeReg, tar.TypeRegA:
		v.Type = "file"
		f, err := root.OpenFile(n, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return v, err
		}
		hash := sha256.New()
		_, err = io.Copy(io.MultiWriter(f, hash), r)
		closeErr := f.Close()
		if err != nil {
			return v, err
		}
		if closeErr != nil {
			return v, closeErr
		}
		v.Hash = hex.EncodeToString(hash.Sum(nil))
		if err = fsutil.SetOwner(root, n, h.Uid, h.Gid, false); err != nil {
			return v, err
		}
		if err = root.Chmod(n, fsutil.Permissions(h.Mode)); err != nil {
			return v, err
		}
		return v, root.Chtimes(n, h.ModTime, h.ModTime)
	case tar.TypeSymlink:
		v.Type = "symlink"
		v.Size = 0
		v.Link = h.Linkname
		if err := safeSymlink(n, h.Linkname); err != nil {
			return v, err
		}
		if err := root.Symlink(h.Linkname, n); err != nil {
			return v, err
		}
		if err := fsutil.SetOwner(root, n, h.Uid, h.Gid, true); err != nil {
			return v, err
		}
		return v, fsutil.SetLinkTimes(filepath.Join(x.stage, filepath.FromSlash(n)), h.ModTime)
	case tar.TypeLink:
		v.Type = "hardlink"
		v.Link = h.Linkname
		if x.o.StripWrapper {
			prefix := x.wrapper + "/"
			if !strings.HasPrefix(v.Link, prefix) {
				return v, fmt.Errorf("archive: hardlink outside wrapper")
			}
			v.Link = strings.TrimPrefix(v.Link, prefix)
		}
		_, err := SafeName(v.Link)
		return v, err
	default:
		return v, fmt.Errorf("archive: unsupported tar entry type %d", h.Typeflag)
	}
}

// linkHardlinks creates the recorded hard links and copies every entry to the
// caller's catalog.
func (x *extractor) linkHardlinks(ctx context.Context) error {
	return x.scratch.EachEntry(ctx, "scan", func(v model.Entry) error {
		if v.Type == "hardlink" {
			if err := safeParents(x.root, v.Link); err != nil {
				return err
			}
			info, err := x.root.Lstat(v.Link)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("archive: hardlink target is not regular")
			}
			if err = x.root.Link(v.Link, v.Path); err != nil {
				return err
			}
			hash, size, err := Hash(ctx, filepath.Join(x.stage, filepath.FromSlash(v.Path)))
			if err != nil {
				return err
			}
			v.Hash, v.Size = hash, size
		}
		if x.o.Catalog != nil {
			return x.o.Catalog.PutEntry(ctx, x.o.Snapshot, v)
		}
		return nil
	})
}

func (x *extractor) applyDirectoryMetadata(ctx context.Context) error {
	return x.scratch.EachDirectory(ctx, "scan", func(v model.Entry) error {
		if v.Type != "directory" {
			return nil
		}
		if err := fsutil.SetOwner(x.root, v.Path, v.UID, v.GID, false); err != nil {
			return err
		}
		if err := x.root.Chmod(v.Path, fsutil.Permissions(v.Mode)); err != nil {
			return err
		}
		t := time.Unix(0, v.ModTime)
		return x.root.Chtimes(v.Path, t, t)
	})
}
