package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"glesha/internal/archive"
	"glesha/internal/cloud"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
	"glesha/internal/fsutil"
	"glesha/internal/logger"
)

type revision struct {
	Version                    model.StateVersion `json:"version"`
	Set, ID, Parent, Key, Hash string
	DescriptorHash             string
	Number                     int64
	Checkpoint                 bool
	Redirect                   *model.Locator  `json:"redirect,omitempty"`
	AuthorityHistory           []model.Locator `json:"authority_history,omitempty"`
}
type Descriptor struct {
	Version model.StateVersion `json:"version"`
	Set     model.Set          `json:"set"`
	Remotes []model.Remote     `json:"remotes"`
}

// authorityMode says whether a catalog transfer honours a retired authority's redirect.
type authorityMode int

const (
	// currentAuthority refuses to publish to, and follows redirects from, a retired authority.
	currentAuthority authorityMode = iota
	// retiredAuthority reads and writes a retired authority in place; only move uses it.
	retiredAuthority
)

// publish uploads the local catalog changes as the next revision on the catalog
// remote. A publication interrupted part way resumes from the saved pending revision.
func (s *Service) publish(ctx context.Context, c repository.Cataloger, mode authorityMode) error {
	ctx = cloud.WithTransferLabel(ctx, "catalog metadata")
	if _, err := catalogStateVersion(ctx, c); err != nil {
		return fmt.Errorf("backup: publish catalog: %w", err)
	}
	if err := s.checkCatalogSecret(ctx, c); err != nil {
		return err
	}
	published, err := s.openRemoteCatalog()
	if err != nil {
		return err
	}
	if published == nil {
		return c.SetMeta(ctx, "dirty", "")
	}
	progress := logger.StartProgress(ctx, s.publishLabel(ctx), -1)
	defer progress.Finish()
	ctx = logger.WithUploadProgress(logger.WithProgress(ctx, nil), progress)
	current, err := c.GetMeta(ctx, "remote_revision")
	if err != nil {
		return err
	}
	existing, err := published.revisions(ctx)
	if err != nil {
		return err
	}
	if mode == currentAuthority {
		if err = s.checkPublishingAuthority(ctx, published.store); err != nil {
			return err
		}
		if len(existing) > 0 && existing[len(existing)-1].Redirect != nil {
			return fmt.Errorf("%w: catalog authority retired; run catalog sync before publication", ErrConflict)
		}
	}
	v, err := published.pendingRevision(ctx, c, existing, current)
	if err != nil {
		return err
	}
	if s.Config.Catalog.Encrypted != strings.HasSuffix(v.Key, ".gpg") {
		return fmt.Errorf("backup: catalog encryption setting conflicts with pending publication")
	}
	for _, r := range existing {
		if r.ID == v.ID {
			return s.markPublished(ctx, c, v)
		}
	}
	if len(existing) > 0 && existing[len(existing)-1].ID != current {
		return fmt.Errorf("%w: pending revision has a different remote parent", ErrConflict)
	}
	if err = v.Version.Validate(); err != nil {
		return fmt.Errorf("backup: pending publication: %w", err)
	}
	file := filepath.Join(s.Directory, "publication-"+v.ID)
	if err = published.writeBatch(ctx, c, &v, file); err != nil {
		return err
	}
	if _, err = cloud.Create(ctx, published.store, v.Key, file, "STANDARD"); err != nil {
		return fmt.Errorf("%w: metadata batch upload: %w", ErrPending, err)
	}
	descriptorFile, err := s.writeDescriptor(ctx, published, c, &v, file)
	if err != nil {
		return err
	}
	// descriptors are revision-specific so discovery never needs a destructive overwrite
	if _, err = cloud.Create(ctx, published.store, published.prefix+"descriptors/"+fmt.Sprintf("%020d-%s", v.Number, v.ID)+published.metadataSuffix(), descriptorFile, "STANDARD"); err != nil {
		return err
	}
	if err = published.commitRevision(ctx, v, file); err != nil {
		return err
	}
	if _, err = published.revisions(ctx); err != nil {
		return err
	}
	return s.markPublished(ctx, c, v)
}

