package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"glesha/archive"
	"glesha/database/model"
	"glesha/file_io"
)

type CleanupFile struct {
	Path      string
	Size      int64
	Hash      string
	Snapshot  string
	Locations []model.Location
	original  os.FileInfo
}
type CleanupPlan struct {
	Files   []CleanupFile
	Skipped string
}

func (s *Service) CleanupBlock(ctx context.Context) (string, error) {
	if s.Pending() {
		reason := "Pending operation"
		return reason, nil
	}
	if _, err := os.Stat(s.path()); os.IsNotExist(err) {
		reason := "Local catalog unavailable"
		return reason, nil
	} else if err != nil {
		return "", err
	}
	condition, err := s.Condition(ctx)
	if err != nil {
		return "", err
	}
	switch condition.State {
	case STATE_CONFLICT, STATE_FAILED, STATE_PAUSED, STATE_WAITING:
		return "Unresolved operation: " + condition.State.String(), nil
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return "", err
	}
	defer c.Close()
	dirty, err := c.GetMeta(ctx, "dirty")
	if err != nil {
		return "", err
	}
	revision, err := c.GetMeta(ctx, "remote_revision")
	if err != nil {
		return "", err
	}
	if dirty != "" || revision == "" {
		reason := "Catalog not published"
		return reason, nil
	}
	unfinished, err := s.unfinishedJobs(ctx, c)
	if err != nil {
		return "", err
	}
	if unfinished {
		reason := "Import or restore unfinished"
		return reason, nil
	}
	complete := true
	if err = c.EachSnapshot(ctx, func(v model.Snapshot) error {
		if v.Status != model.STATUS_COMPLETED {
			complete = false
		}
		return nil
	}); err != nil {
		return "", err
	}
	if !complete {
		reason := "Unfinished snapshot"
		return reason, nil
	}
	return "", nil
}
func (s *Service) PlanCleanup(ctx context.Context) (CleanupPlan, error) {
	plan := CleanupPlan{}
	reason, err := s.CleanupBlock(ctx)
	if err != nil {
		return plan, err
	}
	if reason != "" {
		plan.Skipped = reason
		return plan, nil
	}

	seen := map[string]bool{}
	err = s.History(ctx, func(v model.Snapshot, locations []model.Location) error {
		for _, path := range []string{v.File, filepath.Join(s.Directory, "payload-"+v.Hash+".gpg")} {
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			name := filepath.Base(path)
			if strings.HasPrefix(name, "import-") && !v.Imported {
				continue
			}
			if filepath.Dir(path) != s.Directory || !(strings.HasPrefix(name, "import-") || name == "payload-"+v.Hash+".gpg") {
				continue
			}
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("backup: refusing nonregular cleanup file %q", path)
			}
			hash, size, err := archive.Hash(ctx, path)
			if err != nil {
				return err
			}
			if hash != v.Hash || size != v.Size {
				return fmt.Errorf("backup: cleanup archive changed: %s", name)
			}
			item := CleanupFile{Path: path, Size: size, Hash: hash, Snapshot: v.ID, Locations: locations, original: info}
			if err = s.checkCleanupCopy(ctx, item); err != nil {
				return err
			}
			plan.Files = append(plan.Files, item)
		}
		return nil
	})
	return plan, err
}
func (s *Service) checkCleanupCopy(ctx context.Context, item CleanupFile) error {
	var last error
	for _, l := range item.Locations {
		if l.Status != model.STATUS_COMPLETED {
			continue
		}
		store := s.Stores[l.Provider]
		if store == nil {
			continue
		}
		h, err := store.Head(ctx, l.Key, l.Version)
		if err != nil {
			last = err
			continue
		}
		if h.Size != item.Size || l.Version != "" && h.Version != l.Version || l.ETag != "" && h.ETag != l.ETag || h.Hash != "" && h.Hash != item.Hash {
			last = fmt.Errorf("backup: remote archive identity differs")
			continue
		}
		if l.Version == "" && l.ETag == "" && h.Hash != item.Hash {
			last = fmt.Errorf("backup: remote identity is not pinned")
			continue
		}
		if h.Hash != item.Hash && l.Verification != "sha256" {
			var job importJob
			data, err := os.ReadFile(item.Path + ".json")
			if err != nil || json.Unmarshal(data, &job) != nil || job.Hash != item.Hash || job.Remote != l.Provider || job.Object.Key != l.Key || job.Object.Version != l.Version {
				last = fmt.Errorf("backup: remote copy has only metadata verification")
				continue
			}
		}
		return nil
	}
	if last == nil {
		last = fmt.Errorf("backup: no registered remote copy")
	}
	return fmt.Errorf("backup: cannot clean %s: %w", filepath.Base(item.Path), last)
}
func (s *Service) RemoveCleanupFile(ctx context.Context, item CleanupFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Pending() {
		return ErrPending
	}
	if err := s.checkCleanupCopy(ctx, item); err != nil {
		return err
	}
	hash, size, err := archive.Hash(ctx, item.Path)
	if err != nil {
		return err
	}
	if hash != item.Hash || size != item.Size {
		return fmt.Errorf("backup: cleanup archive changed")
	}
	return file_io.RemoveUnchanged(item.Path, item.original)
}

func (s *Service) CleanupRemotes(ctx context.Context) ([]string, error) {
	remotes := []string{}
	err := s.History(ctx, func(_ model.Snapshot, locations []model.Location) error {
		for _, l := range locations {
			if l.Status == model.STATUS_COMPLETED {
				remotes = append(remotes, l.Provider)
			}
		}
		return nil
	})
	return remotes, err
}
