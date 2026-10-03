package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"glesha/archive"
	"glesha/cloud"
	"glesha/crypt"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
)

const DefaultSpoolMax int64 = 128 << 20
const MinimumSpoolMax int64 = 2 << 20
const maximumChunks = 65536

func ParseSpoolSize(value string) (int64, error) {
	value = strings.TrimSpace(value)
	for _, unit := range []struct {
		name string
		size int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1000000000}, {"MB", 1000000}, {"KB", 1000}, {"G", 1000000000}, {"M", 1000000}, {"K", 1000}, {"B", 1}} {
		if strings.HasSuffix(value, unit.name) {
			number, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, unit.name)), 64)
			size := number * float64(unit.size)
			if err != nil || !(size >= 1 && size < float64(1<<62)) {
				return 0, fmt.Errorf("backup: invalid spool size %q", value)
			}
			return int64(size), nil
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("backup: invalid spool size %q", value)
	}
	return n, nil
}

type spoolWriter struct {
	file      *os.File
	remaining int64
}

func (w *spoolWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("backup: encrypted chunk exceeds spool limit")
	}
	n, err := w.file.Write(p)
	w.remaining -= int64(n)
	return n, err
}

type chunkWriter struct {
	ctx                   context.Context
	service               *Service
	catalog               repository.Cataloger
	snapshot              *model.Snapshot
	request               runRequest
	password              []byte
	directory             string
	limit                 int64
	index                 int
	offset, count         int64
	plain                 hash.Hash
	file                  *os.File
	compressor, encryptor io.WriteCloser
}

