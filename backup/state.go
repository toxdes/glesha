package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"glesha/database/model"
	"glesha/database/repository"
)

type SetState string

const (
	STATE_NEW        SetState = "NEW"
	STATE_SYNCED     SetState = "SYNCED"
	STATE_RUNNING    SetState = "RUNNING"
	STATE_PAUSED     SetState = "PAUSED"
	STATE_LOCAL      SetState = "LOCAL"
	STATE_PUBLISHING SetState = "PUBLISHING"
	STATE_WAITING    SetState = "WAITING"
	STATE_STALE      SetState = "STALE"
	STATE_CONFLICT   SetState = "CONFLICT"
	STATE_FAILED     SetState = "FAILED"
)

func (s SetState) String() string { return string(s) }
func ParseSetState(value string) (SetState, error) {
	s := SetState(value)
	switch s {
	case STATE_NEW, STATE_SYNCED, STATE_RUNNING, STATE_PAUSED, STATE_LOCAL, STATE_PUBLISHING, STATE_WAITING, STATE_STALE, STATE_CONFLICT, STATE_FAILED:
		return s, nil
	}
	return "", fmt.Errorf("backup: unknown set state %q", value)
}

type Condition struct {
	State     SetState `json:"state"`
	Reason    string   `json:"reason,omitempty"`
	Action    string   `json:"action,omitempty"`
	Operation string   `json:"operation,omitempty"`
}
type operationResult struct {
	Version int `json:"version"`
	Condition
	At time.Time `json:"at"`
}

