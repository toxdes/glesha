package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"glesha/archive"
	"glesha/cloud"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
)

func (s *Service) resumeRun(ctx context.Context, o RunOptions) (model.Snapshot, error) {
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return model.Snapshot{}, err
	}
	defer p.Close()
	operation, err := p.GetMeta(ctx, "operation")
	if err != nil {
		return model.Snapshot{}, err
	}
	if operation != "run" {
		return model.Snapshot{}, fmt.Errorf("%w: %s; retry that operation or catalog abandon", ErrPending, operation)
	}
	b, err := p.GetMeta(ctx, "request")
	if err != nil {
		return model.Snapshot{}, err
	}
	var r runRequest
	if err = json.Unmarshal([]byte(b), &r); err != nil {
		return model.Snapshot{}, err
	}
	if (o.ChunkedSet || o.Chunked) && o.Chunked != r.Chunked || o.SpoolMax > 0 && o.SpoolMax != r.SpoolMax || len(o.To) > 0 && strings.Join(o.To, ",") != strings.Join(r.To, ",") || o.Incremental && !r.Incremental || o.Full && r.Incremental || o.Compression != "" && o.Compression != r.Compression || o.Level > 0 && o.Level != r.Level || o.Class != "" && o.Class != r.Class || o.Mode != "" && o.Mode != r.Mode || o.Prefix != "" && o.Prefix != r.Prefix {
		return model.Snapshot{}, fmt.Errorf("backup: options conflict with pending run")
	}
	id, err := p.GetMeta(ctx, "pending_snapshot")
	if err != nil {
		return model.Snapshot{}, err
	}
	v, err := p.Snapshot(ctx, id)
	if err != nil {
		return v, err
	}
	if o.Output != "" && o.Output != v.File {
		return v, fmt.Errorf("backup: output conflicts with pending run")
	}
	if r.Chunked {
		if o.KeepArchive || o.Output != "" {
			return v, fmt.Errorf("backup: chunked mode cannot retain a complete local archive")
		}
		if v.Hash == "" {
			if len(o.Password) == 0 {
				return v, fmt.Errorf("backup: interrupted chunked run requires its original passphrase; repeat run")
			}
			if err = s.createChunks(ctx, p, &v, r, o.Password); err != nil {
				return v, err
			}
		}
		return v, s.finishRun(ctx, p, &v)
	}
	if o.KeepArchive {
		if err = retainRunArchive(ctx, p); err != nil {
			return v, err
		}
		r.AutoCleanup = false
	}
	if v.Hash == "" && v.ReadyFile == "" {
		if len(o.Password) == 0 {
			return v, fmt.Errorf("backup: interrupted archive creation requires a passphrase; repeat run")
		}
		p.Close()
		if err = os.Rename(s.pending(), filepath.Join(s.Directory, "interrupted-"+newID()+".db")); err != nil {
			return v, err
		}
		return s.Run(ctx, RunOptions{To: r.To, Incremental: r.Incremental, Output: v.File, autoCleanup: r.AutoCleanup, Compression: r.Compression, Class: r.Class, Mode: r.Mode, Level: r.Level, Prefix: r.Prefix, Password: o.Password, Confirm: o.Confirm})
	}
	if v.ReadyFile != "" {
		hash, size, err := archive.Hash(ctx, v.ReadyFile)
		if err == nil && hash == v.Hash && size == v.Size {
			if err = file_io.Publish(v.ReadyFile, v.File); err != nil {
				return v, err
			}
			v.ReadyFile = ""
			if err = p.PutSnapshot(ctx, v); err != nil {
				return v, err
			}
		}
	}
	return v, s.finishRun(ctx, p, &v)
}
func (s *Service) finishRun(ctx context.Context, c repository.Cataloger, v *model.Snapshot) error {
	if v.Layout == "chunked" {
		return s.finishChunked(ctx, c, v)
	}
	if err := s.checkCatalogSecret(ctx, c); err != nil {
		return err
	}
	b, err := c.GetMeta(ctx, "request")
	if err != nil {
		return err
	}
	var request runRequest
	if err = json.Unmarshal([]byte(b), &request); err != nil {
		return err
	}
	local, _ := os.Lstat(v.File)
	locations, err := c.Locations(ctx, v.ID)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	verified := false
	for _, remote := range strings.Split(v.Destinations, ",") {
		done := false
		for _, l := range locations {
			if l.Provider == remote && l.Status == model.STATUS_COMPLETED {
				done = true
			}
		}
		if done {
			continue
		}
		if !verified {
			hash, size, err := archive.Hash(L.WithProgressLabel(ctx, "Checking archive before upload"), v.File)
			if err != nil || hash != v.Hash || size != v.Size {
				return fmt.Errorf("backup: completed encrypted archive is unavailable or changed; pending work retained")
			}
			verified = true
		}
		store := s.Stores[remote]
		if store == nil {
			return fmt.Errorf("backup: pending remote is unavailable")
		}
	}
	for _, remote := range strings.Split(v.Destinations, ",") {
		done := false
		for _, l := range locations {
			if l.Provider == remote && l.Status == model.STATUS_COMPLETED {
				done = true
			}
		}
		if done {
			continue
		}
		store := s.Stores[remote]
		class := v.InitialClass
		if s.Kinds[remote] != "aws" {
			class = "STANDARD"
		}
		wg.Add(1)
		go func(remote, class string, store cloud.Store) {
			defer wg.Done()
			key := s.payloadKey(*v)
			h, err := cloud.Create(ctx, store, key, v.File, class)
			if err == nil {
				l := uploadedLocation(v.ID, remote, h)
				l.InitialClass = class
				err = c.PutLocation(ctx, l)
			}
			if err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
		}(remote, class, store)
	}
	wg.Wait()
	if len(failures) > 0 {
		return fmt.Errorf("%w: upload failed; encrypted archive retained: %v", ErrPending, failures[0])
	}
	wrapper, err := c.GetMeta(ctx, "archive_wrapper")
	if err != nil {
		return err
	}
	if wrapper == "" {
		wrapper = archive.Stem(v.File)
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
	if request.AutoCleanup && local != nil {
		// publication-only retries may not have checked local bytes
		if !verified {
			hash, size, err := archive.Hash(L.WithProgress(ctx, nil), v.File)
			if err != nil || hash != v.Hash || size != v.Size {
				L.Warn("Archive retained: local file changed or unavailable.")
				return nil
			}
		}
		if err := file_io.RemoveUnchanged(v.File, local); err != nil {
			L.Warn(fmt.Sprintf("Archive retained: %v", err))
		}
	}
	return nil
}
func (s *Service) payloadKey(v model.Snapshot) string {
	return "glesha/archives/" + s.Set.ID + "/" + v.ID + archive.Extension(v.Compression) + ".gpg"
}

func uploadedLocation(snapshot, remote string, h cloud.Object) model.Location {
	l := Location(snapshot, remote, h)
	t := now()
	l.UploadedAt = &t
	if h.ModifiedAt != nil {
		l.UploadedAt = h.ModifiedAt
	}
	return l
}
func Location(snapshot, remote string, h cloud.Object) model.Location {
	t := now()
	return model.Location{Snapshot: snapshot, Provider: remote, Key: h.Key, Version: h.Version, ETag: h.ETag, CurrentClass: h.Class, ObservedClass: h.Class, InitialClass: h.Class, Status: model.STATUS_COMPLETED, Verification: "sha256", ObservedAt: &t, Cold: h.Cold, Restore: h.Restore, ArchiveStatus: h.ArchiveStatus}
}

func (s *Service) NeedsArchive(ctx context.Context) (bool, error) {
	if !s.Pending() {
		return true, nil
	}
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return false, err
	}
	defer p.Close()
	operation, err := p.GetMeta(ctx, "operation")
	if err != nil {
		return false, err
	}
	if operation != "run" {
		return false, nil
	}
	id, err := p.GetMeta(ctx, "pending_snapshot")
	if err != nil {
		return false, err
	}
	v, err := p.Snapshot(ctx, id)
	return v.Hash == "" && v.ReadyFile == "", err
}
