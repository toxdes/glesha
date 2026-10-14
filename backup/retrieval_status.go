package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"glesha/cloud"
	"glesha/database/model"
	"glesha/database/repository"
)

type RetrievalState string

const (
	RETRIEVAL_QUEUED  RetrievalState = "QUEUED"
	RETRIEVAL_PARTIAL RetrievalState = "PARTIAL"
	RETRIEVAL_READY   RetrievalState = "READY"
	RETRIEVAL_EXPIRED RetrievalState = "EXPIRED"
)

func (s RetrievalState) String() string { return string(s) }
func ParseRetrievalState(value string) (RetrievalState, error) {
	switch s := RetrievalState(value); s {
	case RETRIEVAL_QUEUED, RETRIEVAL_PARTIAL, RETRIEVAL_READY, RETRIEVAL_EXPIRED:
		return s, nil
	default:
		return "", fmt.Errorf("backup: unknown retrieval state %q", value)
	}
}

type RetrievalTask string

const (
	RETRIEVAL_IMPORT  RetrievalTask = "IMPORT"
	RETRIEVAL_RESTORE RetrievalTask = "RESTORE"
)

func (t RetrievalTask) String() string { return string(t) }
func ParseRetrievalTask(value string) (RetrievalTask, error) {
	switch t := RetrievalTask(value); t {
	case RETRIEVAL_IMPORT, RETRIEVAL_RESTORE:
		return t, nil
	default:
		return "", fmt.Errorf("backup: unknown retrieval task %q", value)
	}
}

type RetrievalObject struct {
	Provider  string         `json:"provider"`
	Object    cloud.Object   `json:"object"`
	State     RetrievalState `json:"state"`
	CheckedAt *time.Time     `json:"checked_at,omitempty"`
	ExpiresAt *time.Time     `json:"expires_at,omitempty"`
}