func (s *Service) publishLabel(ctx context.Context) string {
	label := "Publishing catalog"
	if remote, err := s.Registry.Remote(ctx, s.Set.CatalogTo); err == nil {
		name := remote.Name
		if name == remote.ID || name == "" {
			name = remote.Kind + "/" + remote.Bucket
		}
		label += " to " + logger.Value(name)
	}
	return label
}

// descriptorRemotes lists every remote the set's catalog refers to, sorted by ID.
func (s *Service) descriptorRemotes(ctx context.Context, c repository.Cataloger) ([]model.Remote, error) {
	ids := map[string]bool{}
	for _, id := range s.Set.To {
		ids[id] = true
	}
	if s.Set.CatalogTo != "local" {
		ids[s.Set.CatalogTo] = true
	}
	if err := c.EachSnapshot(ctx, func(snapshot model.Snapshot) error {
		locations, err := c.Locations(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		for _, location := range locations {
			ids[location.Provider] = true
		}
		return nil
	}); err != nil {
		return nil, err
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		if id != "" {
			ordered = append(ordered, id)
		}
	}
	sort.Strings(ordered)
	var remotes []model.Remote
	for _, id := range ordered {
		remote, err := s.Registry.Remote(ctx, id)
		if err != nil {
			return nil, err
		}
		remotes = append(remotes, remote)
	}
	return remotes, nil
}

// writeDescriptor writes the set descriptor for v next to file, once, and records its hash.
func (s *Service) writeDescriptor(ctx context.Context, published *remoteCatalog, c repository.Cataloger, v *revision, file string) (string, error) {
	remotes, err := s.descriptorRemotes(ctx, c)
	if err != nil {
		return "", err
	}
	descriptor := Descriptor{Version: v.Version, Set: portableSet(s.Set), Remotes: remotes}
	descriptorFile := file + ".descriptor"
	b, _ := json.Marshal(descriptor)
	if _, statErr := os.Stat(descriptorFile); os.IsNotExist(statErr) {
		if err = published.writeMetadataBytes(ctx, b, descriptorFile); err != nil {
			return "", err
		}
	} else if statErr != nil {
		return "", statErr
	}
	descriptorHash, _, err := archive.Hash(logger.WithProgressLabel(ctx, "Checking catalog descriptor"), descriptorFile)
	if err != nil {
		return "", err
	}
	if v.DescriptorHash != "" && v.DescriptorHash != descriptorHash {
		return "", fmt.Errorf("backup: pending descriptor checksum mismatch")
	}
	if v.DescriptorHash == "" {
		v.DescriptorHash = descriptorHash
		b, _ := json.Marshal(v)
		if err = c.SetMeta(ctx, "publication", string(b)); err != nil {
			return "", err
		}
	}
	return descriptorFile, nil
}

func (s *Service) markPublished(ctx context.Context, c repository.Cataloger, v revision) error {
	if err := s.recordMetadataPublication(v); err != nil {
		logger.Debug("metadata storage estimate unavailable: " + err.Error())
	}
	for k, value := range map[string]string{
		"remote_revision":  v.ID,
		"remote_number":    strconv.FormatInt(v.Number, 10),
		"publication":      "",
		"dirty":            "",
		"force_checkpoint": "",
		"baseline_changed": "",
		"last_sync":        now().Format(time.RFC3339Nano),
	} {
		if err := c.SetMeta(ctx, k, value); err != nil {
			return err
		}
	}
	if err := repository.MarkSynced(ctx, c); err != nil {
		return err
	}
	os.Remove(filepath.Join(s.Directory, "publication-"+v.ID))
	os.Remove(filepath.Join(s.Directory, "publication-"+v.ID) + ".descriptor")
	return nil
}

// Pull downloads catalog revisions newer than the local catalog, following authority redirects.
func (s *Service) Pull(ctx context.Context) error { return s.pull(ctx, currentAuthority, 0) }

func (s *Service) pull(ctx context.Context, mode authorityMode, hops int) error {
	if s.Set.CatalogTo == "local" || s.Set.CatalogTo == "" {
		return nil
	}
	if s.Pending() {
		return fmt.Errorf("%w: finish pending operation before pull", ErrPending)
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	dirty, err := c.GetMeta(ctx, "dirty")
	if err != nil {
		return err
	}
	if dirty != "" {
		return fmt.Errorf("backup: unpublished local changes; catalog push first")
	}
	published, err := s.openRemoteCatalog()
	if err != nil {
		return err
	}
	rs, err := published.revisions(ctx)
	if err != nil {
		return err
	}
	if len(rs) == 0 {
		return nil
	}
	if rs[len(rs)-1].Redirect != nil && mode == currentAuthority {
		if err = s.followAuthority(ctx, rs[len(rs)-1], hops); err != nil {
			return err
		}
		if s.Stores[s.Set.CatalogTo] == nil {
			return ErrAuthorityMoved
		}
		return s.pull(ctx, currentAuthority, hops+1)
	}
	current, err := c.GetMeta(ctx, "remote_revision")
	if err != nil {
		return err
	}
	authority, err := c.GetMeta(ctx, model.StateAuthorityKey)
	if err != nil {
		return err
	}
	if authority == "" {
		authority = s.localAuthority
	}
	authorityChanged := authority != "" && authority != s.Set.CatalogTo
	if authorityChanged {
		current = ""
	}
	if current == rs[len(rs)-1].ID {
		return nil
	}
	return s.applyRevisions(ctx, c, published, rs, current, authorityChanged)
}

func (s *Service) applyRevisions(ctx context.Context, c repository.Cataloger, published *remoteCatalog, rs []revision, current string, authorityChanged bool) error {
	start, checkpoint, err := revisionRange(rs, current)
	if err != nil {
		return err
	}
	temp, err := fsutil.Temp(s.Directory)
	if err != nil {
		return err
	}
	name := temp.Name()
	temp.Close()
	os.Remove(name)
	defer os.Remove(name)
	if current != "" && checkpoint == start && !rs[checkpoint].Checkpoint {
		if err = c.Vacuum(ctx, name); err != nil {
			return err
		}
	}
	staged, err := repository.NewCatalogRepository(ctx, name)
	if err != nil {
		return err
	}
	defer staged.Close()
	progress := logger.StartProgress(ctx, "Applying catalog updates", -1)
	defer progress.Finish()
	ctx = logger.WithProgress(ctx, nil)
	for _, rev := range rs[checkpoint:] {
		if err = s.applyRevision(ctx, published, staged, rev); err != nil {
			return err
		}
	}
	if authorityChanged {
		if err = preservesSnapshots(ctx, c, staged); err != nil {
			return err
		}
	}
	if err = repository.PreserveLocalPaths(ctx, c, staged); err != nil {
		return err
	}
	if err = repository.Preserve(ctx, c, filepath.Join(s.Directory, "before-pull-"+newID()+".db")); err != nil {
		return err
	}
	if err = staged.Close(); err != nil {
		return err
	}
	if err = c.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path())
}

// revisionRange finds the revisions after current and the last checkpoint among
// them, from which applying starts.
func revisionRange(rs []revision, current string) (start, checkpoint int, err error) {
	found := current == ""
	for i, r := range rs {
		if r.ID == current {
			start = i + 1
			found = true
		}
	}
	if !found {
		return 0, 0, fmt.Errorf("%w: local revision not found remotely", ErrConflict)
	}
	checkpoint = start
	for i := start; i < len(rs); i++ {
		if rs[i].Checkpoint {
			checkpoint = i
		}
	}
	return start, checkpoint, nil
}

// applyRevision merges one verified, decrypted revision into the staged catalog.
func (s *Service) applyRevision(ctx context.Context, published *remoteCatalog, staged repository.Cataloger, rev revision) error {
	plain, err := published.decryptRevision(ctx, rev)
	if err != nil {
		return err
	}
	defer os.Remove(plain)
	identity, identityErr := repository.ReadCatalogMeta(ctx, plain, "publication")
	var recorded revision
	if identityErr != nil || json.Unmarshal([]byte(identity), &recorded) != nil || !sameRevision(recorded, rev) {
		return fmt.Errorf("backup: authenticated metadata revision identity differs")
	}
	if err = staged.Apply(ctx, plain); err != nil {
		return err
	}
	setID, err := staged.GetMeta(ctx, "set_id")
	if err != nil {
		return err
	}
	if setID != s.Set.ID {
		return fmt.Errorf("backup: remote catalog belongs to another set")
	}
	return s.markPublished(ctx, staged, rev)
}

// sameRevision reports whether the identity stored inside a batch matches its head.
func sameRevision(a, b revision) bool {
	return a.Version == b.Version && a.ID == b.ID && a.Set == b.Set && a.Parent == b.Parent && a.Number == b.Number && a.Key == b.Key && a.Checkpoint == b.Checkpoint
}

// preservesSnapshots checks that a catalog from a new authority keeps every local snapshot.
func preservesSnapshots(ctx context.Context, c, staged repository.Cataloger) error {
	return c.EachSnapshot(ctx, func(previous model.Snapshot) error {
		incoming, err := staged.Snapshot(ctx, previous.ID)
		if err != nil || incoming.Hash != previous.Hash || !sameManifest(incoming.Manifest, previous.Manifest) {
			return fmt.Errorf("%w: new authority does not preserve snapshot %s", ErrConflict, previous.ID)
		}
		return nil
	})
}
func (s *Service) Push(ctx context.Context) error {
	if s.Pending() {
		p, err := repository.NewCatalogRepository(ctx, s.pending())
		if err != nil {
			return err
		}
		defer p.Close()
		kind, _ := p.GetMeta(ctx, "operation")
		if kind != "push" {
			return fmt.Errorf("%w: another operation exists", ErrPending)
		}
		if err = s.publish(ctx, p, currentAuthority); err != nil {
			return err
		}
		return s.finishPending(ctx, p)
	}
	c, err := s.Catalog(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if err = c.Vacuum(ctx, s.pending()); err != nil {
		return err
	}
	p, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return err
	}
	defer p.Close()
	if err = p.SetMeta(ctx, "operation", "push"); err != nil {
		return err
	}
	if err = s.publish(ctx, p, currentAuthority); err != nil {
		return err
	}
	return s.finishPending(ctx, p)
}
func (s *Service) Abandon(ctx context.Context) error {
	if !s.Pending() {
		return fmt.Errorf("backup: no pending work")
	}
	c, err := repository.NewCatalogRepository(ctx, s.pending())
	if err != nil {
		return err
	}
	kind, err := c.GetMeta(ctx, "operation")
	if err != nil {
		c.Close()
		return err
	}
	spool := ""
	if kind == "run" {
		id, err := c.GetMeta(ctx, "pending_snapshot")
		if err != nil {
			c.Close()
			return err
		}
		v, err := c.Snapshot(ctx, id)
		if err != nil {
			c.Close()
			return err
		}
		if v.Layout == "chunked" {
			if err = uuid.Validate(v.ID); err != nil {
				c.Close()
				return fmt.Errorf("backup: invalid pending snapshot identity")
			}
			spool = filepath.Join(s.Directory, "chunks-"+v.ID)
		}
	}
	if err = c.Close(); err != nil {
		return err
	}
	if err = os.Rename(s.pending(), filepath.Join(s.Directory, "abandoned-"+newID()+".db")); err != nil {
		return err
	}
	if spool != "" {
		return os.RemoveAll(spool)
	}
	return nil
}
