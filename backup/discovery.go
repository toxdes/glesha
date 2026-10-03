package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
		copies, err := SnapshotCopies(v, locations)
		if err != nil {
			return err
		}
		for _, copy := range copies {
			l := copy.Location
			store := s.Stores[l.Provider]
			if store == nil {
				continue
			}
			h, err := store.Head(ctx, l.Key, l.Version)
			if err != nil {
				return err
			}
			if h.Size != copy.Size || h.Hash != "" && h.Hash != copy.Hash {
				return fmt.Errorf("backup: registered object identity changed")
			}
			if h.Key == "" {
				h.Key = l.Key
			}
			if err = s.saveJob(s.observationPath(v.ID, l), h); err != nil {
				return err
			}
		}
		return nil
	})
}

// StoredCopy describes one complete remote object.
type StoredCopy struct {
	Location model.Location
	Size     int64
	Hash     string
}

func SnapshotCopies(v model.Snapshot, locations []model.Location) ([]StoredCopy, error) {
	descriptorSize := v.Size
	if v.Layout == "chunked" {
		for _, chunk := range v.Chunks {
			if chunk.Size < 0 || chunk.Size > descriptorSize {
				return nil, fmt.Errorf("backup: invalid chunked object sizes")
			}
			descriptorSize -= chunk.Size
		}
	}
	copies := make([]StoredCopy, 0, len(locations))
	for _, l := range locations {
		copies = append(copies, StoredCopy{Location: l, Size: descriptorSize, Hash: v.Hash})
	}
	if v.Layout == "chunked" {
		for _, chunk := range v.Chunks {
			for _, l := range chunk.Locations {
				copies = append(copies, StoredCopy{Location: l, Size: chunk.Size, Hash: chunk.Hash})
			}
		}
	}
	return copies, nil
}
func (s *Service) observationPath(snapshot string, l model.Location) string {
	b, _ := json.Marshal([]string{snapshot, l.Provider, l.Key, l.Version})
	sum := sha256.Sum256(b)
	return filepath.Join(s.Directory, "observation-"+hex.EncodeToString(sum[:])+".json")
}
func (s *Service) observeLocation(ctx context.Context, snapshot string, l *model.Location) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file := s.observationPath(snapshot, *l)
	b, err := os.ReadFile(file)
	if os.IsNotExist(err) && !strings.ContainsAny(snapshot+l.Provider, "/\\") {
		file = filepath.Join(s.Directory, "observation-"+snapshot+"-"+l.Provider+".json")
		b, err = os.ReadFile(file)
	}
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var h cloud.Object
	if err = json.Unmarshal(b, &h); err != nil {
		return err
	}
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	if h.Key != l.Key || h.Version != "" && h.Version != l.Version || l.ObservedAt != nil && info.ModTime().Before(*l.ObservedAt) {
		return nil
	}
	observed := info.ModTime().UTC()
	l.ObservedAt = &observed
	l.ObservedClass = h.Class
	l.CurrentClass = h.Class
	l.Cold = h.Cold
	l.Restore = h.Restore
	l.ArchiveStatus = h.ArchiveStatus
	return nil
}