type RetrievalStatus struct {
	ID        string            `json:"id"`
	Task      RetrievalTask     `json:"task"`
	State     RetrievalState    `json:"state"`
	Ready     int               `json:"ready"`
	Waiting   int               `json:"waiting"`
	CheckedAt *time.Time        `json:"checked_at,omitempty"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
	Objects   []RetrievalObject `json:"objects"`
}

type retrievalCache struct {
	Version int               `json:"version"`
	Set     string            `json:"set"`
	Job     string            `json:"job"`
	Task    RetrievalTask     `json:"task"`
	Objects []RetrievalObject `json:"objects"`
}

type retrievalIdentity struct {
	provider, key, version string
}

func rememberRetrieval(job *restoreJob, item retrievalItem) {
	if job.retrievalIndex == nil {
		job.retrievalIndex = make(map[retrievalIdentity]int, len(job.Retrievals))
		for i, old := range job.Retrievals {
			l := old.Location
			job.retrievalIndex[retrievalIdentity{l.Provider, l.Key, l.Version}] = i
		}
	}
	l := item.Location
	identity := retrievalIdentity{l.Provider, l.Key, l.Version}
	if index, ok := job.retrievalIndex[identity]; ok {
		job.Retrievals[index] = item
		return
	}
	job.retrievalIndex[identity] = len(job.Retrievals)
	job.Retrievals = append(job.Retrievals, item)
}

// Retrievals reads local jobs; refresh issues only HEAD requests for their pinned objects.
func (s *Service) Retrievals(ctx context.Context, refresh bool) ([]RetrievalStatus, error) {
	dir, err := os.Open(s.Directory)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var catalog repository.Cataloger
	if _, err = os.Stat(s.path()); err == nil {
		catalog, err = s.Catalog(ctx)
		if err != nil {
			return nil, err
		}
		defer catalog.Close()
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	var result []RetrievalStatus
	for {
		entries, readErr := dir.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			name := entry.Name()
			isImport := strings.HasPrefix(name, "import-") && strings.HasSuffix(name, ".gpg.json")
			isRestore := strings.HasPrefix(name, "restore-") && strings.HasSuffix(name, ".json")
			if !isImport && !isRestore {
				continue
			}
			file := filepath.Join(s.Directory, name)
			data, err := os.ReadFile(file)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			cache := retrievalCache{Version: 1, Set: s.Set.ID, Job: name, Task: RETRIEVAL_RESTORE}
			indices := map[retrievalIdentity]int{}
			if isImport {
				var job importJob
				if err = json.Unmarshal(data, &job); err != nil {
					return nil, err
				}
				if !job.Requested && !strings.Contains(job.Object.Restore, `ongoing-request="true"`) {
					continue
				}
				completed := false
				if catalog != nil {
					err = catalog.EachSnapshot(ctx, func(v model.Snapshot) error {
						completed = completed || v.Status == model.STATUS_COMPLETED && v.File == strings.TrimSuffix(file, ".json")
						return nil
					})
					if err != nil {
						return nil, err
					}
				}
				if completed {
					continue
				}
				cache.Task = RETRIEVAL_IMPORT
				mergeRetrievalObject(&cache.Objects, indices, RetrievalObject{Provider: job.Remote, Object: job.Object, CheckedAt: job.CheckedAt})
			} else {
				var job restoreJob
				if err = json.Unmarshal(data, &job); err != nil {
					return nil, err
				}
				for _, item := range append(job.Retrievals, job.Requests...) {
					if !item.Requested && !strings.Contains(item.Location.Restore, `ongoing-request="true"`) {
						continue
					}
					l := item.Location
					if err = s.observeLocation(ctx, l.Snapshot, &l); err != nil {
						return nil, err
					}
					object := RetrievalObject{Provider: l.Provider, Object: cloud.Object{Key: l.Key, Version: l.Version, ETag: l.ETag, Class: l.CurrentClass, Cold: l.Cold, Restore: l.Restore, ArchiveStatus: l.ArchiveStatus}, CheckedAt: l.ObservedAt}
					mergeRetrievalObject(&cache.Objects, indices, object)
				}
			}
			if len(cache.Objects) == 0 {
				continue
			}
			cacheFile := file + ".retrieval-status"
			if data, err = os.ReadFile(cacheFile); err == nil {
				var saved retrievalCache
				if err = json.Unmarshal(data, &saved); err != nil {
					return nil, err
				}
				if saved.Version != 1 || saved.Set != cache.Set || saved.Job != name || saved.Task != cache.Task {
					return nil, fmt.Errorf("backup: unsupported or mismatched retrieval cache")
				}
				for _, object := range saved.Objects {
					if _, ok := indices[retrievalIdentity{object.Provider, object.Object.Key, object.Object.Version}]; !ok {
						return nil, fmt.Errorf("backup: retrieval cache references a different object")
					}
					if object.State != "" {
						if _, err = ParseRetrievalState(object.State.String()); err != nil {
							return nil, err
						}
					}
					mergeRetrievalObject(&cache.Objects, indices, object)
				}
			} else if !os.IsNotExist(err) {
				return nil, err
			}
			for i := range cache.Objects {
				object := &cache.Objects[i]
				if refresh {
					store := s.Stores[object.Provider]
					if store == nil {
						return nil, fmt.Errorf("backup: retrieval remote unavailable")
					}
					h, err := store.Head(ctx, object.Object.Key, object.Object.Version)
					if err != nil {
						return nil, err
					}
					old := object.Object
					if h.Version != old.Version || h.Key != "" && h.Key != old.Key || old.ETag != "" && h.ETag != old.ETag || old.Size > 0 && h.Size != old.Size || old.Hash != "" && h.Hash != "" && old.Hash != h.Hash {
						return nil, fmt.Errorf("backup: retrieval object identity changed")
					}
					h.Key = old.Key
					checked := now()
					object.Object, object.CheckedAt = h, &checked
					if h.Cold && h.Restore == "" {
						object.State = RETRIEVAL_EXPIRED
					} else if !h.Cold && h.Restore == "" {
						object.State = RETRIEVAL_READY
					}
				}
				classifyRetrieval(object, now())
			}
			if refresh {
				if err = s.saveJob(cacheFile, cache); err != nil {
					return nil, err
				}
			}
			result = append(result, summarizeRetrieval(cache))
		}
		if readErr == io.EOF {
			break
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func mergeRetrievalObject(objects *[]RetrievalObject, indices map[retrievalIdentity]int, candidate RetrievalObject) {
	identity := retrievalIdentity{candidate.Provider, candidate.Object.Key, candidate.Object.Version}
	if index, ok := indices[identity]; ok {
		old := (*objects)[index]
		if old.CheckedAt == nil || candidate.CheckedAt != nil && !candidate.CheckedAt.Before(*old.CheckedAt) {
			(*objects)[index] = candidate
		}
		return
	}
	indices[identity] = len(*objects)
	*objects = append(*objects, candidate)
}

func classifyRetrieval(object *RetrievalObject, at time.Time) {
	previous := object.State
	object.ExpiresAt = nil
	header := object.Object.Restore
	if index := strings.Index(header, `expiry-date="`); index >= 0 {
		value := strings.SplitN(header[index+len(`expiry-date="`):], `"`, 2)[0]
		if expiry, err := http.ParseTime(value); err == nil {
			expiry = expiry.UTC()
			object.ExpiresAt = &expiry
		}
	}
	object.State = RETRIEVAL_QUEUED
	if strings.Contains(header, `ongoing-request="true"`) {
		return
	}
	if object.ExpiresAt != nil && !object.ExpiresAt.After(at) {
		object.State = RETRIEVAL_EXPIRED
		return
	}
	if strings.Contains(header, `ongoing-request="false"`) {
		object.State = RETRIEVAL_READY
		return
	}
	if previous == RETRIEVAL_EXPIRED || previous == RETRIEVAL_READY {
		object.State = previous
	}
}

func summarizeRetrieval(cache retrievalCache) RetrievalStatus {
	result := RetrievalStatus{ID: cache.Job, Task: cache.Task, State: RETRIEVAL_QUEUED, Objects: cache.Objects}
	unknownCheck, unknownExpiry, expired := false, false, false
	for _, object := range cache.Objects {
		if object.State == RETRIEVAL_READY {
			result.Ready++
		} else {
			result.Waiting++
		}
		expired = expired || object.State == RETRIEVAL_EXPIRED
		unknownCheck = unknownCheck || object.CheckedAt == nil
		if object.CheckedAt != nil && (result.CheckedAt == nil || object.CheckedAt.Before(*result.CheckedAt)) {
			result.CheckedAt = object.CheckedAt
		}
		unknownExpiry = unknownExpiry || object.ExpiresAt == nil
		if object.ExpiresAt != nil && (result.ExpiresAt == nil || object.ExpiresAt.Before(*result.ExpiresAt)) {
			result.ExpiresAt = object.ExpiresAt
		}
	}
	if unknownCheck {
		result.CheckedAt = nil
	}
	switch {
	case expired:
		result.State = RETRIEVAL_EXPIRED
	case result.Waiting == 0:
		result.State = RETRIEVAL_READY
	case result.Ready > 0:
		result.State = RETRIEVAL_PARTIAL
	}
	if result.Waiting > 0 || unknownExpiry {
		result.ExpiresAt = nil
	}
	return result
}