// operation results are advisory; catalog and job records remain authoritative.
func (s *Service) RecordResult(ctx context.Context, operation string, err error) error {
	if errors.Is(err, ErrConfirmationRequired) {
		return nil
	}
	path := filepath.Join(s.Directory, "operation-result.json")
	if data, readErr := os.ReadFile(path); readErr == nil {
		var previous operationResult
		if json.Unmarshal(data, &previous) != nil || previous.Version != 1 {
			return fmt.Errorf("backup: unsupported operation-result version")
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	if err == nil {
		if operation == "create" {
			return s.saveJob(path, operationResult{Version: 1, Condition: Condition{State: STATE_NEW, Operation: operation}, At: now()})
		}
		e := os.Remove(path)
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	state := STATE_FAILED
	action := "Inspect the error before retrying."
	switch {
	case errors.Is(err, ErrConflict):
		state = STATE_CONFLICT
		action = "glesha catalog resolve " + s.Set.Name
	case errors.Is(err, context.Canceled), errors.Is(err, ErrPending):
		state = STATE_PAUSED
		action = "glesha retry " + s.Set.Name
	}
	if strings.Contains(err.Error(), "retrieval in progress") {
		state = STATE_WAITING
		action = "Repeat the " + operation + " command when readable."
	}
	if operation == "import" && state == STATE_PAUSED {
		action = "Repeat the import command."
	}
	return s.saveJob(filepath.Join(s.Directory, "operation-result.json"), operationResult{Version: 1, Condition: Condition{State: state, Reason: err.Error(), Action: action, Operation: operation}, At: now()})
}

func (s *Service) Condition(ctx context.Context) (Condition, error) {
	if err := ctx.Err(); err != nil {
		return Condition{}, err
	}
	var result operationResult
	b, err := os.ReadFile(filepath.Join(s.Directory, "operation-result.json"))
	if err == nil {
		if err = json.Unmarshal(b, &result); err != nil {
			return Condition{}, err
		}
		if result.Version != 1 {
			return Condition{}, fmt.Errorf("backup: unsupported operation-result version")
		}
		if _, err = ParseSetState(result.State.String()); err != nil {
			return Condition{}, err
		}
		if result.State != STATE_NEW {
			return result.Condition, nil
		}
		_, catalogErr := os.Stat(s.path())
		_, metadataErr := os.Stat(filepath.Join(s.Directory, "metadata-status.json"))
		if os.IsNotExist(catalogErr) && os.IsNotExist(metadataErr) {
			return result.Condition, nil
		}
		err = os.ErrNotExist
	}
	if !os.IsNotExist(err) {
		return Condition{}, err
	}
	if s.Pending() {
		p, err := repository.NewCatalogRepository(ctx, s.pending())
		if err != nil {
			return Condition{}, err
		}
		defer p.Close()
		op, err := p.GetMeta(ctx, "operation")
		if err != nil {
			return Condition{}, err
		}
		return Condition{State: STATE_PAUSED, Operation: op, Reason: op + " unfinished", Action: "glesha retry " + s.Set.Name}, nil
	}
	if _, err := os.Stat(s.path()); os.IsNotExist(err) {
		if s.Set.CatalogTo != "" && s.Set.CatalogTo != "local" {
			return Condition{State: STATE_STALE, Reason: "Catalog unavailable locally", Action: "glesha catalog sync " + s.Set.Name}, nil
		}
		return Condition{State: STATE_NEW}, nil
	} else if err != nil {
		return Condition{}, err
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return Condition{}, err
	}
	defer c.Close()
	dirty, err := c.GetMeta(ctx, "dirty")
	if err != nil {
		return Condition{}, err
	}
	if dirty != "" {
		return Condition{State: STATE_LOCAL, Reason: "Unpublished " + dirty + " changes", Action: "glesha catalog sync " + s.Set.Name}, nil
	}

	job, unfinished, err := s.pendingJobCondition(ctx, c)
	if err != nil {
		return Condition{}, err
	}
	if unfinished {
		return job, nil
	}

	latest, err := c.GetMeta(ctx, "head")
	if err != nil {
		return Condition{}, err
	}
	if latest == "" {
		completed := false
		if err = c.EachSnapshot(ctx, func(v model.Snapshot) error { completed = completed || v.Status == model.STATUS_COMPLETED; return nil }); err != nil {
			return Condition{}, err
		}
		if !completed {
			return Condition{State: STATE_NEW}, nil
		}
	}
	return Condition{State: STATE_SYNCED}, nil
}

func (s *Service) unfinishedJobs(ctx context.Context, c repository.Cataloger) (bool, error) {
	_, unfinished, err := s.pendingJobCondition(ctx, c)
	return unfinished, err
}

func (s *Service) pendingJobCondition(ctx context.Context, c repository.Cataloger) (Condition, bool, error) {
	dir, err := os.Open(s.Directory)
	if err != nil {
		return Condition{}, false, err
	}
	defer dir.Close()
	var waiting *Condition
	for {
		entries, readErr := dir.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return Condition{}, false, readErr
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return Condition{}, false, err
			}
			name := entry.Name()
			isRestore := strings.HasPrefix(name, "restore-") && strings.HasSuffix(name, ".json")
			isImport := strings.HasPrefix(name, "import-") && strings.HasSuffix(name, ".gpg.json")
			if !isRestore && !isImport {
				continue
			}
			path := filepath.Join(s.Directory, name)
			condition := Condition{State: STATE_PAUSED}
			if isImport {
				file := strings.TrimSuffix(path, ".json")
				completed := false
				if err = c.EachSnapshot(ctx, func(v model.Snapshot) error {
					if v.File == file && v.Status == model.STATUS_COMPLETED {
						completed = true
					}
					return nil
				}); err != nil {
					return Condition{}, false, err
				}
				if completed {
					continue
				}
				var job importJob
				data, err := os.ReadFile(path)
				if err != nil {
					return Condition{}, false, err
				}
				if err = json.Unmarshal(data, &job); err != nil {
					return Condition{}, false, err
				}
				condition.Operation = "import"
				condition.Reason = "Import unfinished"
				condition.Action = "Repeat the import command."
				if _, err = os.Stat(file); os.IsNotExist(err) && (job.Requested || strings.Contains(job.Object.Restore, `ongoing-request="true"`)) {
					condition.State = STATE_WAITING
					condition.Reason = "Import awaiting AWS retrieval"
				}
			} else {
				var job restoreJob
				data, err := os.ReadFile(path)
				if err != nil {
					return Condition{}, false, err
				}
				if err = json.Unmarshal(data, &job); err != nil {
					return Condition{}, false, err
				}
				condition.Operation = "restore"
				condition.Reason = "Restore unfinished"
				condition.Action = "Repeat the restore command."
				for _, request := range job.Requests {
					l := request.Location
					if err = s.observeLocation(ctx, l.Snapshot, &l); err != nil {
						return Condition{}, false, err
					}
					if l.Cold && (request.Requested || strings.Contains(l.Restore, `ongoing-request="true"`)) {
						condition.State = STATE_WAITING
						condition.Reason = "Restore awaiting AWS retrieval"
					}
				}
			}
			if condition.State == STATE_WAITING {
				copy := condition
				waiting = &copy
			} else {
				return condition, true, nil
			}
		}
		if readErr == io.EOF {
			if waiting != nil {
				return *waiting, true, nil
			}
			return Condition{}, false, nil
		}
	}
}
