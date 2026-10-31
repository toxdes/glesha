package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"glesha/database/model"
	"glesha/file_io"
)

const restorePublicationVersion = 1

type restorePublication struct {
	Version  int    `json:"version"`
	Snapshot string `json:"snapshot"`
	Output   string `json:"output"`
	Stage    string `json:"stage"`
}

func (s *Service) restorePublicationPath(output string) string {
	sum := sha256.Sum256([]byte(output))
	return filepath.Join(s.Directory, "restore-publication-"+hex.EncodeToString(sum[:16])+".json")
}

func (s *Service) readRestorePublication(output string) (restorePublication, error) {
	var value restorePublication
	b, err := os.ReadFile(s.restorePublicationPath(output))
	if err != nil {
		return value, err
	}
	if err = json.Unmarshal(b, &value); err != nil {
		return value, err
	}
	if value.Version != restorePublicationVersion || value.Output != output || !filepath.IsAbs(value.Stage) || filepath.Dir(value.Stage) != output || !strings.HasPrefix(filepath.Base(value.Stage), ".glesha-restore-") {
		return value, fmt.Errorf("backup: unsupported or conflicting restore publication")
	}
	return value, nil
}

func (s *Service) RestoreSelection(ctx context.Context, snapshot, output string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	publication, err := s.readRestorePublication(output)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return "", err
	}
	if snapshot != "" && snapshot != "latest" && snapshot != publication.Snapshot {
		return "", fmt.Errorf("backup: another snapshot has pending restore publication")
	}
	return publication.Snapshot, nil
}

func (s *Service) CheckRestoreDestination(ctx context.Context, snapshot, output string) error {
	info, err := os.Lstat(output)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("backup: restore destination must be a directory, not a file or symlink: %s", output)
	}
	root, err := os.OpenRoot(output)
	if err != nil {
		return err
	}
	defer root.Close()
	var stage *os.Root
	if publication, err := s.readRestorePublication(output); err == nil {
		stage, err = os.OpenRoot(publication.Stage)
		if err != nil {
			return err
		}
		defer stage.Close()
		if snapshot != "" && snapshot != "latest" && snapshot != publication.Snapshot {
			return fmt.Errorf("backup: another snapshot has pending restore publication")
		}
		snapshot = publication.Snapshot
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	v, err := Resolve(ctx, c, snapshot)
	if err != nil {
		return err
	}
	return c.EachEntry(ctx, v.ID, func(entry model.Entry) error {
		return file_io.CheckDestinationEntry(root, entry.Path, entry.Type == "directory", stage)
	})
}
