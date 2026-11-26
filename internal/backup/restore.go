package backup

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"glesha/internal/archive"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
	"glesha/internal/fsutil"
	"glesha/internal/logger"
)

type RestoreOptions struct {
	ID, Output string
	From       []string
	Password   func(model.Snapshot) ([]byte, error)
}

func (s *Service) Restore(ctx context.Context, o RestoreOptions) error {
	if o.Output == "" || o.Password == nil {
		return fmt.Errorf("backup: restore output and password callback are required")
	}
	output, err := filepath.Abs(o.Output)
	if err != nil {
		return err
	}
	o.Output = output
	o.ID, err = s.RestoreSelection(ctx, o.ID, output)
	if err != nil {
		return err
	}
	if err = s.CheckRestoreDestination(ctx, o.ID, o.Output); err != nil {
		return err
	}
	c, e := s.Catalog(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	target, e := Resolve(ctx, c, o.ID)
	if e != nil {
		return e
	}
	if target.Status != "COMPLETED" {
		return fmt.Errorf("backup: snapshot is not committed")
	}
	chain, e := s.ancestry(ctx, c, target)
	if e != nil {
		return e
	}
	defer chain.Close()
	publication, err := s.readRestorePublication(o.Output)
	resume := err == nil
	switch {
	case resume:
		if target, e = Resolve(ctx, c, publication.Snapshot); e != nil {
			return e
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	default:
		if publication.Stage, e = newRestoreStage(o.Output); e != nil {
			return e
		}
	}
	stage := publication.Stage
	journal := s.restorePublicationPath(o.Output)
	defer func() {
		// while a publication journal exists, the verified stage is what resumes it
		if _, err := os.Lstat(journal); errors.Is(err, os.ErrNotExist) {
			os.RemoveAll(stage)
		}
	}()
	root, e := os.OpenRoot(stage)
	if e != nil {
		return e
	}
	defer root.Close()
	if !resume {
		if e = s.extractChain(ctx, c, chain, root, stage, o); e != nil {
			return e
		}
	}
	if e = applyDirectoryMetadata(ctx, c, root, target.ID); e != nil {
		return e
	}
	if e = s.verifyRestore(ctx, c, target, stage); e != nil {
		return e
	}
	return s.publishRestore(ctx, target, stage, o.Output, journal)
}

// newRestoreStage creates the staging directory beside output, or inside it when
// output exists and the restore merges into it.
func newRestoreStage(output string) (string, error) {
	parent := filepath.Dir(output)
	if _, err := os.Stat(output); err == nil {
		parent = output
	}
	return os.MkdirTemp(parent, ".glesha-restore-*")
}

func (s *Service) extractChain(ctx context.Context, c repository.Cataloger, chain repository.SnapshotSequencer, root *os.Root, stage string, o RestoreOptions) error {
	return chain.EachReverse(ctx, func(v model.Snapshot) error {
		if v.Layout != "" && v.Layout != "single" && v.Layout != "chunked" {
			return fmt.Errorf("backup: unsupported archive layout %q", v.Layout)
		}
		decodedHash, err := hex.DecodeString(v.Hash)
		if err != nil || len(decodedHash) != 32 {
			return fmt.Errorf("backup: invalid archive checksum")
		}
		file := v.File
		if v.Layout != "chunked" {
			if file, err = s.archiveFile(ctx, c, v, o.From); err != nil {
				return err
			}
		}
		if !v.Full {
			if err = pruneOverlay(ctx, c, root, v); err != nil {
				return err
			}
		}
		if v.Layout == "chunked" {
			return s.restoreChunks(ctx, v, stage, o)
		}
		return s.restoreArchive(ctx, v, file, stage, o.Password)
	})
}

// publishRestore moves the verified stage to output. Merging into an existing
// output is journaled, so an interrupted merge resumes on the next restore.
func (s *Service) publishRestore(ctx context.Context, target model.Snapshot, stage, output, journal string) error {
	if _, err := os.Lstat(output); os.IsNotExist(err) {
		return fsutil.PublishDir(stage, output)
	} else if err != nil {
		return err
	}
	if filepath.Dir(stage) != output {
		return fmt.Errorf("backup: restore destination appeared during reconstruction")
	}
	publication := restorePublication{Version: restorePublicationVersion, Snapshot: target.ID, Output: output, Stage: stage}
	if err := s.saveJob(journal, publication); err != nil {
		return err
	}
	if err := fsutil.MergeDir(ctx, stage, output, journal+".merge"); err != nil {
		return fmt.Errorf("backup: restore publication incomplete; repeat restore to resume; verified staging at %s: %w", stage, err)
	}
	return os.Remove(journal)
}

// archiveFile returns a local file holding v's verified archive: the original
// output, the payload cache, or a fresh download into the cache.
func (s *Service) archiveFile(ctx context.Context, c repository.Cataloger, v model.Snapshot, from []string) (string, error) {
	if hash, size, err := archive.Hash(ctx, v.File); err == nil && hash == v.Hash && size == v.Size {
		return v.File, nil
	}
	locations, err := c.Locations(ctx, v.ID)
	if err != nil {
		return "", err
	}
	file := filepath.Join(s.Directory, "payload-"+v.Hash+".gpg")
	if hash, size, err := archive.Hash(ctx, file); err == nil && hash == v.Hash && size == v.Size {
		return file, nil
	}
	if _, err := os.Stat(file); err == nil {
		return "", fmt.Errorf("backup: cached archive checksum mismatch; preserve and remove the cache before retrying")
	}
	order := from
	if len(order) == 0 {
		order = s.Set.To
	}
	return file, s.downloadArchive(ctx, v, locations, order, file)
}

// downloadArchive copies v's archive to file from the first remote in order that
// has a completed, matching copy.
func (s *Service) downloadArchive(ctx context.Context, v model.Snapshot, locations []model.Location, order []string, file string) error {
	var last error
	for _, provider := range order {
		for _, l := range locations {
			if l.Provider != provider || l.Status != "COMPLETED" {
				continue
			}
			store := s.Stores[provider]
			if store == nil {
				continue
			}
			r, _, err := store.Get(ctx, l.Key, l.Version)
			if err != nil {
				last = s.remoteError(ctx, provider, err)
				continue
			}
			out, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				r.Close()
				return err
			}
			progress := logger.StartWorkerProgress(ctx, "Downloading for restore", v.Size, 1)
			_, err = copyContext(ctx, out, progress.Reader(r))
			progress.Finish()
			r.Close()
			if closeErr := out.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				os.Remove(file)
				last = s.remoteError(ctx, provider, err)
				continue
			}
			hash, size, err := archive.Hash(ctx, file)
			if err != nil || hash != v.Hash || size != v.Size {
				os.Remove(file)
				last = s.remoteError(ctx, provider, fmt.Errorf("backup: archive checksum mismatch"))
				continue
			}
			return nil
		}
	}
	if last != nil {
		return last
	}
	return fmt.Errorf("backup: no readable copy of %s", v.ID)
}

