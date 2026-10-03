package backup

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"glesha/archive"
	"glesha/cloud"
	"glesha/crypt"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
)

type revision struct {
	Version                    int `json:"version"`
	Set, ID, Parent, Key, Hash string
	DescriptorHash             string
	Number                     int64
	Checkpoint                 bool
}
type Descriptor struct {
	Version int            `json:"version"`
	Set     model.Set      `json:"set"`
	Remotes []model.Remote `json:"remotes"`
}

func (s *Service) prefix() string {
	return strings.TrimSuffix(s.Config.Catalog.Prefix, "/") + "/sets/" + s.Set.ID + "/"
}
func (s *Service) metadataStore() (cloud.Store, error) {
	if s.Set.CatalogTo == "local" {
		return nil, nil
	}
	store := s.Stores[s.Set.CatalogTo]
	if store == nil {
		return nil, fmt.Errorf("backup: catalog remote is unavailable")
	}
	return store, nil
}
func (s *Service) revisions(ctx context.Context, store cloud.Store) ([]revision, error) {
	folder := "heads/"
	conditional := s.Kinds[s.Set.CatalogTo] == "aws"
	if conditional {
		folder = "commits/"
	}
	out := []revision{}
	err := cloud.List(ctx, store, s.prefix()+folder, func(h cloud.Object) error {
		name := strings.TrimSuffix(filepath.Base(h.Key), ".json")
		numberText, id, ok := strings.Cut(name, "-")
		if conditional {
			numberText = name
			id = ""
			ok = true
		}
		number, err := strconv.ParseInt(numberText, 10, 64)
		if !ok || err != nil || number < 1 {
			return fmt.Errorf("backup: malformed revision key")
		}
		if !conditional {
			if _, err = uuid.Parse(id); err != nil {
				return fmt.Errorf("backup: malformed revision identity")
			}
		}
		out = append(out, revision{ID: id, Number: number, Checkpoint: number == 1 || number%32 == 0})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	start := 0
	for i, v := range out {
		if i > 0 && v.Number != out[i-1].Number+1 {
			return nil, fmt.Errorf("%w: concurrent or discontinuous metadata histories preserved remotely", ErrConflict)
		}
		if v.Checkpoint {
			start = i
		}
	}
	// cached immutable manifests make routine sync a listing plus new revisions only
	for i := range out {
		hint := out[i]
		name := fmt.Sprintf("%020d-%s.json", hint.Number, hint.ID)
		if conditional {
			name = fmt.Sprintf("%020d.json", hint.Number)
		}
		cache := filepath.Join(s.Directory, "revision-"+name)
		b, err := os.ReadFile(cache)
		if os.IsNotExist(err) {
			if i < start-1 {
				continue
			}
			r, _, getErr := store.Get(ctx, s.prefix()+folder+name, "")
			if getErr != nil {
				return nil, getErr
			}
			b, err = io.ReadAll(io.LimitReader(r, (1<<20)+1))
			r.Close()
		}
		if err != nil {
			return nil, err
		}
		if len(b) > 1<<20 {
			return nil, fmt.Errorf("backup: oversized revision")
		}
		var v revision
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		if _, err = uuid.Parse(v.ID); err != nil {
			return nil, fmt.Errorf("backup: malformed revision identity")
		}
		if v.Version != 2 || v.Set != s.Set.ID || !conditional && v.ID != hint.ID || v.Number != hint.Number || hint.Checkpoint && !v.Checkpoint || !strings.HasPrefix(v.Key, s.prefix()+"batches/") {
			return nil, fmt.Errorf("backup: invalid remote revision")
		}
		if i > 0 && out[i-1].ID != "" && v.Parent != out[i-1].ID {
			return nil, fmt.Errorf("%w: disconnected metadata history", ErrConflict)
		}
		out[i] = v
		if err = os.WriteFile(cache, b, 0600); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Service) publish(ctx context.Context, c repository.Cataloger) error {
	if err := s.checkCatalogSecret(ctx, c); err != nil {
		return err
	}
	store, err := s.metadataStore()
	if err != nil {
		return err
	}
	if store == nil {
		return c.SetMeta(ctx, "dirty", "")
	}
	label := "Publishing catalog"
	if remote, err := s.Registry.Remote(ctx, s.Set.CatalogTo); err == nil {
		label += " to " + remote.Name
	}
	progress := L.StartProgress(ctx, label, -1)
	defer progress.Finish()
	ctx = L.WithUploadProgress(L.WithProgress(ctx, nil), progress)
	current, err := c.GetMeta(ctx, "remote_revision")
	if err != nil {
		return err
	}
	existing, err := s.revisions(ctx, store)
	if err != nil {
		return err
	}
	pending, err := c.GetMeta(ctx, "publication")
	if err != nil {
		return err
	}
	var v revision
	if pending != "" {
		if err = json.Unmarshal([]byte(pending), &v); err != nil {
			return err
		}
	} else {
		number := int64(1)
		if len(existing) > 0 {
			head := existing[len(existing)-1]
			if head.ID != current {
				return fmt.Errorf("%w: pull or reconcile before publication", ErrConflict)
			}
			number = head.Number + 1
		} else if current != "" {
			return fmt.Errorf("%w: remote history disappeared", ErrConflict)
		}
		force, err := c.GetMeta(ctx, "force_checkpoint")
		if err != nil {
			return err
		}
		v = revision{Version: 2, Set: s.Set.ID, ID: uuid.NewString(), Parent: current, Number: number, Checkpoint: number == 1 || number%32 == 0 || force == "true"}
		v.Key = s.prefix() + "batches/" + v.ID + ".db.gz"
		if s.Config.Catalog.Encrypted {
			v.Key += ".gpg"
		}
		b, _ := json.Marshal(v)
		if err = c.SetMeta(ctx, "publication", string(b)); err != nil {
			return err
		}
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
	file := filepath.Join(s.Directory, "publication-"+v.ID)
	if v.Hash == "" {
		plain := file + ".db"
		defer os.Remove(plain)
		if _, err = os.Stat(plain); err == nil {
			os.Remove(plain)
		}
		if err = c.Export(ctx, plain, v.Checkpoint); err != nil {
			return err
		}
		if err = s.encodeMetadata(ctx, plain, file); err != nil {
			return err
		}
		v.Hash, _, err = archive.Hash(ctx, file)
		if err != nil {
			return err
		}
		b, _ := json.Marshal(v)
		if err = c.SetMeta(ctx, "publication", string(b)); err != nil {
			return err
		}
	}
	hash, _, err := archive.Hash(ctx, file)
	if err != nil || hash != v.Hash {
		return fmt.Errorf("backup: publication file changed or disappeared")
	}
	if _, err = cloud.Create(ctx, store, v.Key, file, "STANDARD"); err != nil {
		return fmt.Errorf("%w: metadata batch upload: %w", ErrPending, err)
	}
	descriptor := Descriptor{Version: 2, Set: s.Set}
	remoteIDs := map[string]bool{}
	for _, id := range s.Set.To {
		remoteIDs[id] = true
	}
	if s.Set.CatalogTo != "local" {
		remoteIDs[s.Set.CatalogTo] = true
	}
	if err = c.EachSnapshot(ctx, func(snapshot model.Snapshot) error {
		locations, err := c.Locations(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		for _, location := range locations {
			remoteIDs[location.Provider] = true
		}
		return nil
	}); err != nil {
		return err
	}
	ordered := make([]string, 0, len(remoteIDs))
	for id := range remoteIDs {
		if id != "" {
			ordered = append(ordered, id)
		}
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		remote, err := s.Registry.Remote(ctx, id)
		if err != nil {
			return err
		}
		descriptor.Remotes = append(descriptor.Remotes, remote)
	}
	descriptorFile := file + ".descriptor"
	b, _ := json.Marshal(descriptor)
	if _, statErr := os.Stat(descriptorFile); os.IsNotExist(statErr) {
		if err = s.writeMetadataBytes(ctx, b, descriptorFile); err != nil {
			return err
		}
	} else if statErr != nil {
		return statErr
	}
	descriptorHash, _, err := archive.Hash(ctx, descriptorFile)
	if err != nil {
		return err
	}
	if v.DescriptorHash != "" && v.DescriptorHash != descriptorHash {
		return fmt.Errorf("backup: pending descriptor checksum mismatch")
	}
	if v.DescriptorHash == "" {
		v.DescriptorHash = descriptorHash
		b, _ := json.Marshal(v)
		if err = c.SetMeta(ctx, "publication", string(b)); err != nil {
			return err
		}
	}
	// descriptors are revision-specific so discovery never needs a destructive overwrite
	if _, err = cloud.Create(ctx, store, s.prefix()+"descriptors/"+fmt.Sprintf("%020d-%s", v.Number, v.ID)+s.metadataSuffix(), descriptorFile, "STANDARD"); err != nil {
		return err
	}
	manifestFile := file + ".head"
	defer os.Remove(manifestFile)
	b, _ = json.Marshal(v)
	if err = os.WriteFile(manifestFile, b, 0600); err != nil {
		return err
	}
	if _, err = cloud.Create(ctx, store, s.prefix()+"heads/"+fmt.Sprintf("%020d-%s.json", v.Number, v.ID), manifestFile, "STANDARD"); err != nil {
		return err
	}
	if s.Kinds[s.Set.CatalogTo] == "aws" {
		// this conditional commit is the publication point; earlier objects are proposals
		key := s.prefix() + "commits/" + fmt.Sprintf("%020d.json", v.Number)
		if _, err = cloud.Create(ctx, store, key, manifestFile, "STANDARD"); err != nil {
			if errors.Is(err, cloud.ErrObjectExists) || cloud.Conflict(err) {
				return fmt.Errorf("%w: another writer committed this revision", ErrConflict)
			}
			return fmt.Errorf("%w: catalog commit failed: %w", ErrPending, err)
		}
	}
	if _, err = s.revisions(ctx, store); err != nil {
		return err
	}
	return s.markPublished(ctx, c, v)
}
func (s *Service) metadataSuffix() string {
	if s.Config.Catalog.Encrypted {
		return ".json.gz.gpg"
	}
	return ".json.gz"
}
func (s *Service) markPublished(ctx context.Context, c repository.Cataloger, v revision) error {
	if err := s.recordMetadataPublication(v); err != nil {
		L.Debug("metadata storage estimate unavailable: " + err.Error())
	}
	for k, value := range map[string]string{"remote_revision": v.ID, "remote_number": strconv.FormatInt(v.Number, 10), "publication": "", "dirty": "", "force_checkpoint": ""} {
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
func (s *Service) encodeMetadata(ctx context.Context, plain, file string) error {
	f, err := os.Open(plain)
	if err != nil {
		return err
	}
	defer f.Close()
	return s.writeMetadata(ctx, f, file)
}
func (s *Service) writeMetadataBytes(ctx context.Context, b []byte, file string) error {
	return s.writeMetadata(ctx, strings.NewReader(string(b)), file)
}
func (s *Service) writeMetadata(ctx context.Context, r io.Reader, file string) (err error) {
	tmp, err := file_io.Temp(s.Directory)
	if err != nil {
		return err
	}
	defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
	var dst io.Writer = tmp
	var encrypted io.WriteCloser
	if s.Config.Catalog.Encrypted {
		encrypted, err = crypt.Encrypt(ctx, tmp, s.CatalogPassword)
		if err != nil {
			return err
		}
		dst = encrypted
	}
	zipped := gzip.NewWriter(dst)
	_, err = copyContext(ctx, zipped, r)
	if closeErr := zipped.Close(); err == nil {
		err = closeErr
	}
	if encrypted != nil {
		if closeErr := encrypted.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return file_io.Publish(tmp.Name(), file)
}
func (s *Service) metadataReader(ctx context.Context, r io.Reader, key string) (io.ReadCloser, error) {
	if strings.HasSuffix(key, ".gpg") {
		plain, err := crypt.Decrypt(ctx, r, s.CatalogPassword)
		if err != nil {
			return nil, err
		}
		r = plain
	}
	return gzip.NewReader(file_io.ContextReader{Ctx: ctx, Reader: r})
}
func (s *Service) Pull(ctx context.Context) error {
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
	store, err := s.metadataStore()
	if err != nil {
		return err
	}
	rs, err := s.revisions(ctx, store)
	if err != nil {
		return err
	}
	if len(rs) == 0 {
		return nil
	}
	current, err := c.GetMeta(ctx, "remote_revision")
	if err != nil {
		return err
	}
	if current == rs[len(rs)-1].ID {
		return nil
	}
	start := 0
	found := current == ""
	for i, r := range rs {
		if r.ID == current {
			start = i + 1
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: local revision not found remotely", ErrConflict)
	}
	checkpoint := start
	for i := start; i < len(rs); i++ {
		if rs[i].Checkpoint {
			checkpoint = i
		}
	}
	temp, err := file_io.Temp(s.Directory)
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
	for i := checkpoint; i < len(rs); i++ {
		rev := rs[i]
		r, object, err := store.Get(ctx, rev.Key, "")
		if err != nil {
			return err
		}
		downloaded, err := file_io.Temp(s.Directory)
		if err != nil {
			r.Close()
			return err
		}
		progress := L.StartProgress(ctx, "Downloading catalog", object.Size)
		_, err = copyContext(ctx, downloaded, progress.Reader(r))
		progress.Finish()
		r.Close()
		downloaded.Close()
		if err != nil {
			os.Remove(downloaded.Name())
			return err
		}
		hash, _, err := archive.Hash(L.WithProgressLabel(ctx, "Checking downloaded catalog"), downloaded.Name())
		if err != nil || hash != rev.Hash {
			os.Remove(downloaded.Name())
			return fmt.Errorf("backup: metadata checksum mismatch")
		}
		encrypted, err := os.Open(downloaded.Name())
		if err != nil {
			return err
		}
		body, err := s.metadataReader(ctx, encrypted, rev.Key)
		if err != nil {
			encrypted.Close()
			os.Remove(downloaded.Name())
			return err
		}
		plain, err := file_io.Temp(s.Directory)
		if err != nil {
			body.Close()
			encrypted.Close()
			return err
		}
		_, err = copyContext(ctx, plain, body)
		body.Close()
		encrypted.Close()
		plain.Close()
		os.Remove(downloaded.Name())
		if err != nil {
			os.Remove(plain.Name())
			return err
		}
		identity, identityErr := repository.ReadCatalogMeta(ctx, plain.Name(), "publication")
		var published revision
		if identityErr != nil || json.Unmarshal([]byte(identity), &published) != nil || published.ID != rev.ID || published.Set != rev.Set || published.Parent != rev.Parent || published.Number != rev.Number || published.Key != rev.Key || published.Checkpoint != rev.Checkpoint {
			os.Remove(plain.Name())
			return fmt.Errorf("backup: authenticated metadata revision identity differs")
		}
		err = staged.Apply(ctx, plain.Name())
		os.Remove(plain.Name())
		if err != nil {
			return err
		}
		setID, err := staged.GetMeta(ctx, "set_id")
		if err != nil {
			return err
		}
		if setID != s.Set.ID {
			return fmt.Errorf("backup: remote catalog belongs to another set")
		}
		if err = s.markPublished(ctx, staged, rev); err != nil {
			return err
		}
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
		if err = s.publish(ctx, p); err != nil {
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
	if err = s.publish(ctx, p); err != nil {
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
