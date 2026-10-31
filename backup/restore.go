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

	"glesha/archive"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
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
	stage := ""
	resume := false
	if publication, err := s.readRestorePublication(o.Output); err == nil {
		stage, resume = publication.Stage, true
		target, e = Resolve(ctx, c, publication.Snapshot)
		if e != nil {
			return e
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		parent := filepath.Dir(o.Output)
		if _, err = os.Stat(o.Output); err == nil {
			parent = o.Output
		}
		stage, e = os.MkdirTemp(parent, ".glesha-restore-*")
		if e != nil {
			return e
		}
	}
	preserve := resume
	defer func() {
		if !preserve {
			os.RemoveAll(stage)
		}
	}()
	root, e := os.OpenRoot(stage)
	if e != nil {
		return e
	}
	defer root.Close()
	if !resume {
		if e = chain.EachReverse(ctx, func(v model.Snapshot) error {
			if v.Layout != "" && v.Layout != "single" && v.Layout != "chunked" {
				return fmt.Errorf("backup: unsupported archive layout %q", v.Layout)
			}
			decodedHash, err := hex.DecodeString(v.Hash)
			if err != nil || len(decodedHash) != 32 {
				return fmt.Errorf("backup: invalid archive checksum")
			}
			file := v.File
			if v.Layout != "chunked" {
				hash, size, he := archive.Hash(ctx, file)
				if he != nil || hash != v.Hash || size != v.Size {
					locations, e := c.Locations(ctx, v.ID)
					if e != nil {
						return e
					}
					file = filepath.Join(s.Directory, "payload-"+v.Hash+".gpg")
					cachedHash, cachedSize, cacheErr := archive.Hash(ctx, file)
					if cacheErr != nil || cachedHash != v.Hash || cachedSize != v.Size {
						if _, err := os.Stat(file); err == nil {
							return fmt.Errorf("backup: cached archive checksum mismatch; preserve and remove the cache before retrying")
						}
						found := false
						order := o.From
						if len(order) == 0 {
							order = s.Set.To
						}
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
								r, _, e := store.Get(ctx, l.Key, l.Version)
								if e != nil {
									last = e
									continue
								}
								out, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
								if e != nil {
									r.Close()
									return e
								}
								progress := L.StartWorkerProgress(ctx, "Downloading for restore", v.Size, 1)
								_, e = copyContext(ctx, out, progress.Reader(r))
								progress.Finish()
								r.Close()
								ce := out.Close()
								if e == nil {
									e = ce
								}
								if e != nil {
									os.Remove(file)
									last = e
									continue
								}
								hash, size, e = archive.Hash(ctx, file)
								if e != nil || hash != v.Hash || size != v.Size {
									os.Remove(file)
									last = fmt.Errorf("backup: archive checksum mismatch")
									continue
								}
								found = true
								break
							}
							if found {
								break
							}
						}
						if !found {
							if last != nil {
								return last
							}
							return fmt.Errorf("backup: no readable copy of %s", v.ID)
						}
					}
				}
			}
			if !v.Full {
				if e = c.EachEntry(ctx, v.Parent, func(entry model.Entry) error {
					_, e := c.Entry(ctx, v.ID, entry.Path)
					if errors.Is(e, sql.ErrNoRows) {
						if _, e = archive.SafeName(entry.Path); e != nil {
							return e
						}
						return root.RemoveAll(entry.Path)
					}
					return e
				}); e != nil {
					return e
				}
				if e = c.EachEntry(ctx, v.ID, func(entry model.Entry) error {
					if entry.Archive != v.ID {
						return nil
					}
					info, e := root.Lstat(entry.Path)
					if os.IsNotExist(e) {
						return nil
					}
					if e != nil {
						return e
					}
					if entry.Type == "directory" && info.IsDir() {
						return nil
					}
					return root.RemoveAll(entry.Path)
				}); e != nil {
					return e
				}
			}
			if v.Layout == "chunked" {
				return s.restoreChunks(ctx, v, stage, o)
			}
			return s.restoreArchive(ctx, v, file, stage, o.Password)
		}); e != nil {
			return e
		}
	}
	if e = c.EachDirectory(ctx, target.ID, func(entry model.Entry) error {
		if e := file_io.SetOwner(root, entry.Path, entry.UID, entry.GID, false); e != nil {
			return e
		}
		if e := root.Chmod(entry.Path, file_io.Permissions(entry.Mode)); e != nil {
			return e
		}
		t := time.Unix(0, entry.ModTime)
		return root.Chtimes(entry.Path, t, t)
	}); e != nil {
		return e
	}
	// inventory the reconstructed tree and compare every path to the historical catalog
	temp, e := os.CreateTemp(s.Directory, ".glesha-verify-*")
	if e != nil {
		return e
	}
	temp.Close()
	defer os.Remove(temp.Name())
	actual, e := repository.NewCatalogRepository(ctx, temp.Name())
	if e != nil {
		return e
	}
	defer actual.Close()
	for _, r := range target.Roots {
		if r.Name == "" || filepath.Base(r.Name) != r.Name || r.Name == "." || r.Name == ".." {
			return fmt.Errorf("backup: unsafe source-root mapping")
		}
	}
	roots := []model.Root{{Path: stage}}
	if e = archive.Inventory(ctx, actual, "verify", roots, nil, s.Budget.HashWorkers); e != nil {
		return e
	}
	count, want := 0, 0
	if e = actual.EachEntry(ctx, "verify", func(entry model.Entry) error {
		count++
		expected, e := c.Entry(ctx, target.ID, entry.Path)
		if e != nil {
			return e
		}
		if entry.Type != expected.Type || !file_io.SamePortableMetadata(entry.Mode, entry.ModTime, expected.Mode, expected.ModTime) || entry.Hash != expected.Hash || entry.Link != expected.Link {
			return fmt.Errorf("backup: restored metadata or content differs at %s", entry.Path)
		}
		return nil
	}); e != nil {
		return e
	}
	if e = c.EachEntry(ctx, target.ID, func(model.Entry) error { want++; return nil }); e != nil {
		return e
	}
	if count != want {
		return fmt.Errorf("backup: restored inventory differs")
	}
	if _, e = os.Lstat(o.Output); os.IsNotExist(e) {
		return file_io.PublishDir(stage, o.Output)
	} else if e != nil {
		return e
	}
	if filepath.Dir(stage) != o.Output {
		return fmt.Errorf("backup: restore destination appeared during reconstruction")
	}
	publication := restorePublication{Version: restorePublicationVersion, Snapshot: target.ID, Output: o.Output, Stage: stage}
	journal := s.restorePublicationPath(o.Output)
	if e = s.saveJob(journal, publication); e != nil {
		return e
	}
	preserve = true
	if e = file_io.MergeDir(ctx, stage, o.Output, journal+".merge"); e != nil {
		return fmt.Errorf("backup: restore publication incomplete; repeat restore to resume; verified staging at %s: %w", stage, e)
	}
	if e = os.Remove(journal); e != nil {
		return e
	}
	preserve = false
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
