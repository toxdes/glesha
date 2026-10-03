package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"glesha/archive"
	"glesha/cloud"
	"glesha/database/model"
	"glesha/database/repository"
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
			if s.Kinds[l.Provider] != "aws" {
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
		if s.Kinds[l.Provider] != "aws" {
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
	if err = s.publish(ctx, p); err != nil {
		return err
	}
	return s.finishPending(ctx, p)
}

func (s *Service) Move(ctx context.Context, to string, retrieval RetrievalOptions) error {
	if !s.Pending() && (to == "" || s.Stores[to] == nil) {
		return fmt.Errorf("backup: destination remote is required")
	}
	if !s.Pending() {
		if err := s.Pull(ctx); err != nil {
			return err
		}
	}
	view, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	err = view.EachSnapshot(ctx, func(v model.Snapshot) error {
		if v.Layout == "chunked" {
			return fmt.Errorf("backup: moving sets with chunked snapshots is not supported")
		}
		return nil
	})
	view.Close()
	if err != nil {
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
	saved, _ := p.GetMeta(ctx, "move_to")
	if saved != "" {
		if to != "" && to != saved {
			return fmt.Errorf("backup: destination conflicts with pending move")
		}
		to = saved
	} else {
		if to == "" || s.Stores[to] == nil {
			return fmt.Errorf("backup: destination remote is required")
		}
		if err = p.SetMeta(ctx, "move_to", to); err != nil {
			return err
		}
	}
	if s.Stores[to] == nil {
		return fmt.Errorf("backup: destination remote unavailable")
	}
	policy, err := p.GetMeta(ctx, "move_retrieval")
	if err != nil {
		return err
	}
	if policy != "" {
		var saved struct {
			Tier    string
			Days    int32
			Request bool
		}
		if err = json.Unmarshal([]byte(policy), &saved); err != nil {
			return err
		}
		if retrieval.Tier != "" && retrieval.Tier != saved.Tier || retrieval.Days != 0 && retrieval.Days != saved.Days {
			return fmt.Errorf("backup: retrieval options conflict with pending move")
		}
		retrieval.Tier, retrieval.Days = saved.Tier, saved.Days
		retrieval.Request = retrieval.Request || saved.Request
	} else {
		if retrieval.Tier == "" {
			retrieval.Tier = "bulk"
		}
		if retrieval.Days == 0 {
			retrieval.Days = s.Config.Cold.Days
		}
		b, _ := json.Marshal(struct {
			Tier    string
			Days    int32
			Request bool
		}{retrieval.Tier, retrieval.Days, retrieval.Request})
		if err = p.SetMeta(ctx, "move_retrieval", string(b)); err != nil {
			return err
		}
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

	err = p.EachSnapshot(ctx, func(v model.Snapshot) error {
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
		file := v.File
		hash, size, err := archive.Hash(ctx, file)
		if err != nil || hash != v.Hash || size != v.Size {
			// preparation pins retrieval approvals; existing sources remain untouched
			_, _, err = s.PrepareRestore(ctx, v.ID, filepath.Join(s.Directory, "move-"+v.ID), s.Set.To, retrieval)
			if err != nil {
				return err
			}
			file = filepath.Join(s.Directory, "move-payload-"+v.ID+".gpg")
			if _, err = os.Stat(file); os.IsNotExist(err) {
				readable := false
				for _, remote := range s.Set.To {
					for _, l := range locations {
						if l.Provider != remote {
							continue
						}
						_, err = Download(ctx, s.Stores, []string{remote}, l.Key, l.Version, file)
						if err == nil {
							readable = true
							break
						}
					}
					if readable {
						break
					}
				}
				if !readable {
					return fmt.Errorf("backup: no readable source for %s", v.ID)
				}
			}
			hash, size, err = archive.Hash(ctx, file)
			if err != nil || hash != v.Hash || size != v.Size {
				return fmt.Errorf("backup: source checksum mismatch during move")
			}
		}
		h, err := cloud.Create(ctx, s.Stores[to], s.payloadKey(v), file, "STANDARD")
		if err != nil {
			return err
		}
		location := uploadedLocation(v.ID, to, h)
		if err = p.PutLocation(ctx, location); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	b, _ := json.Marshal(next)
	if err = p.SetMeta(ctx, "move_set", string(b)); err != nil {
		return err
	}
	previous := s.Set
	s.Set = next
	if err = s.publish(ctx, p); err != nil {
		s.Set = previous
		return err
	}
	if err = s.Registry.Save(ctx, next); err != nil {
		s.Set = previous
		return err
	}
	return s.finishPending(ctx, p)
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
	if err = peer.Pull(ctx); err != nil {
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
	for key, value := range map[string]string{"remote_revision": revision, "remote_number": number, "publication": "", "force_checkpoint": "true", "move_metadata_ready": "true"} {
		if err = p.SetMeta(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}
