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

	"glesha/crypt"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
)

func Plain(ctx context.Context, r io.Reader, password []byte) (io.Reader, error) {
	return plainWith(ctx, crypt.NewCipher(), r, password)
}

func IsCompressed(signature []byte) bool {
	return len(signature) >= 3 && (signature[0] == 0x1f && signature[1] == 0x8b || string(signature[:3]) == "BZh" || string(signature[:3]) == "\xfd7z")
}

func plainWith(ctx context.Context, decryptor crypt.Decryptor, r io.Reader, password []byte) (io.Reader, error) {
	b := bufio.NewReader(file_io.ContextReader{Ctx: ctx, Reader: r})
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
	progress := L.StartProgress(ctx, "Validating archive", info.Size())
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
	progress := L.StartProgress(ctx, "Decrypting", info.Size())
	defer progress.Finish()
	r, e := crypt.Decrypt(ctx, progress.Reader(in), password)
	if e != nil {
		return "", e
	}
	tempDir := filepath.Dir(output)
	if output == "" {
		tempDir = filepath.Dir(input)
	}
	out, e := file_io.Temp(tempDir)
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
	if e = file_io.Publish(out.Name(), output); e != nil {
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
	if e = file_io.PublishDir(stage, o.Output); e != nil {
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
	progress := L.StartProgress(ctx, "Extracting", info.Size())
	defer progress.Finish()
	return extractWith(ctx, o, stage, manifest, func(fn func(*tar.Header, io.Reader) error) (string, error) {
		return scanWith(ctx, o.Decoder, o.Decryptor, progress.Reader(in), o.Password, fn)
	})
}

func ExtractTarInto(ctx context.Context, o ExtractOptions, r io.Reader, stage string, manifest *model.Manifest) error {
	_, err := extractWith(ctx, o, stage, manifest, func(fn func(*tar.Header, io.Reader) error) (string, error) {
		tr := tar.NewReader(file_io.ContextReader{Ctx: ctx, Reader: r})
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
	root, e := os.OpenRoot(stage)
	if e != nil {
		return "", e
	}
	defer root.Close()
	tmp, e := file_io.Temp(filepath.Dir(stage))
	if e != nil {
		return "", e
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	cat, e := repository.NewCatalogRepository(ctx, tmp.Name())
	if e != nil {
		return "", e
	}
	defer cat.Close()
	wrapper := ""
	format, e := scan(func(h *tar.Header, r io.Reader) error {
		n, e := SafeName(h.Name)
		if e != nil {
			return e
		}
		if o.StripWrapper {
			first, rest, found := strings.Cut(n, "/")
			if wrapper == "" {
				wrapper = first
			}
			if first != wrapper {
				return fmt.Errorf("archive: inconsistent archive wrapper")
			}
			if !found {
				return nil
			}
			n = rest
			if n == "" {
				return nil
			}
		}
		if n == DeletionsName || !o.StripWrapper && strings.Count(n, "/") == 1 && path.Base(n) == DeletionsName {
			_, e := io.Copy(io.Discard, r)
			return e
		}
		if n == ManifestName || !o.StripWrapper && strings.Count(n, "/") == 1 && path.Base(n) == ManifestName {
			if h.Size > 4<<20 {
				return fmt.Errorf("archive: manifest exceeds limit")
			}
			if e = json.NewDecoder(io.LimitReader(r, 4<<20)).Decode(manifest); e != nil {
				return e
			}
			if manifest.Version != 1 && manifest.Version != 2 {
				return fmt.Errorf("archive: unsupported manifest version")
			}
			if manifest.Layout != "" && manifest.Layout != "single" && manifest.Layout != "chunked" {
				return fmt.Errorf("archive: unsupported manifest layout")
			}
			return nil
		}
		if e = safeParents(root, n); e != nil {
			return e
		}
		if e = root.MkdirAll(path.Dir(n), 0700); e != nil {
			return e
		}
		if o.Overlay {
			info, se := root.Lstat(n)
			if se == nil && !(h.Typeflag == tar.TypeDir && info.IsDir()) {
				if e = root.RemoveAll(n); e != nil {
					return e
				}
			} else if se != nil && !os.IsNotExist(se) {
				return se
			}
		}
		parent := path.Dir(n)
		if parent == "." {
			parent = ""
		}
		v := model.Entry{Path: n, Name: path.Base(n), Parent: parent, Mode: h.Mode, Size: h.Size, ModTime: h.ModTime.UnixNano(), UID: h.Uid, GID: h.Gid, PAX: h.PAXRecords, Archive: o.Snapshot, Member: h.Name}
		switch h.Typeflag {
		case tar.TypeDir:
			v.Type = "directory"
			v.Size = 0
			if e = root.MkdirAll(n, 0700); e != nil {
				return e
			}
		case tar.TypeReg, tar.TypeRegA:
			v.Type = "file"
			f, e := root.OpenFile(n, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			hash := sha256.New()
			_, e = io.Copy(io.MultiWriter(f, hash), r)
			ce := f.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
			v.Hash = hex.EncodeToString(hash.Sum(nil))
			if e = file_io.SetOwner(root, n, h.Uid, h.Gid, false); e != nil {
				return e
			}
			if e = root.Chmod(n, file_io.Permissions(h.Mode)); e != nil {
				return e
			}
			if e = root.Chtimes(n, h.ModTime, h.ModTime); e != nil {
				return e
			}
		case tar.TypeSymlink:
			v.Type = "symlink"
			v.Size = 0
			v.Link = h.Linkname
			if e = safeSymlink(n, h.Linkname); e != nil {
				return e
			}
			if e = root.Symlink(h.Linkname, n); e != nil {
				return e
			}
			if e = file_io.SetOwner(root, n, h.Uid, h.Gid, true); e != nil {
				return e
			}
			if e = file_io.SetLinkTimes(filepath.Join(stage, filepath.FromSlash(n)), h.ModTime); e != nil {
				return e
			}
		case tar.TypeLink:
			v.Type = "hardlink"
			v.Link = h.Linkname
			if o.StripWrapper {
				prefix := wrapper + "/"
				if !strings.HasPrefix(v.Link, prefix) {
					return fmt.Errorf("archive: hardlink outside wrapper")
				}
				v.Link = strings.TrimPrefix(v.Link, prefix)
			}
			if _, e = SafeName(v.Link); e != nil {
				return e
			}
		default:
			return fmt.Errorf("archive: unsupported tar entry type %d", h.Typeflag)
		}
		return cat.PutEntry(ctx, "scan", v)
	})
	if e != nil {
		return "", e
	}
	e = cat.EachEntry(ctx, "scan", func(v model.Entry) error {
		if v.Type == "hardlink" {
			if e := safeParents(root, v.Link); e != nil {
				return e
			}
			info, e := root.Lstat(v.Link)
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("archive: hardlink target is not regular")
			}
			if e = root.Link(v.Link, v.Path); e != nil {
				return e
			}
			hash, size, e := Hash(ctx, filepath.Join(stage, filepath.FromSlash(v.Path)))
			if e != nil {
				return e
			}
			v.Hash, v.Size = hash, size
		}
		if o.Catalog != nil {
			if e := o.Catalog.PutEntry(ctx, o.Snapshot, v); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	// apply directory metadata after creating descendants
	if o.DeferDirectoryMetadata {
		return format, nil
	}
	e = cat.EachDirectory(ctx, "scan", func(v model.Entry) error {
		if v.Type != "directory" {
			return nil
		}
		if e := file_io.SetOwner(root, v.Path, v.UID, v.GID, false); e != nil {
			return e
		}
		if e := root.Chmod(v.Path, file_io.Permissions(v.Mode)); e != nil {
			return e
		}
		t := time.Unix(0, v.ModTime)
		return root.Chtimes(v.Path, t, t)
	})
	return format, e
}
