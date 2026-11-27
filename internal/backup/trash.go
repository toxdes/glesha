package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"glesha/internal/cloud"
	"glesha/internal/database/model"
	"glesha/internal/fsutil"
)

var (
	ErrInTrash         = errors.New("backup: set is in trash")
	ErrNotInTrash      = errors.New("backup: set is not in trash")
	ErrDeletionStarted = errors.New("backup: deletion has started and cannot be undone")
	ErrCatalogLocked   = errors.New("backup: catalog is locked")
	ErrNotDue          = errors.New("backup: trash period has not ended")
)

// archivesPrefix holds every archive glesha uploads, under the set's UUID.
const archivesPrefix = "glesha/archives/"

// Deletion reports what a purge removed and what it left.
type Deletion struct {
	Freed cloud.Freed
	// Kept lists archives the purge left, because they may still be in use
	Kept []KeptObject
	// Done is true once every remote is clean and the set is forgotten locally
	Done bool
}

type KeptObject struct {
	Object model.ObjectRef
	Reason string
}

// tombstonesPrefix holds the deletion markers. Older versions list only
// directory/v1/, so they never read these.
func (s *Service) tombstonesPrefix() string {
	prefix := s.DiscoveryPrefix
	if prefix == "" {
		prefix = s.Config.Catalog.Prefix
	}
	return strings.TrimSuffix(prefix, "/") + "/directory/tombstones/v1/"
}
func (s *Service) trashPrefix() string { return s.tombstonesPrefix() + s.Set.ID + "/" }
func (s *Service) trashKey() string    { return s.trashPrefix() + "trash.json" }
func (s *Service) deletedKey() string  { return s.trashPrefix() + "deleted.json" }

// Trash puts the set in the trash on every open remote. The deadline is the
// earliest one the remotes hold, so trashing a set again never extends it.
func (s *Service) Trash(ctx context.Context, period time.Duration) (model.Trash, error) {
	if len(s.Stores) == 0 {
		return model.Trash{}, fmt.Errorf("backup: no remote is open")
	}
	marker := model.TrashMarker{Version: model.TrashVersion, Set: s.Set.ID, Period: int64(period / time.Second)}
	started := false
	if s.Set.Trash != nil {
		marker.Imported, started = s.Set.Trash.Imported, s.Set.Trash.Started
	} else {
		locked, err := s.CatalogLocked(ctx)
		if err != nil {
			return model.Trash{}, err
		}
		if locked {
			return model.Trash{}, ErrCatalogLocked
		}
		if marker.Imported, err = s.externalObjects(ctx); err != nil {
			return model.Trash{}, err
		}
	}
	var deadline time.Time
	var failures []error
	for _, id := range sortedStores(s.Stores) {
		stored, err := s.publishTrash(ctx, s.Stores[id], marker)
		if err != nil {
			failures = append(failures, fmt.Errorf("backup: trash marker on %s: %w", id, err))
			continue
		}
		if deadline.IsZero() || stored.Deadline.Before(deadline) {
			deadline = stored.Deadline
		}
		marker.Imported = stored.Imported
	}
	if deadline.IsZero() {
		return model.Trash{}, errors.Join(failures...)
	}
	s.Set.Trash = &model.Trash{Deadline: deadline, Imported: marker.Imported, Started: started}
	if err := s.Registry.Save(ctx, s.Set); err != nil {
		return model.Trash{}, err
	}
	return *s.Set.Trash, errors.Join(failures...)
}

// publishTrash writes the marker unless the remote already has one, and
// returns the marker the remote holds.
func (s *Service) publishTrash(ctx context.Context, store cloud.Store, marker model.TrashMarker) (model.Trash, error) {
	if existing, found, err := readTrash(ctx, store, s.trashKey()); err != nil || found {
		return existing, err
	}
	if _, err := s.putJSON(ctx, store, s.trashKey(), marker); err != nil && !errors.Is(err, cloud.ErrObjectExists) {
		return model.Trash{}, err
	}
	existing, _, err := readTrash(ctx, store, s.trashKey())
	return existing, err
}

