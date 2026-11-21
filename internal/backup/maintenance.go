package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"glesha/internal/archive"
	"glesha/internal/cloud"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
)

func (s *Service) stageOperation(ctx context.Context, kind string) (repository.Cataloger, error) {
	if !s.Pending() {
		c, err := s.Catalog(ctx)
		if err != nil {
			return nil, err
		}
		err = c.Vacuum(ctx, s.pending())
		c.Close()
		if err != nil {
			return nil, err
		}
		p, err := repository.NewCatalogRepository(ctx, s.pending())
		if err != nil {
			return nil, err
		}
		if err = clearOperation(ctx, p); err == nil {
			err = p.SetMeta(ctx, "operation", kind)
		}
		if err != nil {
			p.Close()
			return nil, err
		}
		return p, nil
	}
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return nil, err
	}
	operation, err := p.GetMeta(ctx, "operation")
	if err != nil || operation != kind {
		p.Close()
		return nil, fmt.Errorf("%w: another operation is pending", ErrPending)
	}
	return p, nil
}

func (s *Service) StorageClass(ctx context.Context, id, class string) error {
	if class != "" {
		if err := ValidateClass(class); err != nil {
			return err
		}
	}
	if !s.Pending() {
		if err := s.Pull(ctx); err != nil {
			return err
		}
		c, err := s.Catalog(ctx)
		if err != nil {
			return err
		}
		v, err := Resolve(ctx, c, id)
		if err != nil {
			c.Close()
			return err
		}
		if v.Layout == "chunked" {
			c.Close()
			return fmt.Errorf("backup: storage-class changes for chunked snapshots are not supported")
		}
		locations, err := c.Locations(ctx, v.ID)
		c.Close()
		if err != nil {
			return err
		}
		found, matching := false, true
		for _, l := range locations {
			if !s.Capabilities(l.Provider).StorageClasses {
				continue
			}
			found = true
			store := s.Stores[l.Provider]
			if store == nil {
				return fmt.Errorf("backup: AWS remote unavailable")
			}
			h, err := store.Head(ctx, l.Key, l.Version)
			if err != nil {
				return err
			}
			matching = matching && h.Class == class
		}
		if !found {
			return fmt.Errorf("backup: snapshot has no registered AWS copy")
		}
		if matching {
			return nil
		}
	}
	p, err := s.stageOperation(ctx, "storage-class")
	if err != nil {
		return err
	}
	defer p.Close()
	if err = s.checkCatalogSecret(ctx, p); err != nil {
		return err
	}
	savedID, _ := p.GetMeta(ctx, "class_snapshot")
	savedClass, _ := p.GetMeta(ctx, "class_target")
	if savedID != "" {
		if id != "" && id != "latest" && id != savedID || class != "" && class != savedClass {
			return fmt.Errorf("backup: options conflict with pending class change")
		}
		id, class = savedID, savedClass
	} else {
		v, err := Resolve(ctx, p, id)
		if err != nil {
			return err
		}
		id = v.ID
		if class == "" {
			return fmt.Errorf("backup: storage class is required")
		}
		if err = p.SetMeta(ctx, "class_snapshot", id); err != nil {
			return err
		}
		if err = p.SetMeta(ctx, "class_target", class); err != nil {
			return err
		}
	}
	v, err := p.Snapshot(ctx, id)
	if err != nil {
		return err
	}
	if v.Layout == "chunked" {
		return fmt.Errorf("backup: storage-class changes for chunked snapshots are not supported")
	}
	locations, err := p.Locations(ctx, id)
	if err != nil {
		return err
	}
	found := false
	for _, l := range locations {
		if !s.Capabilities(l.Provider).StorageClasses {
			continue
		}
		found = true
		store := s.Stores[l.Provider]
		if store == nil {
			return fmt.Errorf("backup: AWS remote unavailable")
		}
		h, err := store.Head(ctx, l.Key, l.Version)
		if err != nil {
			return err
		}
		if h.Class != class {
			if h.Cold {
				return fmt.Errorf("backup: request retrieval and wait until the source is readable before changing class")
			}
			changer, ok := store.(cloud.ClassChanger)
			if !ok {
				return fmt.Errorf("backup: remote does not support storage-class changes")
			}
			h, err = changer.CopyClass(ctx, l.Key, l.Version, class)
			if err != nil {
				return err
			}
		}
		l.Version = h.Version
		l.ETag = h.ETag
		l.CurrentClass = h.Class
		l.ObservedClass = h.Class
		l.Cold = h.Cold
		t := now()
		l.ObservedAt = &t
		if err = p.PutLocation(ctx, l); err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("backup: snapshot has no registered AWS copy")
	}
	if err = s.publish(ctx, p, currentAuthority); err != nil {
		return err
	}
	return s.finishPending(ctx, p)
}

