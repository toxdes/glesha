package archive

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	bz "github.com/dsnet/compress/bzip2"
	"github.com/ulikunitz/xz"

	"glesha/crypt"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
	"glesha/memory"
)

const DeletionsName = ".glesha-deletions-v1.jsonl"
const ManifestName = ".glesha-manifest-v1.json"

type Options struct {
	Timestamp                 time.Time
	Sources                   file_io.SourceReader
	Encoder                   Encoder
	Encryptor                 crypt.Encryptor
	Output, Compression, Mode string
	Level                     int
	Password                  []byte
	Budget                    memory.Budget
	Manifest                  model.Manifest
	Catalog                   repository.Cataloger
	OnReady                   func(context.Context, string, int64, string) error
	Exclusions                []string
}
type spill struct {
	chunks   [][]byte
	length   int64
	file     *os.File
	limit    int64
	mode     string
	streamed bool
}

func (s *spill) Write(p []byte) (int, error) {
	if !s.streamed && s.length+int64(len(p)) > s.limit {
		if s.mode == "memory" {
			return 0, fmt.Errorf("archive: encrypted archive exceeds memory budget; use auto or stream")
		}
		for _, chunk := range s.chunks {
			if _, e := s.file.Write(chunk); e != nil {
				return 0, e
			}
		}
		s.chunks = nil
		s.length = 0
		s.streamed = true
	}
	if s.streamed {
		return s.file.Write(p)
	}
	n := len(p)
	for len(p) > 0 {
		if len(s.chunks) == 0 || len(s.chunks[len(s.chunks)-1]) == cap(s.chunks[len(s.chunks)-1]) {
			s.chunks = append(s.chunks, make([]byte, 0, 64*1024))
		}
		i := len(s.chunks) - 1
		remaining := cap(s.chunks[i]) - len(s.chunks[i])
		if remaining > len(p) {
			remaining = len(p)
		}
		s.chunks[i] = append(s.chunks[i], p[:remaining]...)
		s.length += int64(remaining)
		p = p[remaining:]
	}
	return n, nil
}

type Encoder interface {
	Compress(context.Context, io.Writer, string, int) (io.WriteCloser, error)
}

type encoder struct{}

func NewEncoder() Encoder { return encoder{} }

func Compress(w io.Writer, format string, level int) (io.WriteCloser, error) {
	return NewEncoder().Compress(context.Background(), w, format, level)
}