// pruneOverlay removes paths an incremental snapshot deleted or replaced, before
// its archive is extracted over the staging tree.
func pruneOverlay(ctx context.Context, c repository.Cataloger, root *os.Root, v model.Snapshot) error {
	if err := c.EachEntry(ctx, v.Parent, func(entry model.Entry) error {
		_, err := c.Entry(ctx, v.ID, entry.Path)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err = archive.SafeName(entry.Path); err != nil {
				return err
			}
			return root.RemoveAll(entry.Path)
		}
		return err
	}); err != nil {
		return err
	}
	return c.EachEntry(ctx, v.ID, func(entry model.Entry) error {
		if entry.Archive != v.ID {
			return nil
		}
		info, err := root.Lstat(entry.Path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type == "directory" && info.IsDir() {
			return nil
		}
		return root.RemoveAll(entry.Path)
	})
}

// applyDirectoryMetadata sets ownership, permissions and times on restored
// directories, after their contents are in place.
func applyDirectoryMetadata(ctx context.Context, c repository.Cataloger, root *os.Root, snapshot string) error {
	return c.EachDirectory(ctx, snapshot, func(entry model.Entry) error {
		if err := fsutil.SetOwner(root, entry.Path, entry.UID, entry.GID, false); err != nil {
			return err
		}
		if err := root.Chmod(entry.Path, fsutil.Permissions(entry.Mode)); err != nil {
			return err
		}
		t := time.Unix(0, entry.ModTime)
		return root.Chtimes(entry.Path, t, t)
	})
}