// readTrash returns the trash marker at key; its deadline counts from the
// time the bucket stored it, so a wrong local clock cannot shorten it.
func readTrash(ctx context.Context, store cloud.Store, key string) (model.Trash, bool, error) {
	r, h, err := store.Get(ctx, key, "")
	if cloud.NotFound(err) {
		return model.Trash{}, false, nil
	}
	if err != nil {
		return model.Trash{}, false, err
	}
	defer r.Close()
	var marker model.TrashMarker
	if err = json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&marker); err != nil {
		return model.Trash{}, false, fmt.Errorf("backup: malformed trash marker %q: %w", key, err)
	}
	if marker.Version != model.TrashVersion || marker.Period < 0 {
		return model.Trash{}, false, fmt.Errorf("backup: unsupported trash marker %q", key)
	}
	stored := now()
	if h.ModifiedAt != nil {
		stored = h.ModifiedAt.UTC()
	}
	return model.Trash{Deadline: stored.Add(time.Duration(marker.Period) * time.Second), Imported: marker.Imported}, true, nil
}

func (s *Service) putJSON(ctx context.Context, store cloud.Store, key string, v any) (cloud.Object, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return cloud.Object{}, err
	}
	file, err := fsutil.Temp(s.Directory)
	if err != nil {
		return cloud.Object{}, err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(b)
	if err = errors.Join(writeErr, file.Close()); err != nil {
		return cloud.Object{}, err
	}
	return cloud.Create(ctx, store, key, file.Name(), "STANDARD")
}

// Undelete takes the set out of the trash before its deadline.
func (s *Service) Undelete(ctx context.Context) error {
	if s.Set.Trash == nil {
		return ErrNotInTrash
	}
	if s.Set.Trash.Due(now()) {
		return ErrDeletionStarted
	}
	for _, id := range sortedStores(s.Stores) {
		started, err := s.deletionStarted(ctx, s.Stores[id])
		if err != nil {
			return fmt.Errorf("backup: read deletion marker on %s: %w", id, err)
		}
		if started {
			s.Set.Trash.Started = true
			return errors.Join(ErrDeletionStarted, s.Registry.Save(ctx, s.Set))
		}
	}
	for _, id := range sortedStores(s.Stores) {
		if _, err := cloud.DeleteAll(ctx, s.Stores[id], s.trashKey(), nil); err != nil {
			return fmt.Errorf("backup: remove trash marker on %s: %w", id, err)
		}
	}
	s.Set.Trash = nil
	return s.Registry.Save(ctx, s.Set)
}