func (encoder) Compress(ctx context.Context, w io.Writer, format string, level int) (io.WriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch format {
	case "gzip":
		return gzip.NewWriterLevel(w, level)
	case "bzip2":
		return bz.NewWriter(w, &bz.WriterConfig{Level: level})
	case "xz":
		dictionary, err := xzDictionary(level)
		if err != nil {
			return nil, err
		}
		return (xz.WriterConfig{DictCap: dictionary, CheckSum: xz.CRC64}).NewWriter(w)
	default:
		return nil, fmt.Errorf("archive: unknown compression %q", format)
	}
}
func Extension(format string) string {
	if format == "xz" {
		return ".tar.xz"
	}
	if format == "gzip" {
		return ".tar.gz"
	}
	return ".tar.bz2"
}
func Create(ctx context.Context, o Options) (hash string, size int64, err error) {
	if o.Compression == "xz" {
		dictionary, e := xzDictionary(o.Level)
		if e != nil {
			return "", 0, e
		}
		o.Budget, e = memory.Reserve(o.Budget, 5*int64(dictionary)+12*memory.MiB)
		if e != nil {
			return "", 0, e
		}
	}
	if o.Sources == nil {
		o.Sources = file_io.NewSourceReader()
	}
	if o.Encoder == nil {
		o.Encoder = NewEncoder()
	}
	if o.Encryptor == nil {
		o.Encryptor = crypt.NewCipher()
	}
	if _, e := os.Lstat(o.Output); !os.IsNotExist(e) {
		return "", 0, fmt.Errorf("archive: output already exists or cannot be inspected")
	}
	if o.Timestamp.IsZero() {
		o.Timestamp = time.Now().UTC()
	}
	stem := Stem(o.Output)
	normalized, e := SafeName(stem)
	if e != nil || normalized != stem || path.Base(stem) != stem {
		return "", 0, fmt.Errorf("archive: invalid stem")
	}
	if o.Mode != "auto" && o.Mode != "memory" && o.Mode != "stream" {
		return "", 0, fmt.Errorf("archive: invalid archive mode")
	}
	if err = prepareHardlinks(ctx, o); err != nil {
		return "", 0, err
	}
	total, e := archiveInputBytes(ctx, o)
	if e != nil {
		return "", 0, e
	}
	f, e := file_io.Temp(filepath.Dir(o.Output))
	if e != nil {
		return "", 0, e
	}
	retainReady := false
	defer func() {
		f.Close()
		if !retainReady {
			os.Remove(f.Name())
		}
	}()
	s := &spill{file: f, limit: o.Budget.Buffer, mode: o.Mode, streamed: o.Mode == "stream"}
	enc, e := o.Encryptor.Encrypt(ctx, s, o.Password)
	if e != nil {
		return "", 0, e
	}
	comp, e := o.Encoder.Compress(ctx, enc, o.Compression, o.Level)
	if e != nil {
		enc.Close()
		return "", 0, e
	}
	progress := L.StartWorkerProgress(ctx, "Archiving", total, 1)
	defer progress.Finish()
	tw := tar.NewWriter(comp)
	err = writeTar(ctx, o, stem, tw, progress)
	progress.Finish()
	var finalizing *L.Progress
	if err == nil {
		finalizing = L.StartProgress(ctx, "Finalizing archive", -1)
	}
	defer finalizing.Finish()
	err = joinClose(err, tw.Close())
	err = joinClose(err, comp.Close())
	err = joinClose(err, enc.Close())
	if err != nil {
		return "", 0, err
	}
	if !s.streamed {
		for _, chunk := range s.chunks {
			if _, err = f.Write(chunk); err != nil {
				return "", 0, err
			}
		}
	}
	if err = validateSources(ctx, o.Sources, o.Catalog, o.Manifest.ID, o.Output); err != nil {
		return "", 0, err
	}
	if err = validateTree(ctx, o.Sources, o.Catalog, o.Manifest.ID, o.Manifest.Roots, append(o.Exclusions, o.Output, f.Name())); err != nil {
		return "", 0, err
	}
	if err = f.Sync(); err != nil {
		return "", 0, err
	}
	if err = f.Close(); err != nil {
		return "", 0, err
	}
	hash, size, err = Hash(ctx, f.Name())
	if err != nil {
		return "", 0, err
	}
	if err = ctx.Err(); err != nil {
		return "", 0, err
	}
	if o.OnReady != nil {
		if err = o.OnReady(ctx, hash, size, f.Name()); err != nil {
			return "", 0, err
		}
		retainReady = true
	}
	err = file_io.Publish(f.Name(), o.Output)
	return
}
func joinClose(first, next error) error {
	if first != nil {
		return first
	}
	return next
}
func Stem(filename string) string {
	b := filepath.Base(filename)
	for _, s := range []string{".tar.xz.gpg", ".tar.bz2.gpg", ".tar.gz.gpg", ".tar.xz", ".tar.bz2", ".tar.gz"} {
		if strings.HasSuffix(b, s) {
			return strings.TrimSuffix(b, s)
		}
	}
	return strings.TrimSuffix(b, filepath.Ext(b))
}

func prepareHardlinks(ctx context.Context, o Options) error {
	if o.Manifest.Parent != "" {
		if e := o.Catalog.EachEntry(ctx, o.Manifest.ID, func(v model.Entry) error {
			if v.Type != "hardlink" {
				return nil
			}
			changed, e := Changed(ctx, o.Catalog, o.Manifest.ID, o.Manifest.Parent, v)
			if e != nil || !changed {
				return e
			}
			target, e := o.Catalog.Entry(ctx, o.Manifest.ID, v.Link)
			if e != nil {
				return e
			}
			if target.Type != "file" {
				return fmt.Errorf("archive: hardlink target is not a regular file %s", v.Link)
			}
			target.Archive = o.Manifest.ID
			return o.Catalog.PutEntry(ctx, o.Manifest.ID, target)
		}); e != nil {
			return e
		}
		// rebind inherited aliases when their target is replaced
		if e := o.Catalog.EachEntry(ctx, o.Manifest.ID, func(v model.Entry) error {
			if v.Type != "hardlink" {
				return nil
			}
			target, e := o.Catalog.Entry(ctx, o.Manifest.ID, v.Link)
			if e != nil {
				return e
			}
			if target.Archive != o.Manifest.ID {
				return nil
			}
			v.Archive = o.Manifest.ID
			return o.Catalog.PutEntry(ctx, o.Manifest.ID, v)
		}); e != nil {
			return e
		}
	}
	return nil
}