func (w *chunkWriter) begin() error {
	if w.index >= maximumChunks {
		return fmt.Errorf("backup: chunk count exceeds %d", maximumChunks)
	}
	w.plain = sha256.New()
	w.count = 0
	if w.index < len(w.snapshot.Chunks) {
		return nil
	}
	f, err := file_io.Temp(w.directory)
	if err != nil {
		return err
	}
	w.file = f
	w.encryptor, err = crypt.NewCipher().Encrypt(w.ctx, &spoolWriter{file: f, remaining: w.request.SpoolMax}, w.password)
	if err != nil {
		return err
	}
	w.compressor, err = archive.NewEncoder().Compress(w.ctx, w.encryptor, w.snapshot.Compression, w.request.Level)
	return err
}
func (w *chunkWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		if err := w.ctx.Err(); err != nil {
			return total, err
		}
		if w.plain == nil {
			if err := w.begin(); err != nil {
				return total, err
			}
		}
		n := len(p)
		if int64(n) > w.limit-w.count {
			n = int(w.limit - w.count)
		}
		if w.compressor != nil {
			if _, err := w.compressor.Write(p[:n]); err != nil {
				return total, err
			}
		}
		w.plain.Write(p[:n])
		w.count += int64(n)
		total += n
		p = p[n:]
		if w.count == w.limit {
			if err := w.complete(); err != nil {
				return total, err
			}
		}
	}
	return total, nil
}
func (w *chunkWriter) abort() {
	if w.compressor != nil {
		w.compressor.Close()
	}
	if w.encryptor != nil {
		w.encryptor.Close()
	}
	if w.file != nil {
		w.file.Close()
		os.Remove(w.file.Name())
	}
}
func (w *chunkWriter) complete() error {
	plainHash := hex.EncodeToString(w.plain.Sum(nil))
	if w.index < len(w.snapshot.Chunks) {
		ch := w.snapshot.Chunks[w.index]
		if ch.Index != w.index || ch.Offset != w.offset || ch.PlainSize != w.count || ch.PlainHash != plainHash {
			return fmt.Errorf("backup: source changed since interrupted chunked run; abandon pending work explicitly before restarting")
		}
	} else {
		if err := w.compressor.Close(); err != nil {
			return err
		}
		w.compressor = nil
		if err := w.encryptor.Close(); err != nil {
			return err
		}
		w.encryptor = nil
		if err := w.file.Sync(); err != nil {
			return err
		}
		if err := w.file.Close(); err != nil {
			return err
		}
		digest, size, err := archive.Hash(L.WithProgress(w.ctx, nil), w.file.Name())
		if err != nil {
			return err
		}
		ch := model.Chunk{Index: w.index, Offset: w.offset, PlainSize: w.count, PlainHash: plainHash, Size: size, Hash: digest, File: w.file.Name(), Key: fmt.Sprintf("glesha/archives/%s/%s/chunks/%06d-%s%s.gpg", w.service.Set.ID, w.snapshot.ID, w.index, newID(), archive.Extension(w.snapshot.Compression))}
		w.snapshot.Chunks = append(w.snapshot.Chunks, ch)
		if err := w.catalog.PutSnapshot(w.ctx, *w.snapshot); err != nil {
			return err
		}
		w.file = nil
	}
	if err := w.service.uploadChunk(w.ctx, w.catalog, w.snapshot, w.index); err != nil {
		return err
	}
	w.offset += w.count
	w.index++
	w.plain = nil
	return nil
}
func (s *Service) uploadChunk(ctx context.Context, c repository.Cataloger, v *model.Snapshot, index int) error {
	ch := &v.Chunks[index]
	for _, remote := range strings.Split(v.Destinations, ",") {
		done := false
		for _, l := range ch.Locations {
			if l.Provider == remote && l.Status == model.STATUS_COMPLETED {
				done = true
			}
		}
		if done {
			continue
		}
		hash, size, err := archive.Hash(L.WithProgress(ctx, nil), ch.File)
		if err != nil || hash != ch.Hash || size != ch.Size {
			return fmt.Errorf("%w: unfinished chunk spool is unavailable or changed", ErrPending)
		}
		store := s.Stores[remote]
		if store == nil {
			return fmt.Errorf("backup: chunk destination is unavailable")
		}
		class := v.InitialClass
		if s.Kinds[remote] != "aws" {
			class = "STANDARD"
		}
		h, err := cloud.Create(cloud.WithTransferLabel(ctx, fmt.Sprintf("chunk %d", index+1)), store, ch.Key, ch.File, class)
		if err != nil {
			return fmt.Errorf("%w: chunk upload: %v", ErrPending, err)
		}
		if h.Size != ch.Size || h.Hash != "" && h.Hash != ch.Hash {
			return fmt.Errorf("backup: uploaded chunk identity differs")
		}
		l := uploadedLocation(v.ID, remote, h)
		l.InitialClass = class
		ch.Locations = append(ch.Locations, l)
		if err = c.PutSnapshot(ctx, *v); err != nil {
			return err
		}
	}
	if ch.File != "" {
		if err := os.Remove(ch.File); err != nil && !os.IsNotExist(err) {
			return err
		}
		ch.File = ""
		return c.PutSnapshot(ctx, *v)
	}
	return nil
}
func (s *Service) checkChunkPassword(ctx context.Context, c repository.Cataloger, id string, password []byte) error {
	probe, err := c.GetMeta(ctx, "chunk_password_probe")
	if err != nil {
		return err
	}
	if probe == "" {
		var out bytes.Buffer
		w, err := crypt.NewCipher().Encrypt(ctx, &out, password)
		if err != nil {
			return err
		}
		if _, err = w.Write([]byte(id)); err != nil {
			return err
		}
		if err = w.Close(); err != nil {
			return err
		}
		return c.SetMeta(ctx, "chunk_password_probe", base64.StdEncoding.EncodeToString(out.Bytes()))
	}
	b, err := base64.StdEncoding.DecodeString(probe)
	if err != nil {
		return err
	}
	r, err := crypt.NewCipher().Decrypt(ctx, bytes.NewReader(b), password)
	if err != nil {
		return fmt.Errorf("backup: original chunked archive passphrase is required")
	}
	actual, err := io.ReadAll(io.LimitReader(r, 1024))
	if err != nil || string(actual) != id {
		return fmt.Errorf("backup: original chunked archive passphrase is required")
	}
	return nil
}
func (s *Service) createChunks(ctx context.Context, c repository.Cataloger, v *model.Snapshot, r runRequest, password []byte) error {
	if r.SpoolMax < MinimumSpoolMax || len(v.Chunks) > maximumChunks {
		return fmt.Errorf("backup: invalid chunked run journal")
	}
	if err := s.checkChunkPassword(ctx, c, v.ID, password); err != nil {
		return err
	}
	directory := filepath.Join(s.Directory, "chunks-"+v.ID)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	// remove only unjournaled application-created spool artifacts
	files, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, f := range files {
		registered := false
		for _, ch := range v.Chunks {
			registered = registered || ch.File == filepath.Join(directory, f.Name())
		}
		if !registered {
			if err = os.Remove(filepath.Join(directory, f.Name())); err != nil {
				return err
			}
		}
	}
	limit := r.SpoolMax / 2
	if limit > 256<<20 {
		limit = 256 << 20
	}
	w := &chunkWriter{ctx: ctx, service: s, catalog: c, snapshot: v, request: r, password: password, directory: directory, limit: limit}
	defer w.abort()
	options := archive.Options{Output: v.File, Compression: v.Compression, Level: r.Level, Password: password, Budget: s.Budget, Manifest: v.Manifest, Catalog: c, Timestamp: v.Created, Exclusions: []string{v.File, filepath.Dir(s.Directory)}}
	if err = archive.StreamTar(ctx, options, w); err != nil {
		return err
	}
	if w.plain != nil {
		if err = w.complete(); err != nil {
			return err
		}
	}
	if w.index != len(v.Chunks) {
		return fmt.Errorf("backup: source changed since interrupted chunked run")
	}
	// the encrypted descriptor is the independently recoverable completion manifest
	descriptor, err := json.Marshal(struct {
		Version  int            `json:"version"`
		Snapshot string         `json:"snapshot"`
		Manifest model.Manifest `json:"manifest"`
		Chunks   []model.Chunk  `json:"chunks"`
	}{1, v.ID, v.Manifest, v.Chunks})
	if err != nil {
		return err
	}
	if int64(len(descriptor))*2+65536 > r.SpoolMax {
		return fmt.Errorf("backup: chunk manifest exceeds spool allowance")
	}
	f, err := file_io.Temp(directory)
	if err != nil {
		return err
	}
	defer f.Close()
	enc, err := crypt.NewCipher().Encrypt(ctx, &spoolWriter{file: f, remaining: r.SpoolMax}, password)
	if err != nil {
		return err
	}
	if _, err = enc.Write(descriptor); err != nil {
		enc.Close()
		return err
	}
	if err = enc.Close(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	digest, size, err := archive.Hash(L.WithProgress(ctx, nil), f.Name())
	if err != nil {
		return err
	}
	v.Hash = digest
	v.Size = size
	for _, ch := range v.Chunks {
		v.Size += ch.Size
	}
	v.ReadyFile = f.Name()
	return c.PutSnapshot(ctx, *v)
}
func (s *Service) finishChunked(ctx context.Context, c repository.Cataloger, v *model.Snapshot) error {
	if err := s.checkCatalogSecret(ctx, c); err != nil {
		return err
	}
	for i := range v.Chunks {
		if err := s.uploadChunk(ctx, c, v, i); err != nil {
			return err
		}
	}
	locations, err := c.Locations(ctx, v.ID)
	if err != nil {
		return err
	}
	for _, remote := range strings.Split(v.Destinations, ",") {
		done := false
		for _, l := range locations {
			done = done || l.Provider == remote && l.Status == model.STATUS_COMPLETED
		}
		if done {
			continue
		}
		class := v.InitialClass
		if s.Kinds[remote] != "aws" {
			class = "STANDARD"
		}
		if s.Stores[remote] == nil {
			return fmt.Errorf("backup: pending chunk destination is unavailable")
		}
		h, err := cloud.Create(cloud.WithTransferLabel(ctx, "chunk manifest"), s.Stores[remote], "glesha/archives/"+s.Set.ID+"/"+v.ID+"/manifest.json.gpg", v.ReadyFile, class)
		if err != nil {
			return fmt.Errorf("%w: chunk manifest upload: %v", ErrPending, err)
		}
		expected := v.Size
		for _, ch := range v.Chunks {
			expected -= ch.Size
		}
		if h.Size != expected || h.Hash != "" && h.Hash != v.Hash {
			return fmt.Errorf("backup: chunk manifest object identity differs")
		}
		l := uploadedLocation(v.ID, remote, h)
		l.InitialClass = class
		if err = c.PutLocation(ctx, l); err != nil {
			return err
		}
	}
	wrapper, err := c.GetMeta(ctx, "archive_wrapper")
	if err != nil {
		return err
	}
	descriptorFile := v.ReadyFile
	v.ReadyFile = ""
	v.File = ""
	if err = c.SetMeta(ctx, "chunk_password_probe", ""); err != nil {
		return err
	}
	if !v.Full {
		s.Set.Authority = strings.Split(v.Destinations, ",")[0]
		s.Set.To = []string{s.Set.Authority}
		if err = s.Registry.Save(ctx, s.Set); err != nil {
			return err
		}
	}
	if err = c.Commit(ctx, *v, wrapper, true); err != nil {
		return err
	}
	v.Status = model.STATUS_COMPLETED
	if err = s.publish(ctx, c); err != nil {
		return err
	}
	if err = s.finishPending(ctx, c); err != nil {
		return err
	}
	os.Remove(descriptorFile)
	os.RemoveAll(filepath.Join(s.Directory, "chunks-"+v.ID))
	return nil
}