// verifyRestore inventories the staging tree and compares every path with the catalog.
func (s *Service) verifyRestore(ctx context.Context, c repository.Cataloger, target model.Snapshot, stage string) error {
	temp, err := os.CreateTemp(s.Directory, ".glesha-verify-*")
	if err != nil {
		return err
	}
	temp.Close()
	defer os.Remove(temp.Name())
	actual, err := repository.NewCatalogRepository(ctx, temp.Name())
	if err != nil {
		return err
	}
	defer actual.Close()
	for _, r := range target.Roots {
		if r.Name == "" || filepath.Base(r.Name) != r.Name || r.Name == "." || r.Name == ".." {
			return fmt.Errorf("backup: unsafe source-root mapping")
		}
	}
	if err = archive.Inventory(ctx, actual, "verify", []model.Root{{Path: stage}}, nil, s.Budget.HashWorkers); err != nil {
		return err
	}
	count, want := 0, 0
	if err = actual.EachEntry(ctx, "verify", func(entry model.Entry) error {
		count++
		expected, err := c.Entry(ctx, target.ID, entry.Path)
		if err != nil {
			return err
		}
		if entry.Type != expected.Type || !fsutil.SamePortableMetadata(entry.Mode, entry.ModTime, expected.Mode, expected.ModTime) || entry.Hash != expected.Hash || entry.Link != expected.Link {
			return fmt.Errorf("backup: restored metadata or content differs at %s", entry.Path)
		}
		return nil
	}); err != nil {
		return err
	}
	if err = c.EachEntry(ctx, target.ID, func(model.Entry) error { want++; return nil }); err != nil {
		return err
	}
	if count != want {
		return fmt.Errorf("backup: restored inventory differs")
	}
	return nil
}

func (s *Service) restoreArchive(ctx context.Context, v model.Snapshot, file, stage string, passwordFor func(model.Snapshot) ([]byte, error)) error {
	password, e := passwordFor(v)
	if e != nil {
		return e
	}
	var manifest model.Manifest
	format, e := archive.ExtractInto(ctx, archive.ExtractOptions{Input: file, Password: password, StripWrapper: true, Overlay: !v.Full, DeferDirectoryMetadata: true}, stage, &manifest)
	for j := range password {
		password[j] = 0
	}
	if e != nil {
		return fmt.Errorf("backup: extract snapshot %s: %w", v.ID, e)
	}
	if format != v.Compression {
		return fmt.Errorf("backup: archive compression differs from catalog")
	}
	payloadID := v.ID
	if v.PayloadID != "" {
		payloadID = v.PayloadID
	}
	if manifest.ID != "" && manifest.ID != payloadID {
		return fmt.Errorf("backup: archive manifest identity mismatch")
	}
	if manifest.ID != "" && (manifest.Full != v.Full || v.PayloadID == "" && !sameManifest(manifest, v.Manifest)) {
		return fmt.Errorf("backup: archive manifest differs from catalog")
	}
	return nil
}

func (s *Service) localArchive(ctx context.Context, v model.Snapshot) (string, bool) {
	if v.Layout == "chunked" {
		return "", false
	}
	if decoded, err := hex.DecodeString(v.Hash); err != nil || len(decoded) != 32 {
		return "", false
	}
	for _, file := range []string{v.File, filepath.Join(s.Directory, "payload-"+v.Hash+".gpg")} {
		hash, size, err := archive.Hash(ctx, file)
		if err == nil && hash == v.Hash && size == v.Size {
			return file, true
		}
	}
	return "", false
}

func (s *Service) RestoreNeedsDownload(ctx context.Context, id, output string) (bool, error) {
	output, err := filepath.Abs(output)
	if err != nil {
		return false, err
	}
	jobFile := s.restoreJobPath(output)
	if b, err := os.ReadFile(jobFile); err == nil {
		var job restoreJob
		if err = json.Unmarshal(b, &job); err != nil {
			return false, err
		}
		id = job.Snapshot
	} else if !os.IsNotExist(err) {
		return false, err
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return false, err
	}
	defer c.Close()
	v, err := Resolve(ctx, c, id)
	if err != nil {
		return false, err
	}
	chain, err := s.ancestry(ctx, c, v)
	if err != nil {
		return false, err
	}
	defer chain.Close()
	needed := false
	err = chain.EachReverse(ctx, func(v model.Snapshot) error { _, ok := s.localArchive(ctx, v); needed = needed || !ok; return nil })
	return needed, err
}