func (s *Service) deletionStarted(ctx context.Context, store cloud.Store) (bool, error) {
	_, err := store.Head(ctx, s.deletedKey(), "")
	if cloud.NotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// Purge deletes every stored version of the set from each open remote in
// remotes, then forgets the set once all of remotes are clean. Remotes that
// are not open stay for a later purge. Unless immediately is set, the trash
// period must have ended on the clock of the set's catalog remote.
func (s *Service) Purge(ctx context.Context, remotes []string, immediately bool) (Deletion, error) {
	if s.Set.Trash == nil {
		return Deletion{}, ErrNotInTrash
	}
	if !immediately && !s.Set.Trash.Started {
		if err := s.confirmDue(ctx); err != nil {
			return Deletion{}, err
		}
	}
	if !s.Set.Trash.Started {
		s.Set.Trash.Started = true
		if err := s.Registry.Save(ctx, s.Set); err != nil {
			return Deletion{}, err
		}
	}
	peers, err := s.peerUses(ctx)
	if err != nil {
		return Deletion{}, err
	}
	var result Deletion
	clean := 0
	var failures []error
	for _, id := range remotes {
		store := s.Stores[id]
		if store == nil {
			continue
		}
		if err := s.purgeRemote(ctx, id, store, peers, &result); err != nil {
			failures = append(failures, fmt.Errorf("backup: delete from %s: %w", id, err))
			continue
		}
		clean++
	}
	if len(failures) > 0 || clean < len(remotes) {
		return result, errors.Join(failures...)
	}
	if err = s.Registry.Remove(ctx, s.Set.ID); err != nil {
		return result, err
	}
	result.Done = true
	return result, os.RemoveAll(s.Directory)
}

// confirmDue reads the trash marker again on the set's catalog remote and
// compares its deadline with that bucket's clock, so a wrong local clock or
// an undelete on another device cannot start the deletion.
func (s *Service) confirmDue(ctx context.Context) error {
	for _, id := range sortedStores(s.Stores) {
		if started, err := s.deletionStarted(ctx, s.Stores[id]); err != nil || started {
			return err
		}
	}
	store := s.Stores[s.Set.CatalogTo]
	if store == nil {
		ids := sortedStores(s.Stores)
		if (s.Set.CatalogTo != "" && s.Set.CatalogTo != "local") || len(ids) == 0 {
			return ErrNotDue
		}
		store = s.Stores[ids[0]]
	}
	trash, found, err := readTrash(ctx, store, s.trashKey())
	if err != nil {
		return err
	}
	if !found {
		s.Set.Trash = nil
		return errors.Join(ErrNotInTrash, s.Registry.Save(ctx, s.Set))
	}
	clock, err := s.bucketNow(ctx, store)
	if err != nil {
		return err
	}
	if clock.Before(trash.Deadline) {
		s.Set.Trash.Deadline = trash.Deadline
		return errors.Join(ErrNotDue, s.Registry.Save(ctx, s.Set))
	}
	return nil
}

// bucketNow returns the time store records for a new object.
func (s *Service) bucketNow(ctx context.Context, store cloud.Store) (time.Time, error) {
	key := s.trashPrefix() + "clock.json"
	if _, err := s.putJSON(ctx, store, key, struct{}{}); err != nil && !errors.Is(err, cloud.ErrObjectExists) {
		return time.Time{}, err
	}
	h, err := store.Head(ctx, key, "")
	_, cleanErr := cloud.DeleteAll(ctx, store, key, nil)
	if err = errors.Join(err, cleanErr); err != nil {
		return time.Time{}, err
	}
	if h.ModifiedAt == nil {
		return now(), nil
	}
	return h.ModifiedAt.UTC(), nil
}

// purgeRemote writes the deleted marker first, so no device can undo or
// forget a deletion that has removed data, then deletes the set's objects.
func (s *Service) purgeRemote(ctx context.Context, id string, store cloud.Store, peers peerUses, result *Deletion) error {
	marker := model.DeletedMarker{Version: model.TrashVersion, Set: s.Set.ID}
	if _, err := s.putJSON(ctx, store, s.deletedKey(), marker); err != nil && !errors.Is(err, cloud.ErrObjectExists) {
		return err
	}
	for _, prefix := range s.setPrefixes(ctx) {
		freed, err := cloud.DeleteAll(ctx, store, prefix, peers.keeper(id, &result.Kept))
		if err != nil {
			return err
		}
		result.Freed.Add(freed)
	}
	for _, ref := range s.Set.Trash.Imported {
		if ref.Remote != id {
			continue
		}
		size, reason, err := inspectImported(ctx, store, ref, peers)
		if err != nil {
			return err
		}
		if reason != "" {
			result.Kept = append(result.Kept, KeptObject{Object: ref, Reason: reason})
			continue
		}
		if size.Objects == 0 {
			continue
		}
		deleter, ok := store.(cloud.VersionDeleter)
		if !ok {
			return cloud.ErrVersionsUnsupported
		}
		if err = deleter.DeleteVersion(ctx, ref.Key, ref.Version); err != nil && !cloud.NotFound(err) {
			return err
		}
		result.Freed.Add(size)
	}
	_, err := cloud.DeleteAll(ctx, store, s.trashPrefix(), func(o cloud.Object) bool { return o.Key == s.deletedKey() })
	return err
}

// DeletionSize counts what a purge would delete from each open remote.
func (s *Service) DeletionSize(ctx context.Context) (map[string]cloud.Freed, error) {
	imported := []model.ObjectRef{}
	if s.Set.Trash != nil {
		imported = s.Set.Trash.Imported
	} else {
		var err error
		if imported, err = s.externalObjects(ctx); err != nil {
			return nil, err
		}
	}
	peers, err := s.peerUses(ctx)
	if err != nil {
		return nil, err
	}
	sizes := map[string]cloud.Freed{}
	for _, id := range sortedStores(s.Stores) {
		store := s.Stores[id]
		var size cloud.Freed
		for _, prefix := range s.setPrefixes(ctx) {
			found, err := cloud.CountAll(ctx, store, prefix, peers.keeper(id, nil))
			if err != nil {
				return nil, fmt.Errorf("backup: list %s: %w", id, err)
			}
			size.Add(found)
		}
		for _, ref := range imported {
			if ref.Remote != id {
				continue
			}
			found, _, err := inspectImported(ctx, store, ref, peers)
			if err != nil {
				return nil, err
			}
			size.Add(found)
		}
		sizes[id] = size
	}
	return sizes, nil
}

// setPrefixes lists every prefix that holds only this set's objects: its
// archives, and its catalog and discovery records under each catalog prefix
// the set has used.
func (s *Service) setPrefixes(ctx context.Context) []string {
	catalogs := []string{s.Config.Catalog.Prefix, s.DiscoveryPrefix}
	if locators, err := s.Registry.Locators(ctx, s.Set.ID); err == nil {
		for _, v := range locators {
			catalogs = append(catalogs, v.Prefix)
		}
	}
	prefixes := []string{archivesPrefix + s.Set.ID + "/"}
	for _, p := range catalogs {
		if p = strings.TrimSuffix(p, "/"); p != "" {
			prefixes = append(prefixes, p+"/sets/"+s.Set.ID+"/", p+"/directory/v1/"+s.Set.ID+"/")
		}
	}
	slices.Sort(prefixes)
	return slices.Compact(prefixes)
}

// inspectImported returns the stored size of an imported archive, or why a
// purge must keep it.
func inspectImported(ctx context.Context, store cloud.Store, ref model.ObjectRef, peers peerUses) (cloud.Freed, string, error) {
	if set, ok := peers.user(ref); ok {
		return cloud.Freed{}, "set " + set + " uses it", nil
	}
	if len(peers.unread) > 0 {
		return cloud.Freed{}, "the catalog of set " + peers.unread[0] + " is not on this computer", nil
	}
	h, err := store.Head(ctx, ref.Key, ref.Version)
	if cloud.NotFound(err) {
		return cloud.Freed{}, "", nil
	}
	if err != nil {
		return cloud.Freed{}, "", err
	}
	// without versions, the key may now hold a different file than the import
	if versionID(ref.Version) == "" && (ref.ETag == "" || strings.Trim(h.ETag, `"`) != strings.Trim(ref.ETag, `"`)) {
		return cloud.Freed{}, "it changed after it was imported", nil
	}
	return cloud.Freed{Objects: 1, Bytes: h.Size}, "", nil
}

// externalObjects lists the set's archive copies stored outside its own
// archive prefix, such as imported archives.
func (s *Service) externalObjects(ctx context.Context) ([]model.ObjectRef, error) {
	refs, err := s.objectRefs(ctx)
	if err != nil {
		return nil, err
	}
	own := archivesPrefix + s.Set.ID + "/"
	return slices.DeleteFunc(refs, func(r model.ObjectRef) bool { return strings.HasPrefix(r.Key, own) }), nil
}

func (s *Service) objectRefs(ctx context.Context) ([]model.ObjectRef, error) {
	c, err := s.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var refs []model.ObjectRef
	err = c.EachSnapshot(ctx, func(v model.Snapshot) error {
		locations, err := c.Locations(ctx, v.ID)
		for _, l := range locations {
			if l.Key != "" {
				refs = append(refs, model.ObjectRef{Remote: l.Provider, Key: l.Key, Version: l.Version, ETag: l.ETag})
			}
		}
		return err
	})
	slices.SortFunc(refs, compareRefs)
	return slices.CompactFunc(refs, func(a, b model.ObjectRef) bool { return compareRefs(a, b) == 0 }), err
}

func compareRefs(a, b model.ObjectRef) int {
	return strings.Compare(a.Remote+"\x00"+a.Key+"\x00"+a.Version, b.Remote+"\x00"+b.Key+"\x00"+b.Version)
}

// peerUses records the archives that the other sets on this computer use.
type peerUses struct {
	// used maps remote, key and version to a set name
	used map[string]string
	// unread names the sets with a published catalog that is not on this
	// computer
	unread []string
}

func usedID(remote, key, version string) string {
	return remote + "\x00" + key + "\x00" + versionID(version)
}

// versionID returns "" for an object in a bucket without versions.
func versionID(version string) string {
	if version == "null" {
		return ""
	}
	return version
}

func (p peerUses) user(ref model.ObjectRef) (string, bool) {
	set, ok := p.used[usedID(ref.Remote, ref.Key, ref.Version)]
	return set, ok
}

// keeper returns a filter that keeps the objects on remote that another set
// uses, and records them in kept when kept is not nil.
func (p peerUses) keeper(remote string, kept *[]KeptObject) func(cloud.Object) bool {
	return func(o cloud.Object) bool {
		ref := model.ObjectRef{Remote: remote, Key: o.Key, Version: o.Version}
		set, ok := p.user(ref)
		if ok && kept != nil {
			*kept = append(*kept, KeptObject{Object: ref, Reason: "set " + set + " uses it"})
		}
		return ok
	}
}

func (s *Service) peerUses(ctx context.Context) (peerUses, error) {
	uses := peerUses{used: map[string]string{}}
	var sets []model.Set
	if err := s.Registry.Each(ctx, func(v model.Set) error {
		if v.ID != s.Set.ID {
			sets = append(sets, v)
		}
		return nil
	}); err != nil {
		return uses, err
	}
	root := filepath.Dir(s.Directory)
	for _, v := range sets {
		peer := &Service{Config: s.Config, Registry: s.Registry, Set: v, Directory: filepath.Join(root, v.ID)}
		if _, err := os.Stat(peer.path()); err != nil {
			// a set that never published a catalog has no archives
			if locators, err := s.Registry.Locators(ctx, v.ID); err != nil {
				return uses, err
			} else if len(locators) > 0 {
				uses.unread = append(uses.unread, v.Name)
			}
			continue
		}
		refs, err := peer.objectRefs(ctx)
		if err != nil {
			return uses, fmt.Errorf("backup: read catalog of %s: %w", v.Name, err)
		}
		for _, r := range refs {
			uses.used[usedID(r.Remote, r.Key, r.Version)] = v.Name
		}
	}
	return uses, nil
}

// SharedArchives counts the set's archives that each other readable set on
// this computer also uses.
func (s *Service) SharedArchives(ctx context.Context) (map[string]int, error) {
	refs, err := s.objectRefs(ctx)
	if err != nil {
		return nil, err
	}
	peers, err := s.peerUses(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, r := range refs {
		if set, ok := peers.user(r); ok {
			counts[set]++
		}
	}
	return counts, nil
}

// checkImportUnused refuses an archive that another set on this computer
// already holds, or may hold, so deleting one set never breaks another.
func (s *Service) checkImportUnused(ctx context.Context, remote, key, version string) error {
	peers, err := s.peerUses(ctx)
	if err != nil {
		return err
	}
	if set, ok := peers.user(model.ObjectRef{Remote: remote, Key: key, Version: version}); ok {
		return fmt.Errorf("backup: %s is already in set %s; import it there or use another key", key, set)
	}
	if len(peers.unread) > 0 {
		return fmt.Errorf("backup: cannot check whether set %s uses %s; run glesha catalog unlock %s", peers.unread[0], key, peers.unread[0])
	}
	return nil
}

func sortedStores(stores map[string]cloud.Store) []string {
	ids := make([]string, 0, len(stores))
	for id := range stores {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

type trashMarkers struct {
	deleted map[string]bool
	trash   map[string]model.Trash
}

// discoverTrash reads the deletion markers in store; s is a service without a
// set, as DiscoverState uses it.
func (s *Service) discoverTrash(ctx context.Context, store cloud.Store) (trashMarkers, error) {
	markers := trashMarkers{deleted: map[string]bool{}, trash: map[string]model.Trash{}}
	prefix := s.tombstonesPrefix()
	err := cloud.List(ctx, store, prefix, func(object cloud.Object) error {
		set, name, _ := strings.Cut(strings.TrimPrefix(object.Key, prefix), "/")
		if _, err := uuid.Parse(set); err != nil {
			return fmt.Errorf("backup: malformed trash marker key %q", object.Key)
		}
		switch name {
		case "deleted.json":
			markers.deleted[set] = true
		case "trash.json":
			trash, found, err := readTrash(ctx, store, object.Key)
			if err != nil || !found {
				return err
			}
			markers.trash[set] = trash
		}
		return nil
	})
	return markers, err
}

// applyTrash records the trash state of known sets. The catalog remote of a
// set decides whether it is in the trash, so an undelete on another device
// takes effect here. A deleted marker means a deletion has started; the set
// stays known until a purge has cleaned every remote.
func (s *Service) applyTrash(ctx context.Context, remote string, markers trashMarkers) error {
	var sets []model.Set
	if err := s.Registry.Each(ctx, func(v model.Set) error { sets = append(sets, v); return nil }); err != nil {
		return err
	}
	for _, v := range sets {
		trash, found := markers.trash[v.ID]
		switch {
		case v.Trash != nil && v.Trash.Started:
			continue
		case markers.deleted[v.ID]:
			next := model.Trash{Started: true}
			if v.Trash != nil {
				next = *v.Trash
				next.Started = true
			}
			if found && len(next.Imported) == 0 {
				next.Imported = trash.Imported
			}
			v.Trash = &next
		case found && (v.Trash == nil || !v.Trash.Deadline.Equal(trash.Deadline)):
			v.Trash = &trash
		case !found && v.Trash != nil && v.CatalogTo == remote:
			v.Trash = nil
		default:
			continue
		}
		if err := s.Registry.Save(ctx, v); err != nil {
			return err
		}
	}
	return nil
}
