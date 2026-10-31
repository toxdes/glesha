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

	"glesha/file_io"
)

type RestoreCancellation struct {
	Tasks   int      `json:"tasks"`
	Records []string `json:"preserved_records,omitempty"`
}

func (s *Service) restoreJobPath(output string) string {
	digest := sha256.Sum256([]byte(output))
	return filepath.Join(s.Directory, "restore-"+hex.EncodeToString(digest[:16])+".json")
}

// CancelRestore archives local task records without touching restored or staged files.
func (s *Service) CancelRestore(ctx context.Context, output string) (RestoreCancellation, error) {
	var result RestoreCancellation
	if output != "" {
		var err error
		output, err = filepath.Abs(output)
		if err != nil {
			return result, err
		}
	}
	entries, err := file_io.ReadDir(ctx, s.Directory)
	if err != nil {
		return result, err
	}
	var files []string
	destinations := map[string]bool{}
	otherTasks := false
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "restore-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		if output != "" && name != filepath.Base(s.restoreJobPath(output)) && name != filepath.Base(s.restorePublicationPath(output)) {
			otherTasks = true
			continue
		}
		if !entry.Type().IsRegular() {
			return result, fmt.Errorf("backup: restore task must be a regular file: %s", name)
		}
		path := filepath.Join(s.Directory, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return result, err
		}
		var destination string
		if strings.HasPrefix(name, "restore-publication-") {
			var publication restorePublication
			if err = json.Unmarshal(data, &publication); err != nil {
				return result, err
			}
			if publication.Version != restorePublicationVersion || publication.Snapshot == "" || !filepath.IsAbs(publication.Output) {
				return result, fmt.Errorf("backup: unsupported or conflicting restore publication")
			}
			if _, err = s.readRestorePublication(publication.Output); err != nil {
				return result, err
			}
			if path != s.restorePublicationPath(publication.Output) {
				return result, fmt.Errorf("backup: restore publication identity mismatch")
			}
			destination = publication.Output
		} else {
			var job struct {
				restoreJob
				Version *int
			}
			if err = json.Unmarshal(data, &job); err != nil {
				return result, err
			}
			if job.Version != nil || job.Snapshot == "" || !filepath.IsAbs(job.Output) || path != s.restoreJobPath(job.Output) {
				return result, fmt.Errorf("backup: unsupported or conflicting restore task")
			}
			destination = job.Output
		}
		files = append(files, path)
		destinations[destination] = true
	}
	advisory := filepath.Join(s.Directory, "operation-result.json")
	if !otherTasks && (output == "" || len(files) > 0) {
		if data, err := os.ReadFile(advisory); err == nil {
			var previous operationResult
			if err = json.Unmarshal(data, &previous); err != nil {
				return result, err
			}
			if previous.Version != 1 {
				return result, fmt.Errorf("backup: unsupported operation-result version")
			}
			if previous.Operation == "restore" {
				files = append(files, advisory)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return result, err
		}
	}
	if len(files) == 0 {
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	suffix := ".cancelled-" + newID()
	for _, path := range files {
		target := path + suffix
		if err := os.Rename(path, target); err != nil {
			return result, fmt.Errorf("backup: preserve cancelled restore task: %w", err)
		}
		result.Records = append(result.Records, target)
	}
	result.Tasks = len(destinations)
	if result.Tasks == 0 {
		result.Tasks = 1
	}
	return result, nil
}
