package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"glesha/cloud"
	"glesha/database/model"
)

func (s *Service) Discover(ctx context.Context, remote string, fn func(Descriptor) error) error {
	store := s.Stores[remote]
	if store == nil {
		return fmt.Errorf("backup: discovery remote unavailable")
	}
	latest := map[string]cloud.Object{}
	numbers := map[string]int64{}
	prefix := strings.TrimSuffix(s.Config.Catalog.Prefix, "/") + "/sets/"
	folder := "heads"
	conditional := s.Kinds[remote] == "aws"
	if conditional {
		folder = "commits"
	}
	if err := cloud.List(ctx, store, prefix, func(h cloud.Object) error {
		parts := strings.Split(strings.TrimPrefix(h.Key, prefix), "/")
		if len(parts) != 3 || parts[1] != folder {
			return nil
		}
		if _, err := uuid.Parse(parts[0]); err != nil {
			return fmt.Errorf("backup: invalid set identifier")
		}
		numberText, id, ok := strings.Cut(strings.TrimSuffix(parts[2], ".json"), "-")
		if conditional {
			numberText = strings.TrimSuffix(parts[2], ".json")
			ok = true
		}
		number, err := strconv.ParseInt(numberText, 10, 64)
		if !ok || err != nil || number < 1 {
			return fmt.Errorf("backup: malformed revision key")
		}
		if !conditional {
			if _, err = uuid.Parse(id); err != nil {
				return err
			}
		}
		if number == numbers[parts[0]] {
			return fmt.Errorf("%w: multiple heads preserved for set %s", ErrConflict, parts[0])
		}
		if number > numbers[parts[0]] {
			numbers[parts[0]] = number
			latest[parts[0]] = h
		}
		return nil
	}); err != nil {
		return err
	}
	for setID, h := range latest {
		stem := strings.TrimSuffix(filepath.Base(h.Key), ".json")
		if conditional {
			r, _, err := store.Get(ctx, h.Key, "")
			if err != nil {
				return err
			}
			b, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
			r.Close()
			if err != nil {
				return err
			}
			var committed revision
			if err = json.Unmarshal(b, &committed); err != nil {
				return err
			}
			if committed.Version != 2 || committed.Set != setID || committed.Number != numbers[setID] {
				return fmt.Errorf("backup: invalid committed discovery revision")
			}
			if _, err = uuid.Parse(committed.ID); err != nil {
				return err
			}
			stem = fmt.Sprintf("%020d-%s", committed.Number, committed.ID)
		}

		descriptorKey := prefix + setID + "/descriptors/" + stem + ".json.gz"
		descriptor, err := store.Head(ctx, descriptorKey+".gpg", "")
		if err == nil {
			descriptorKey += ".gpg"
		} else if cloud.NotFound(err) {
			descriptor, err = store.Head(ctx, descriptorKey, "")
		}
		if err != nil {
			return err
		}
		h = descriptor
		h.Key = descriptorKey
		r, _, err := store.Get(ctx, h.Key, "")
		if err != nil {
			return err
		}
		body, err := s.metadataReader(ctx, r, h.Key)
		if err != nil {
			r.Close()
			return err
		}
		var d Descriptor
		payload, readErr := io.ReadAll(io.LimitReader(body, (4<<20)+1))
		err = readErr
		if err == nil && len(payload) > 4<<20 {
			err = fmt.Errorf("backup: oversized descriptor")
		}
		if err == nil {
			err = json.Unmarshal(payload, &d)
		}
		body.Close()
		r.Close()
		if err != nil {
			return err
		}
		if d.Version != 2 || d.Set.ID != setID || !validName.MatchString(d.Set.Name) {
			return fmt.Errorf("backup: invalid set descriptor")
		}
		if _, err = uuid.Parse(d.Set.ID); err != nil {
			return err
		}
		for _, ref := range d.Remotes {
			bound, err := s.Registry.BindRemote(ctx, ref)
			if err != nil {
				return err
			}
			if bound.ID != ref.ID {
				return fmt.Errorf("backup: descriptor remote identity differs")
			}
		}
		if _, err = s.Registry.Set(ctx, d.Set.ID); err != nil {
			if err = s.Registry.Create(ctx, d.Set); err != nil {
				return fmt.Errorf("backup: discovered set name conflicts; metadata is preserved remotely: %w", err)
			}
		}
		if err = fn(d); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Refresh(ctx context.Context) error {
	return s.History(ctx, func(v model.Snapshot, locations []model.Location) error {
		for _, l := range locations {
			store := s.Stores[l.Provider]
			if store == nil {
				continue
			}
			h, err := store.Head(ctx, l.Key, l.Version)
			if err != nil {
				return err
			}
			if err = s.saveJob(filepath.Join(s.Directory, "observation-"+v.ID+"-"+l.Provider+".json"), h); err != nil {
				return err
			}
		}
		return nil
	})
}