// Move copies the set's snapshots and catalog to the remote to and makes it the
// set's only remote. An interrupted move resumes from its pending journal.
func (s *Service) Move(ctx context.Context, to string, retrieval RetrievalOptions) error {
	if err := s.rememberAuthority(ctx, ""); err != nil {
		return err
	}
	previousAuthority, err := s.Locator(ctx, s.Set.ID)
	if err != nil {
		return err
	}
	if !s.Pending() && (to == "" || s.Stores[to] == nil) {
		return fmt.Errorf("backup: destination remote is required")
	}
	if !s.Pending() {
		if err := s.Pull(ctx); err != nil {
			return err
		}
	}
	if err = s.rejectChunkedMove(ctx); err != nil {
		return err
	}
	p, err := s.stageOperation(ctx, "move")
	if err != nil {
		return err
	}
	defer p.Close()
	if err = s.checkCatalogSecret(ctx, p); err != nil {
		return err
	}
	if to, err = s.moveDestination(ctx, p, to); err != nil {
		return err
	}
	moveSource, err := s.moveSourceSet(ctx, p)
	if err != nil {
		return err
	}
	if retrieval, err = s.moveRetrieval(ctx, p, retrieval); err != nil {
		return err
	}
	next := s.Set
	next.To = []string{to}
	next.CatalogTo = to
	if next.Authority != "" {
		next.Authority = to
	}
	if err = s.prepareMoveCatalog(ctx, p, next); err != nil {
		return err
	}
	if err = p.EachSnapshot(ctx, func(v model.Snapshot) error {
		return s.copySnapshotTo(ctx, p, v, to, retrieval)
	}); err != nil {
		return err
	}
	b, _ := json.Marshal(next)
	if err = p.SetMeta(ctx, "move_set", string(b)); err != nil {
		return err
	}
	previous := s.Set
	s.Set = next
	err = s.publish(ctx, p, retiredAuthority)
	if err != nil {
		s.Set = previous
		return err
	}
	if err = s.retireMoveSource(ctx, p, moveSource, previousAuthority); err != nil {
		s.Set = previous
		return err
	}
	if err = s.Registry.Save(ctx, next); err != nil {
		s.Set = previous
		return err
	}
	if err = s.rememberAuthority(ctx, locatorID(previousAuthority)); err != nil {
		return err
	}
	return s.finishPending(ctx, p)
}

func (s *Service) rejectChunkedMove(ctx context.Context) error {
	view, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	defer view.Close()
	return view.EachSnapshot(ctx, func(v model.Snapshot) error {
		if v.Layout == "chunked" {
			return fmt.Errorf("backup: moving sets with chunked snapshots is not supported")
		}
		return nil
	})
}

// moveDestination returns the pending move's destination, or saves to as it.
func (s *Service) moveDestination(ctx context.Context, p repository.Cataloger, to string) (string, error) {
	saved, _ := p.GetMeta(ctx, "move_to")
	if saved != "" {
		if to != "" && to != saved {
			return "", fmt.Errorf("backup: destination conflicts with pending move")
		}
		to = saved
	} else {
		if to == "" || s.Stores[to] == nil {
			return "", fmt.Errorf("backup: destination remote is required")
		}
		if err := p.SetMeta(ctx, "move_to", to); err != nil {
			return "", err
		}
	}
	if s.Stores[to] == nil {
		return "", fmt.Errorf("backup: destination remote unavailable")
	}
	return to, nil
}

// moveSourceSet returns the set as it was when the move started, saving it and
// its remote revision on the first attempt.
func (s *Service) moveSourceSet(ctx context.Context, p repository.Cataloger) (model.Set, error) {
	var source model.Set
	saved, err := p.GetMeta(ctx, "move_source_set")
	if err != nil {
		return source, err
	}
	if saved != "" {
		if err = json.Unmarshal([]byte(saved), &source); err != nil || source.ID != s.Set.ID {
			return source, fmt.Errorf("backup: invalid move source journal")
		}
		return source, nil
	}
	source = s.Set
	b, err := json.Marshal(source)
	if err != nil {
		return source, err
	}
	if err = p.SetMeta(ctx, "move_source_set", string(b)); err != nil {
		return source, err
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return source, err
	}
	revision, err := c.GetMeta(ctx, "remote_revision")
	c.Close()
	if err != nil {
		return source, err
	}
	return source, p.SetMeta(ctx, "move_source_revision", revision)
}

// movePolicy is the retrieval policy saved with a pending move.
type movePolicy struct {
	Tier    string
	Days    int32
	Request bool
}

// moveRetrieval reconciles retrieval with the pending move's saved policy, or
// fills in defaults and saves them.
func (s *Service) moveRetrieval(ctx context.Context, p repository.Cataloger, retrieval RetrievalOptions) (RetrievalOptions, error) {
	policy, err := p.GetMeta(ctx, "move_retrieval")
	if err != nil {
		return retrieval, err
	}
	if policy != "" {
		var saved movePolicy
		if err = json.Unmarshal([]byte(policy), &saved); err != nil {
			return retrieval, err
		}
		if retrieval.UseSavedDays || retrieval.Days == 0 {
			retrieval.Days = saved.Days
		}
		if retrieval.Tier != "" && retrieval.Tier != saved.Tier || retrieval.Days != 0 && retrieval.Days != saved.Days {
			return retrieval, fmt.Errorf("backup: retrieval options conflict with pending move")
		}
		retrieval.Tier, retrieval.Days = saved.Tier, saved.Days
		retrieval.Request = retrieval.Request || saved.Request
		return retrieval, nil
	}
	if retrieval.Tier == "" {
		retrieval.Tier = "bulk"
	}
	if retrieval.Days == 0 {
		retrieval.Days = s.Config.Cold.Days
	}
	b, _ := json.Marshal(movePolicy{retrieval.Tier, retrieval.Days, retrieval.Request})
	return retrieval, p.SetMeta(ctx, "move_retrieval", string(b))
}

