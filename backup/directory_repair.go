package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"glesha/database/model"
)

type directoryRepair struct {
	Version model.DiscoveryVersion `json:"version"`
	Message string                 `json:"message"`
}

func (s *Service) DirectoryRepairPending(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	b, err := os.ReadFile(filepath.Join(s.Directory, "directory-repair.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var value directoryRepair
	if err = json.Unmarshal(b, &value); err != nil {
		return false, err
	}
	if err = value.Version.Validate(); err != nil {
		return false, fmt.Errorf("backup: registry repair journal: %w", err)
	}
	return true, nil
}

func (s *Service) RecordDirectoryRepair(ctx context.Context, failure error) error {
	if _, err := s.DirectoryRepairPending(ctx); err != nil {
		return err
	}
	file := filepath.Join(s.Directory, "directory-repair.json")
	if failure == nil {
		err := os.Remove(file)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return s.saveJob(file, directoryRepair{Version: model.DiscoveryVersionInitial, Message: failure.Error()})
}