func writeTar(ctx context.Context, o Options, stem string, tw *tar.Writer, progress *L.Progress) error {
	if e := tw.WriteHeader(&tar.Header{Name: stem + "/", Typeflag: tar.TypeDir, Mode: 0700, ModTime: o.Timestamp}); e != nil {
		return e
	}
	b, e := json.Marshal(o.Manifest)
	if len(b) > 4<<20 {
		return fmt.Errorf("archive: manifest exceeds 4MiB limit")
	}
	if e != nil {
		return e
	}
	if e = tw.WriteHeader(&tar.Header{Name: path.Join(stem, ManifestName), Mode: 0600, Size: int64(len(b))}); e != nil {
		return e
	}
	if _, e = tw.Write(b); e != nil {
		return e
	}
	if o.Manifest.DeletionsMember != "" {
		eachDeleted := func(fn func(string) error) error {
			return o.Catalog.EachEntry(ctx, o.Manifest.Parent, func(v model.Entry) error {
				_, err := o.Catalog.Entry(ctx, o.Manifest.ID, v.Path)
				if errors.Is(err, sql.ErrNoRows) {
					return fn(v.Path)
				}
				return err
			})
		}
		var size int64
		if err := eachDeleted(func(name string) error { b, err := json.Marshal(name); size += int64(len(b)) + 1; return err }); err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: path.Join(stem, DeletionsName), Mode: 0600, Size: size}); err != nil {
			return err
		}
		encoder := json.NewEncoder(tw)
		if err := eachDeleted(func(name string) error { return encoder.Encode(name) }); err != nil {
			return err
		}
	}
	batch := make([]model.Entry, 0, 128)
	saveEntry := func(v model.Entry) error {
		batch = append(batch, v)
		if len(batch) < cap(batch) {
			return nil
		}
		err := o.Catalog.PutEntries(ctx, o.Manifest.ID, batch)
		batch = batch[:0]
		return err
	}
	err := o.Catalog.EachEntry(ctx, o.Manifest.ID, func(v model.Entry) error {
		include, e := includedEntry(ctx, o, v)
		if e != nil {
			return e
		}
		if !include {
			old, e := o.Catalog.Entry(ctx, o.Manifest.Parent, v.Path)
			if e != nil {
				return e
			}
			v.Archive, v.Member = old.Archive, old.Member
			v.PAX = old.PAX
			return saveEntry(v)
		}
		info, e := o.Sources.Lstat(ctx, v.Source)
		if e != nil {
			return e
		}
		check := v
		if v.Type == "directory" && Within(filepath.Dir(o.Output), v.Source) {
			check.ModTime = info.ModTime().UnixNano()
		}
		if !unchanged(check, info) {
			return fmt.Errorf("archive: source changed before archival %s", v.Source)
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, e = o.Sources.Readlink(ctx, v.Source)
			if e != nil {
				return e
			}
			if link != v.Link {
				return fmt.Errorf("archive: symlink changed %s", v.Source)
			}
		}
		h, e := tar.FileInfoHeader(info, link)
		if e != nil {
			return e
		}
		h.Name = path.Join(stem, v.Path)
		h.Format = tar.FormatPAX
		h.ModTime = time.Unix(0, v.ModTime)
		if v.Type == "hardlink" {
			h.Typeflag = tar.TypeLink
			h.Linkname = path.Join(stem, v.Link)
			h.Size = 0
		}

		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if h.Typeflag == tar.TypeReg {
			f, e := o.Sources.Open(ctx, v.Source)
			if e != nil {
				return e
			}
			hash := sha256.New()
			n, e := io.CopyBuffer(io.MultiWriter(tw, hash), file_io.ContextReader{Ctx: ctx, Reader: progress.Reader(f)}, make([]byte, 64*1024))
			after, se := f.Stat()
			sourceAfter, pathErr := o.Sources.Lstat(ctx, v.Source)
			f.Close()
			if e != nil {
				return e
			}
			if pathErr != nil || se != nil || !os.SameFile(info, sourceAfter) || !unchanged(v, after) || n != v.Size || hex.EncodeToString(hash.Sum(nil)) != v.Hash {
				return fmt.Errorf("archive: included bytes differ from inventory %s", v.Source)
			}
		}
		v.Archive = o.Manifest.ID
		v.Member = h.Name
		return saveEntry(v)
	})
	if err != nil {
		return err
	}
	if len(batch) > 0 {
		return o.Catalog.PutEntries(ctx, o.Manifest.ID, batch)
	}
	return nil
}
