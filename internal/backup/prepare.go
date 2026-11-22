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
	"time"

	"glesha/internal/archive"
	"glesha/internal/cloud"
	"glesha/internal/config"
	"glesha/internal/database/model"
	"glesha/internal/fsutil"
	"glesha/internal/logger"
)

type RetrievalOptions struct {
	UseSavedDays bool
	Tier         string
	Days         int32
	Request      bool
	Confirm      func(context.Context, string) error
}
type retrievalItem struct {
	Location  model.Location
	Tier      string
	Requested bool
}
type restoreJob struct {
	retrievalIndex   map[retrievalIdentity]int
	Retrievals       []retrievalItem `json:",omitempty"`
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
	return fsutil.WriteAtomic(context.Background(), file, b)
}

// PrepareRestore makes every archive the restore of snapshot id needs readable:
// local copies are used as they are, and cold copies get retrieval requests.
// It returns ErrPending while retrievals are in progress.
func (s *Service) PrepareRestore(ctx context.Context, id, output string, from []string, o RetrievalOptions) (string, string, error) {
	output, err := filepath.Abs(output)
	if err != nil {
		return "", "", err
	}
	jobFile := s.restoreJobPath(output)
	job, o, err := s.loadRestoreJob(jobFile, id, output, from, o)
	if err != nil {
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
	plan := newRetrievalPlan(&job, o.Tier)
	err = chain.EachReverse(ctx, func(snapshot model.Snapshot) error {
		if snapshot.Layout == "chunked" {
			if err := validateChunks(snapshot); err != nil {
				return err
			}
			for _, chunk := range snapshot.Chunks {
				payload := snapshot
				payload.Hash, payload.Size, payload.File = chunk.Hash, chunk.Size, ""
				if err := s.inspect(ctx, plan, payload, chunk.Locations); err != nil {
					return err
				}
			}
			return nil
		}
		locations, err := c.Locations(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		return s.inspect(ctx, plan, snapshot, locations)
	})
	if err != nil {
		return "", "", err
	}
	job.Requests = plan.pending
	if err = s.saveJob(jobFile, job); err != nil {
		return "", "", err
	}
	if len(plan.pending) == 0 {
		return v.ID, jobFile, nil
	}
	if err = s.requestRetrievals(ctx, &job, jobFile, o, plan.bytes); err != nil {
		return "", "", err
	}
	return v.ID, jobFile, fmt.Errorf("%w: retrieval in progress; repeat the restore command when objects are readable", ErrPending)
}

// loadRestoreJob returns the pending restore job for output, checked against the
// new request, or a new job; o comes back with defaults filled in.
func (s *Service) loadRestoreJob(jobFile, id, output string, from []string, o RetrievalOptions) (restoreJob, RetrievalOptions, error) {
	useSavedDays := o.UseSavedDays || o.Days == 0
	if o.Tier == "" {
		o.Tier = "bulk"
	}
	if o.Days == 0 {
		o.Days = s.Config.Cold.Days
	}
	if o.Days == 0 {
		o.Days = config.DefaultRetrievalDays
	}
	job := restoreJob{Output: output, From: from, Tier: o.Tier, Days: o.Days}
	b, err := os.ReadFile(jobFile)
	if os.IsNotExist(err) {
		return job, o, nil
	}
	if err != nil {
		return job, o, err
	}
	if err = json.Unmarshal(b, &job); err != nil {
		return job, o, err
	}
	if useSavedDays && job.Days > 0 {
		o.Days = job.Days
	}
	if job.Tier != "" && (job.Tier != o.Tier || job.Days != o.Days) {
		return job, o, fmt.Errorf("backup: retrieval options conflict with pending restore")
	}
	job.Tier, job.Days = o.Tier, o.Days
	if id != "" && id != "latest" && id != job.Snapshot {
		return job, o, fmt.Errorf("backup: snapshot conflicts with pending restore")
	}
	if len(from) > 0 && strings.Join(from, ",") != strings.Join(job.From, ",") {
		return job, o, fmt.Errorf("backup: source order conflicts with pending restore")
	}
	return job, o, nil
}

// retrievalPlan collects the cold copies a restore needs, one per archive.
type retrievalPlan struct {
	job  *restoreJob
	tier string
	// requested holds copies with a retrieval already asked for
	requested map[retrievalIdentity]retrievalItem
	seen      map[retrievalIdentity]bool
	pending   []retrievalItem
	// bytes counts the archives that still need a retrieval request
	bytes int64
}

func newRetrievalPlan(job *restoreJob, tier string) *retrievalPlan {
	plan := &retrievalPlan{job: job, tier: tier, requested: map[retrievalIdentity]retrievalItem{}, seen: map[retrievalIdentity]bool{}, pending: []retrievalItem{}}
	for _, group := range [][]retrievalItem{job.Retrievals, job.Requests} {
		for _, item := range group {
			l := item.Location
			if item.Requested || strings.Contains(l.Restore, `ongoing-request="true"`) {
				plan.requested[retrievalIdentity{l.Provider, l.Key, l.Version}] = item
			}
		}
	}
	return plan
}

// inspect finds a readable copy of snapshot's archive, or adds the first cold one to plan.
func (s *Service) inspect(ctx context.Context, plan *retrievalPlan, snapshot model.Snapshot, locations []model.Location) error {
	if _, ok := s.localArchive(ctx, snapshot); ok {
		return nil
	}
	var cold *retrievalItem
	var last error
	for _, remote := range plan.job.From {
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
			l.Version, l.CurrentClass, l.Restore, l.Cold = h.Version, h.Class, h.Restore, h.Cold
			checked := now()
			l.ObservedAt = &checked
			if old, ok := plan.requested[retrievalIdentity{l.Provider, l.Key, l.Version}]; ok {
				rememberRetrieval(plan.job, retrievalItem{Location: l, Tier: old.Tier, Requested: true})
			}
			if !h.Cold {
				return nil
			}
			if !s.Capabilities(remote).ColdRetrieval {
				last = fmt.Errorf("backup: remote cannot retrieve cold objects")
				continue
			}
			tier := plan.tier
			if tier == "" {
				tier = "bulk"
			}
			if tier == "expedited" && h.DeepArchive() {
				last = fmt.Errorf("backup: expedited retrieval is unavailable for Deep Archive")
				continue
			}
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
	identity := retrievalIdentity{cold.Location.Provider, cold.Location.Key, cold.Location.Version}
	if _, ok := plan.requested[identity]; ok {
		cold.Requested = cold.Location.Restore != ""
	}
	if strings.Contains(cold.Location.Restore, `ongoing-request="true"`) {
		cold.Requested = true
	}
	if plan.seen[identity] {
		return nil
	}
	plan.seen[identity] = true
	plan.pending = append(plan.pending, *cold)
	if !cold.Requested {
		plan.bytes += snapshot.Size
	}
	return nil
}

// requestRetrievals asks for every pending retrieval not yet requested, after
// confirmation, saving the job after each request.
func (s *Service) requestRetrievals(ctx context.Context, job *restoreJob, jobFile string, o RetrievalOptions, bytes int64) error {
	count := 0
	for _, p := range job.Requests {
		if !p.Requested {
			count++
		}
	}
	if count == 0 {
		return nil
	}
	if !o.Request {
		if o.Confirm == nil {
			return ErrConfirmationRequired
		}
		message := fmt.Sprintf("Retrieve %d objects (%s), tier %s? Retrieval, storage and download charges apply.", count, logger.Bytes(bytes), job.Requests[0].Tier)
		if err := o.Confirm(ctx, message); err != nil {
			return err
		}
	}
	for i := range job.Requests {
		p := &job.Requests[i]
		if p.Requested {
			continue
		}
		restorer, ok := s.Stores[p.Location.Provider].(cloud.VersionRestorer)
		if !ok {
			return fmt.Errorf("backup: remote cannot restore pinned versions")
		}
		tier := strings.ToUpper(p.Tier[:1]) + p.Tier[1:]
		if err := restorer.RestoreVersion(ctx, p.Location.Key, p.Location.Version, o.Days, tier); err != nil {
			return err
		}
		p.Requested = true
		rememberRetrieval(job, *p)
		if err := s.saveJob(jobFile, *job); err != nil {
			return err
		}
	}
	return nil
}

type importJob struct {
	CheckedAt *time.Time `json:",omitempty"`
	Days      int32
	Remote    string
	Object    cloud.Object
	Hash      string
	Requested bool
	Tier      string
}

func (s *Service) PrepareImport(ctx context.Context, from []string, key string, o RetrievalOptions) (string, string, cloud.Object, error) {
	useSavedDays := o.UseSavedDays || o.Days == 0
	if o.Tier == "" {
		o.Tier = "bulk"
	}
	if o.Days == 0 {
		o.Days = s.Config.Cold.Days
	}
	if o.Days == 0 {
		o.Days = config.DefaultRetrievalDays
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
	if useSavedDays && job.Days > 0 {
		o.Days = job.Days
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
			checked := now()
			candidate := importJob{Remote: remote, Object: h, Tier: o.Tier, Days: o.Days, CheckedAt: &checked}
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
	if h.Size != job.Object.Size || h.Version != job.Object.Version || job.Object.ETag != "" && h.ETag != job.Object.ETag {
		return "", "", h, fmt.Errorf("backup: pinned import object changed")
	}
	checked := now()
	job.Object, job.CheckedAt = h, &checked
	if strings.Contains(h.Restore, `ongoing-request="true"`) {
		job.Requested = true
	} else if h.Cold && h.Restore == "" {
		job.Requested = false
	}
	if err = s.saveJob(sidecar, job); err != nil {
		return "", "", h, err
	}
	if h.Cold {
		if !s.Capabilities(job.Remote).ColdRetrieval {
			return "", "", h, fmt.Errorf("backup: remote does not support cold retrieval")
		}
		if job.Tier == "expedited" && h.DeepArchive() {
			return "", "", h, fmt.Errorf("backup: expedited unavailable for Deep Archive")
		}
		if !job.Requested && !strings.Contains(h.Restore, `ongoing-request="true"`) {
			if !o.Request {
				if o.Confirm == nil {
					return "", "", h, ErrConfirmationRequired
				}
				if err = o.Confirm(ctx, fmt.Sprintf("Import requires retrieval of %s using %s. Charges apply. Request?", logger.Bytes(h.Size), job.Tier)); err != nil {
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
