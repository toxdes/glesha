package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"glesha/archive"
	"glesha/cloud"
	"glesha/database/model"
	"glesha/file_io"
	L "glesha/logger"
)

type RetrievalOptions struct {
	Tier    string
	Days    int32
	Request bool
	Confirm func(context.Context, string) error
}
type retrievalItem struct {
	Location  model.Location
	Tier      string
	Requested bool
}
type restoreJob struct {
	Tier             string
	Days             int32
	Snapshot, Output string
	From             []string
	Requests         []retrievalItem
}

func (s *Service) saveJob(file string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	temp, err := file_io.Temp(s.Directory)
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(b); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), file)
}
func (s *Service) PrepareRestore(ctx context.Context, id, output string, from []string, o RetrievalOptions) (string, string, error) {
	output, err := filepath.Abs(output)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256([]byte(output))
	jobFile := filepath.Join(s.Directory, "restore-"+hex.EncodeToString(digest[:16])+".json")
	if o.Tier == "" {
		o.Tier = "bulk"
	}
	if o.Days == 0 {
		o.Days = s.Config.Cold.Days
	}
	if o.Days == 0 {
		o.Days = 7
	}
	job := restoreJob{Output: output, From: from, Tier: o.Tier, Days: o.Days}
	b, err := os.ReadFile(jobFile)
	if err == nil {
		if err = json.Unmarshal(b, &job); err != nil {
			return "", "", err
		}
		if job.Tier != "" && (job.Tier != o.Tier || job.Days != o.Days) {
			return "", "", fmt.Errorf("backup: retrieval options conflict with pending restore")
		}
		job.Tier, job.Days = o.Tier, o.Days
		if id != "" && id != "latest" && id != job.Snapshot {
			return "", "", fmt.Errorf("backup: snapshot conflicts with pending restore")
		}
		if len(from) > 0 && strings.Join(from, ",") != strings.Join(job.From, ",") {
			return "", "", fmt.Errorf("backup: source order conflicts with pending restore")
		}
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return "", "", err
	}
	defer c.Close()
	targetID := id
	if job.Snapshot != "" {
		targetID = job.Snapshot
	}
	v, err := Resolve(ctx, c, targetID)
	if err != nil {
		return "", "", err
	}
	job.Snapshot = v.ID
	if len(job.From) == 0 {
		job.From = s.Set.To
	}

	chain, err := s.ancestry(ctx, c, v)
	if err != nil {
		return "", "", err
	}
	defer chain.Close()
	pending := []retrievalItem{}
	var total int64
	inspect := func(snapshot model.Snapshot, locations []model.Location) error {
		if _, ok := s.localArchive(ctx, snapshot); ok {
			return nil
		}
		var cold *retrievalItem
		var last error
		for _, remote := range job.From {
			for _, l := range locations {
				if l.Provider != remote || l.Status != model.STATUS_COMPLETED {
					continue
				}
				store := s.Stores[remote]
				if store == nil {
					continue
				}
				h, err := store.Head(ctx, l.Key, l.Version)
				if err != nil {
					last = err
					continue
				}
				if h.Size != snapshot.Size || h.Hash != "" && h.Hash != snapshot.Hash {
					last = fmt.Errorf("backup: registered object identity changed")
					continue
				}
				if !h.Cold {
					return nil
				}
				if s.Kinds[remote] != "aws" {
					last = fmt.Errorf("backup: remote cannot retrieve cold objects")
					continue
				}
				tier := o.Tier
				if tier == "" {
					tier = "bulk"
				}
				if tier == "expedited" && (h.Class == "DEEP_ARCHIVE" || strings.Contains(h.ArchiveStatus, "DEEP")) {
					last = fmt.Errorf("backup: expedited retrieval is unavailable for Deep Archive")
					continue
				}
				l.Version = h.Version
				l.CurrentClass = h.Class
				l.Restore = h.Restore
				if cold == nil {
					cold = &retrievalItem{Location: l, Tier: tier}
				}
			}
		}
		if cold == nil {
			if last != nil {
				return last
			}
			return fmt.Errorf("backup: no registered readable copy of %s", snapshot.ID)
		}
		for _, old := range job.Requests {
			if old.Location.Provider == cold.Location.Provider && old.Location.Key == cold.Location.Key && old.Location.Version == cold.Location.Version {
				cold.Requested = old.Requested
			}
		}
		if strings.Contains(cold.Location.Restore, `ongoing-request="true"`) {
			cold.Requested = true
		}
		pending = append(pending, *cold)
		if !cold.Requested {
			total += snapshot.Size
		}
		return nil
	}
	err = chain.EachReverse(ctx, func(snapshot model.Snapshot) error {
		if snapshot.Layout == "chunked" {
			if err := validateChunks(snapshot); err != nil {
				return err
			}
			for _, chunk := range snapshot.Chunks {
				payload := snapshot
				payload.Hash, payload.Size, payload.File = chunk.Hash, chunk.Size, ""
				if err := inspect(payload, chunk.Locations); err != nil {
					return err
				}
			}
			return nil
		}
		locations, err := c.Locations(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		return inspect(snapshot, locations)
	})
	if err != nil {
		return "", "", err
	}
	job.Requests = pending
	if err = s.saveJob(jobFile, job); err != nil {
		return "", "", err
	}
	if len(pending) == 0 {
		return v.ID, jobFile, nil
	}
	count := 0
	for _, p := range pending {
		if !p.Requested {
			count++
		}
	}
	if count > 0 {
		if !o.Request {
			if o.Confirm == nil {
				return "", "", ErrConfirmationRequired
			}
			message := fmt.Sprintf("Retrieve %d objects (%s), tier %s? Retrieval, storage and download charges apply.", count, L.Bytes(total), pending[0].Tier)
			if err = o.Confirm(ctx, message); err != nil {
				return "", "", err
			}
		}
		for i := range job.Requests {
			p := &job.Requests[i]
			if p.Requested {
				continue
			}
			store := s.Stores[p.Location.Provider]
			restorer, ok := store.(cloud.VersionRestorer)
			if !ok {
				return "", "", fmt.Errorf("backup: remote cannot restore pinned versions")
			}
			tier := strings.ToUpper(p.Tier[:1]) + p.Tier[1:]
			if err = restorer.RestoreVersion(ctx, p.Location.Key, p.Location.Version, o.Days, tier); err != nil {
				return "", "", err
			}
			p.Requested = true
			if err = s.saveJob(jobFile, job); err != nil {
				return "", "", err
			}
		}
	}
	return v.ID, jobFile, fmt.Errorf("%w: retrieval in progress; repeat the restore command when objects are readable", ErrPending)
}

type importJob struct {
	Days      int32
	Remote    string
	Object    cloud.Object
	Hash      string
	Requested bool
	Tier      string
}

func (s *Service) PrepareImport(ctx context.Context, from []string, key string, o RetrievalOptions) (string, string, cloud.Object, error) {
	if o.Tier == "" {
		o.Tier = "bulk"
	}
	if o.Days == 0 {
		o.Days = s.Config.Cold.Days
	}
	if o.Days == 0 {
		o.Days = 7
	}
	digest := sha256.Sum256([]byte(strings.Join(from, ",") + ":" + key))
	file := filepath.Join(s.Directory, "import-"+hex.EncodeToString(digest[:16])+".gpg")
	sidecar := file + ".json"
	var job importJob
	b, err := os.ReadFile(sidecar)
	if err == nil {
		if err = json.Unmarshal(b, &job); err != nil {
			return "", "", cloud.Object{}, err
		}
	} else if !os.IsNotExist(err) {
		return "", "", cloud.Object{}, err
	}
	if job.Remote != "" && (job.Tier != o.Tier || job.Days != o.Days) {
		return "", "", job.Object, fmt.Errorf("backup: retrieval options conflict with pending import")
	}
	if job.Remote == "" {
		var cold *importJob
		var last error
		for _, remote := range from {
			store := s.Stores[remote]
			if store == nil {
				continue
			}
			h, err := store.Head(ctx, key, "")
			if err != nil {
				last = err
				continue
			}
			candidate := importJob{Remote: remote, Object: h, Tier: o.Tier, Days: o.Days}
			if candidate.Tier == "" {
				candidate.Tier = "bulk"
			}
			if h.Cold {
				if cold == nil {
					cold = &candidate
				}
				continue
			}
			job = candidate
			break
		}
		if job.Remote == "" {
			if cold == nil {
				if last == nil {
					last = fmt.Errorf("backup: no import source available")
				}
				return "", "", cloud.Object{}, last
			}
			job = *cold
		}
		if err = s.saveJob(sidecar, job); err != nil {
			return "", "", job.Object, err
		}
	}
	if _, err = os.Stat(file); err == nil {
		hash, size, err := archive.Hash(ctx, file)
		if err != nil || size != job.Object.Size || job.Hash != "" && hash != job.Hash {
			return "", "", job.Object, fmt.Errorf("backup: import download changed")
		}
		if job.Hash == "" {
			job.Hash = hash
			if err = s.saveJob(sidecar, job); err != nil {
				return "", "", job.Object, err
			}
		}
		return file, job.Remote, job.Object, nil
	} else if !os.IsNotExist(err) {
		return "", "", job.Object, err
	}
	store := s.Stores[job.Remote]
	if store == nil {
		return "", "", job.Object, fmt.Errorf("backup: pinned import remote unavailable")
	}
	h, err := store.Head(ctx, key, job.Object.Version)
	if err != nil {
		return "", "", job.Object, err
	}
	if h.Cold {
		if s.Kinds[job.Remote] != "aws" {
			return "", "", h, fmt.Errorf("backup: remote does not support cold retrieval")
		}
		if job.Tier == "expedited" && (h.Class == "DEEP_ARCHIVE" || strings.Contains(h.ArchiveStatus, "DEEP")) {
			return "", "", h, fmt.Errorf("backup: expedited unavailable for Deep Archive")
		}
		if !job.Requested && !strings.Contains(h.Restore, `ongoing-request="true"`) {
			if !o.Request {
				if o.Confirm == nil {
					return "", "", h, ErrConfirmationRequired
				}
				if err = o.Confirm(ctx, fmt.Sprintf("Import requires retrieval of %s using %s. Charges apply. Request?", L.Bytes(h.Size), job.Tier)); err != nil {
					return "", "", h, err
				}
			}
			restorer, ok := store.(cloud.VersionRestorer)
			if !ok {
				return "", "", h, fmt.Errorf("backup: remote cannot retrieve pinned objects")
			}
			tier := strings.ToUpper(job.Tier[:1]) + job.Tier[1:]
			if err = restorer.RestoreVersion(ctx, key, job.Object.Version, o.Days, tier); err != nil {
				return "", "", h, err
			}
			job.Requested = true
			if err = s.saveJob(sidecar, job); err != nil {
				return "", "", h, err
			}
		}
		return "", job.Remote, h, fmt.Errorf("%w: import retrieval in progress; repeat import when readable", ErrPending)
	}
	object, err := Download(ctx, s.Stores, []string{job.Remote}, key, job.Object.Version, file)
	if err != nil {
		return "", "", object, err
	}
	hash, size, err := archive.Hash(ctx, file)
	if err != nil || size != job.Object.Size {
		return "", "", object, fmt.Errorf("backup: downloaded object changed")
	}
	job.Hash = hash
	job.Object = object
	if err = s.saveJob(sidecar, job); err != nil {
		return "", "", object, err
	}
	return file, job.Remote, object, nil
}