// copySnapshotTo uploads v's archive to the remote to, unless a matching copy is already there.
func (s *Service) copySnapshotTo(ctx context.Context, p repository.Cataloger, v model.Snapshot, to string, retrieval RetrievalOptions) error {
	if v.Status != model.STATUS_COMPLETED {
		return fmt.Errorf("backup: unfinished snapshot cannot be moved")
	}
	locations, err := p.Locations(ctx, v.ID)
	if err != nil {
		return err
	}
	for _, l := range locations {
		if l.Provider == to && l.Status == model.STATUS_COMPLETED {
			h, err := s.Stores[to].Head(ctx, l.Key, l.Version)
			if err == nil && h.Size == v.Size && h.Hash == v.Hash {
				return nil
			}
		}
	}
	file, err := s.moveSourceFile(ctx, v, locations, retrieval)
	if err != nil {
		return err
	}
	h, err := cloud.Create(ctx, s.Stores[to], s.payloadKey(v), file, "STANDARD")
	if err != nil {
		return err
	}
	return p.PutLocation(ctx, uploadedLocation(v.ID, to, h))
}

// moveSourceFile returns a local, verified copy of v's archive, retrieving and
// downloading it from the set's current remotes when needed.
func (s *Service) moveSourceFile(ctx context.Context, v model.Snapshot, locations []model.Location, retrieval RetrievalOptions) (string, error) {
	if hash, size, err := archive.Hash(ctx, v.File); err == nil && hash == v.Hash && size == v.Size {
		return v.File, nil
	}
	// preparation pins retrieval approvals; existing sources remain untouched
	if _, _, err := s.PrepareRestore(ctx, v.ID, filepath.Join(s.Directory, "move-"+v.ID), s.Set.To, retrieval); err != nil {
		return "", err
	}
	file := filepath.Join(s.Directory, "move-payload-"+v.ID+".gpg")
	if _, err := os.Stat(file); os.IsNotExist(err) && !s.downloadMoveSource(ctx, locations, file) {
		return "", fmt.Errorf("backup: no readable source for %s", v.ID)
	}
	hash, size, err := archive.Hash(ctx, file)
	if err != nil || hash != v.Hash || size != v.Size {
		return "", fmt.Errorf("backup: source checksum mismatch during move")
	}
	return file, nil
}

// downloadMoveSource downloads the first readable copy, in remote order, to file.
func (s *Service) downloadMoveSource(ctx context.Context, locations []model.Location, file string) bool {
	for _, remote := range s.Set.To {
		for _, l := range locations {
			if l.Provider != remote {
				continue
			}
			if _, err := Download(ctx, s.Stores, []string{remote}, l.Key, l.Version, file); err == nil {
				return true
			}
		}
	}
	return false
}

func (s *Service) prepareMoveCatalog(ctx context.Context, p repository.Cataloger, next model.Set) error {
	saved, err := p.GetMeta(ctx, "move_metadata_ready")
	if err != nil {
		return err
	}
	if saved != "" {
		return nil
	}
	peer := *s
	peer.Set = next
	peer.Directory = filepath.Join(s.Directory, ".glesha-move-view-"+newID())
	if err = os.Mkdir(peer.Directory, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(peer.Directory)
	if err = peer.pull(ctx, retiredAuthority, 0); err != nil {
		return err
	}
	target, err := peer.Catalog(ctx)
	if err != nil {
		return err
	}
	defer target.Close()
	if err = target.EachSnapshot(ctx, func(v model.Snapshot) error {
		source, err := p.Snapshot(ctx, v.ID)
		if err != nil {
			return fmt.Errorf("%w: destination has snapshots missing locally; refusing to replace its catalog", ErrConflict)
		}
		if source.Hash != v.Hash || source.Size != v.Size || !sameManifest(source.Manifest, v.Manifest) {
			return fmt.Errorf("%w: destination snapshot identity differs", ErrConflict)
		}
		return nil
	}); err != nil {
		return err
	}
	revision, err := target.GetMeta(ctx, "remote_revision")
	if err != nil {
		return err
	}
	number, err := target.GetMeta(ctx, "remote_number")
	if err != nil {
		return err
	}
	for key, value := range map[string]string{"remote_revision": revision, "remote_number": number, "publication": "", "force_checkpoint": "true", "move_metadata_ready": "true", authorityRelocationKey: ""} {
		if err = p.SetMeta(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}
