package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"

	"glesha/archive"
	"glesha/database/model"
	L "glesha/logger"
)

type chunkCounter struct {
	reader io.Reader
	hash   hash.Hash
	size   int64
	max    int64
}

func (r *chunkCounter) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.hash.Write(p[:n])
	r.size += int64(n)
	if r.max > 0 && r.size > r.max {
		return n, fmt.Errorf("backup: chunk exceeds recorded size")
	}
	return n, err
}
func (r *chunkCounter) matches(size int64, digest string) bool {
	return r.size == size && hex.EncodeToString(r.hash.Sum(nil)) == digest
}

type chunkStream struct {
	ctx             context.Context
	service         *Service
	snapshot        model.Snapshot
	from            []string
	password        []byte
	index           int
	remote          io.ReadCloser
	decoded         io.Reader
	plain           io.Reader
	encrypted, body *chunkCounter
	progress        *L.Progress
}

func validateChunks(v model.Snapshot) error {
	if len(v.Chunks) == 0 || len(v.Chunks) > 65536 {
		return fmt.Errorf("backup: invalid chunk count")
	}
	var offset int64
	for i, c := range v.Chunks {
		if c.Index != i || c.Offset != offset || c.PlainSize <= 0 || c.Size <= 0 || offset > int64(^uint64(0)>>1)-c.PlainSize {
			return fmt.Errorf("backup: invalid chunk sequence")
		}
		for _, digest := range []string{c.Hash, c.PlainHash} {
			b, err := hex.DecodeString(digest)
			if err != nil || len(b) != sha256.Size {
				return fmt.Errorf("backup: invalid chunk checksum")
			}
		}
		offset += c.PlainSize
	}
	return nil
}
func (r *chunkStream) open() error {
	c := r.snapshot.Chunks[r.index]
	var last error
	for _, provider := range r.from {
		for _, l := range c.Locations {
			if l.Provider != provider || l.Status != model.STATUS_COMPLETED {
				continue
			}
			store := r.service.Stores[provider]
			if store == nil {
				continue
			}
			remote, object, err := store.Get(r.ctx, l.Key, l.Version)
			if err != nil {
				last = err
				continue
			}
			if object.Size != c.Size || object.Hash != "" && object.Hash != c.Hash {
				remote.Close()
				last = fmt.Errorf("backup: chunk object identity changed")
				continue
			}
			r.remote = remote
			r.progress = L.StartWorkerProgress(r.ctx, fmt.Sprintf("Downloading chunk %d/%d", r.index+1, len(r.snapshot.Chunks)), c.Size, 1)
			r.encrypted = &chunkCounter{reader: r.progress.Reader(remote), hash: sha256.New(), max: c.Size}
			r.plain, err = archive.Plain(r.ctx, r.encrypted, r.password)
			if err == nil {
				var format string
				r.decoded, format, err = archive.NewDecoder().Decompress(r.ctx, r.plain)
				if err == nil && format != r.snapshot.Compression {
					err = fmt.Errorf("backup: chunk compression differs from catalog")
				}
			}
			if err != nil {
				r.Close()
				return err
			}
			r.body = &chunkCounter{reader: r.decoded, hash: sha256.New(), max: c.PlainSize}
			return nil
		}
	}
	if last == nil {
		last = fmt.Errorf("backup: no readable copy of chunk %d", r.index+1)
	}
	return last
}
func (r *chunkStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for r.index < len(r.snapshot.Chunks) {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if r.remote == nil {
			if err := r.open(); err != nil {
				return 0, err
			}
		}
		c := r.snapshot.Chunks[r.index]
		n, err := r.body.Read(p)
		if r.body.size > c.PlainSize {
			return n, fmt.Errorf("backup: chunk exceeds recorded size")
		}
		if err != io.EOF {
			return n, err
		}
		if !r.body.matches(c.PlainSize, c.PlainHash) {
			return n, fmt.Errorf("backup: chunk plaintext checksum mismatch")
		}
		trailing, e := io.Copy(io.Discard, r.plain)
		if e != nil {
			return n, e
		}
		if trailing != 0 {
			return n, fmt.Errorf("backup: trailing compressed chunk bytes")
		}
		if _, e = io.Copy(io.Discard, r.encrypted); e != nil {
			return n, e
		}
		if !r.encrypted.matches(c.Size, c.Hash) {
			return n, fmt.Errorf("backup: encrypted chunk checksum mismatch")
		}
		if e = r.Close(); e != nil {
			return n, e
		}
		r.index++
		if n > 0 {
			return n, nil
		}
	}
	return 0, io.EOF
}
func (r *chunkStream) Close() error {
	if r.progress != nil {
		r.progress.Finish()
		r.progress = nil
	}
	if closer, ok := r.decoded.(io.Closer); ok {
		closer.Close()
	}
	r.decoded = nil
	if r.remote == nil {
		return nil
	}
	err := r.remote.Close()
	r.remote = nil
	return err
}
func (s *Service) restoreChunks(ctx context.Context, v model.Snapshot, stage string, o RestoreOptions) error {
	if err := validateChunks(v); err != nil {
		return err
	}
	password, err := o.Password(v)
	if err != nil {
		return err
	}
	defer func() {
		for i := range password {
			password[i] = 0
		}
	}()
	from := o.From
	if len(from) == 0 {
		from = s.Set.To
	}
	stream := &chunkStream{ctx: ctx, service: s, snapshot: v, from: from, password: password}
	defer stream.Close()
	var manifest model.Manifest
	if err = archive.ExtractTarInto(ctx, archive.ExtractOptions{StripWrapper: true, Overlay: !v.Full, DeferDirectoryMetadata: true}, stream, stage, &manifest); err != nil {
		return fmt.Errorf("backup: extract chunked snapshot %s: %w", v.ID, err)
	}
	if !sameManifest(manifest, v.Manifest) {
		return fmt.Errorf("backup: archive manifest differs from catalog")
	}
	return nil
}
