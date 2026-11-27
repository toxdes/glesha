package backup

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"glesha/internal/archive"
	"glesha/internal/cloud"
	"glesha/internal/crypt"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
	"glesha/internal/fsutil"
	"glesha/internal/logger"
)

// remoteCatalog is one set's published catalog on its authority remote, and
// the local cache of it: revision heads and commits, catalog batches and
// descriptors under prefix. It is a snapshot of the Service settings it was
// built from, so build a new one after the authority, prefix or catalog
// encryption changes.
type remoteCatalog struct {
	store     cloud.Store // nil when working from the local cache only
	remote    string      // authority remote ID
	set       string
	prefix    string
	encrypted bool
	password  []byte
	cache     string // directory holding the cached metadata
}

// remoteCatalog describes s's catalog at its current authority.
func (s *Service) remoteCatalog() *remoteCatalog {
	return &remoteCatalog{
		store:     s.Stores[s.Set.CatalogTo],
		remote:    s.Set.CatalogTo,
		set:       s.Set.ID,
		prefix:    s.prefix(),
		encrypted: s.Config.Catalog.Encrypted,
		password:  s.CatalogPassword,
		cache:     s.Directory,
	}
}

// openRemoteCatalog is remoteCatalog for operations that contact the
// authority; it returns nil for a catalog kept only locally.
func (s *Service) openRemoteCatalog() (*remoteCatalog, error) {
	if s.Set.CatalogTo == "local" {
		return nil, nil
	}
	if s.Stores[s.Set.CatalogTo] == nil {
		return nil, fmt.Errorf("backup: catalog remote is unavailable")
	}
	return s.remoteCatalog(), nil
}

func (s *Service) prefix() string {
	return strings.TrimSuffix(s.Config.Catalog.Prefix, "/") + "/sets/" + s.Set.ID + "/"
}

func (c *remoteCatalog) conditional() bool {
	return c.store != nil && c.store.Provider().Capabilities.ConditionalWrites
}

// metadataReader decompresses catalog metadata read from key, decrypting it
// with password when key is encrypted.
func metadataReader(ctx context.Context, r io.Reader, key string, password []byte) (io.ReadCloser, error) {
	if strings.HasSuffix(key, ".gpg") {
		plain, err := crypt.Decrypt(ctx, r, password)
		if err != nil {
			return nil, err
		}
		r = plain
	}
	return gzip.NewReader(fsutil.ContextReader{Ctx: ctx, Reader: r})
}

func (c *remoteCatalog) metadataSuffix() string {
	if c.encrypted {
		return ".json.gz.gpg"
	}
	return ".json.gz"
}

func (c *remoteCatalog) encodeMetadata(ctx context.Context, plain, file string) error {
	f, err := os.Open(plain)
	if err != nil {
		return err
	}
	defer f.Close()
	return c.writeMetadata(ctx, f, file)
}

func (c *remoteCatalog) writeMetadataBytes(ctx context.Context, b []byte, file string) error {
	return c.writeMetadata(ctx, strings.NewReader(string(b)), file)
}

func (c *remoteCatalog) writeMetadata(ctx context.Context, r io.Reader, file string) (err error) {
	tmp, err := fsutil.Temp(c.cache)
	if err != nil {
		return err
	}
	defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
	var dst io.Writer = tmp
	var encrypted io.WriteCloser
	if c.encrypted {
		encrypted, err = crypt.Encrypt(ctx, tmp, c.password)
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
	return fsutil.Publish(tmp.Name(), file)
}

func (c *remoteCatalog) metadataCachePath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.cache, "metadata-"+hex.EncodeToString(sum[:]))
}

// cachedMetadata returns the cached copy of key, downloading it unless the
// cache already matches expected.
func (c *remoteCatalog) cachedMetadata(ctx context.Context, key, expected string) (name string, downloaded bool, err error) {
	name = c.metadataCachePath(key)
	if expected != "" {
		if hash, _, err := archive.Hash(logger.WithProgressLabel(logger.WithProgress(ctx, nil), "Checking cached catalog"), name); err == nil && hash == expected {
			return name, false, nil
		}
	}
	r, _, err := c.store.Get(ctx, key, "")
	if err != nil {
		return "", false, err
	}
	defer r.Close()
	f, err := fsutil.Temp(c.cache)
	if err != nil {
		return "", false, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = copyContext(ctx, f, r); err != nil {
		return "", false, err
	}
	if err = f.Sync(); err != nil {
		return "", false, err
	}
	if err = f.Close(); err != nil {
		return "", false, err
	}
	if expected != "" {
		hash, _, err := archive.Hash(logger.WithProgressLabel(ctx, "Checking downloaded catalog"), f.Name())
		if err != nil || hash != expected {
			return "", false, fmt.Errorf("backup: downloaded metadata checksum differs")
		}
	}
	if err = os.Rename(f.Name(), name); err != nil {
		return "", false, err
	}
	return name, true, nil
}

func (c *remoteCatalog) downloadedState() (downloadedState, error) {
	var v downloadedState
	b, err := os.ReadFile(filepath.Join(c.cache, "downloaded-state.json"))
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("backup: read downloaded state: %w", err)
	}
	if err = v.Version.Validate(); err != nil {
		return v, err
	}
	if len(v.Revisions) == 0 {
		return v, fmt.Errorf("backup: downloaded catalog has no revisions")
	}
	if v.Authority != "" && v.Authority != c.remote {
		return v, fmt.Errorf("backup: downloaded catalog belongs to a previous authority; run catalog sync")
	}
	for _, rev := range v.Revisions {
		if err = rev.Version.Validate(); err != nil {
			return v, err
		}
		if rev.Set != c.set || !strings.HasPrefix(rev.Key, c.prefix+"batches/") {
			return v, fmt.Errorf("backup: downloaded catalog identity differs")
		}
	}
	last := v.Revisions[len(v.Revisions)-1]
	stem := c.prefix + "descriptors/" + fmt.Sprintf("%020d-%s.json.gz", last.Number, last.ID)
	if v.Descriptor != stem && v.Descriptor != stem+".gpg" {
		return v, fmt.Errorf("backup: downloaded descriptor identity differs")
	}
	return v, nil
}

func (c *remoteCatalog) revisions(ctx context.Context) ([]revision, error) {
	folder := "heads/"
	conditional := c.conditional()
	if conditional {
		folder = "commits/"
	}
	out := []revision{}
	err := cloud.List(ctx, c.store, c.prefix+folder, func(h cloud.Object) error {
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
		identity := c.remote + "\x00" + c.prefix + folder + name
		cache := filepath.Join(c.cache, fmt.Sprintf("revision-%x.json", sha256.Sum256([]byte(identity))))
		b, err := os.ReadFile(cache)
		if os.IsNotExist(err) {
			if i < start-1 {
				continue
			}
			r, _, getErr := c.store.Get(ctx, c.prefix+folder+name, "")
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
		if err = v.Version.Validate(); err != nil {
			return nil, fmt.Errorf("backup: read remote revision: %w", err)
		}
		if v.Redirect != nil {
			if err = validateLocator(*v.Redirect); err != nil {
				return nil, err
			}
			if v.Redirect.Set != c.set {
				return nil, fmt.Errorf("backup: authority redirect belongs to another set")
			}
		}
		if v.Set != c.set || !conditional && v.ID != hint.ID || v.Number != hint.Number || hint.Checkpoint && !v.Checkpoint || !strings.HasPrefix(v.Key, c.prefix+"batches/") {
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

// pendingRevision returns the saved pending publication, or starts the revision
// after the current remote head and saves it as pending.
func (c *remoteCatalog) pendingRevision(ctx context.Context, local repository.Cataloger, existing []revision, current string) (revision, error) {
	var v revision
	pending, err := local.GetMeta(ctx, "publication")
	if err != nil {
		return v, err
	}
	if pending != "" {
		return v, json.Unmarshal([]byte(pending), &v)
	}
	number := int64(1)
	if len(existing) > 0 {
		head := existing[len(existing)-1]
		if head.ID != current {
			return v, fmt.Errorf("%w: pull or reconcile before publication", ErrConflict)
		}
		number = head.Number + 1
	} else if current != "" {
		return v, fmt.Errorf("%w: remote history disappeared", ErrConflict)
	}
	force, err := local.GetMeta(ctx, "force_checkpoint")
	if err != nil {
		return v, err
	}
	v = revision{
		Version:    model.StateVersionPortable,
		Set:        c.set,
		ID:         uuid.NewString(),
		Parent:     current,
		Number:     number,
		Checkpoint: number == 1 || number%32 == 0 || force == "true",
	}
	redirect, err := local.GetMeta(ctx, authorityRelocationKey)
	if err != nil {
		return v, err
	}
	if redirect != "" {
		var move authorityRelocation
		if err = json.Unmarshal([]byte(redirect), &move); err != nil {
			return v, err
		}
		v.Redirect, v.AuthorityHistory = &move.Locator, move.History
	}
	v.Key = c.prefix + "batches/" + v.ID + ".db.gz"
	if c.encrypted {
		v.Key += ".gpg"
	}
	b, _ := json.Marshal(v)
	return v, local.SetMeta(ctx, "publication", string(b))
}

// writeBatch exports the catalog changes of v to file, once, and checks that the
// file still matches the recorded hash.
func (c *remoteCatalog) writeBatch(ctx context.Context, local repository.Cataloger, v *revision, file string) error {
	if v.Hash == "" {
		if err := local.SetMeta(ctx, model.StateAuthorityKey, c.remote); err != nil {
			return err
		}
		if err := local.SetMeta(ctx, stateProtocolMeta, v.Version.String()); err != nil {
			return err
		}
		plain := file + ".db"
		defer os.Remove(plain)
		if _, err := os.Stat(plain); err == nil {
			os.Remove(plain)
		}
		if err := local.Export(ctx, plain, v.Checkpoint); err != nil {
			return err
		}
		if err := c.encodeMetadata(ctx, plain, file); err != nil {
			return err
		}
		hash, _, err := archive.Hash(logger.WithProgressLabel(ctx, "Checking catalog metadata"), file)
		if err != nil {
			return err
		}
		v.Hash = hash
		b, _ := json.Marshal(v)
		if err = local.SetMeta(ctx, "publication", string(b)); err != nil {
			return err
		}
	}
	hash, _, err := archive.Hash(logger.WithProgressLabel(ctx, "Checking catalog metadata"), file)
	if err != nil || hash != v.Hash {
		return fmt.Errorf("backup: publication file changed or disappeared")
	}
	return nil
}

// commitRevision uploads the revision head and, where writes are conditional,
// the commit object that makes it the published revision.
func (c *remoteCatalog) commitRevision(ctx context.Context, v revision, file string) error {
	manifestFile := file + ".head"
	defer os.Remove(manifestFile)
	b, _ := json.Marshal(v)
	if err := os.WriteFile(manifestFile, b, 0600); err != nil {
		return err
	}
	if _, err := cloud.Create(ctx, c.store, c.prefix+"heads/"+fmt.Sprintf("%020d-%s.json", v.Number, v.ID), manifestFile, "STANDARD"); err != nil {
		return err
	}
	if !c.conditional() {
		return nil
	}
	// this conditional commit is the publication point; earlier objects are proposals
	key := c.prefix + "commits/" + fmt.Sprintf("%020d.json", v.Number)
	if _, err := cloud.Create(ctx, c.store, key, manifestFile, "STANDARD"); err != nil {
		if errors.Is(err, cloud.ErrObjectExists) || cloud.Conflict(err) {
			return fmt.Errorf("%w: another writer committed this revision", ErrConflict)
		}
		return fmt.Errorf("%w: catalog commit failed: %w", ErrPending, err)
	}
	return nil
}

// decryptRevision returns a temporary file holding rev's plain catalog batch,
// checked against the hash its head recorded.
func (c *remoteCatalog) decryptRevision(ctx context.Context, rev revision) (string, error) {
	cached, _, err := c.cachedMetadata(ctx, rev.Key, rev.Hash)
	if err != nil {
		return "", err
	}
	r, err := os.Open(cached)
	if err != nil {
		return "", err
	}
	info, err := r.Stat()
	if err != nil {
		r.Close()
		return "", err
	}
	downloaded, err := fsutil.Temp(c.cache)
	if err != nil {
		r.Close()
		return "", err
	}
	defer os.Remove(downloaded.Name())
	progress := logger.StartProgress(ctx, "Loading catalog", info.Size())
	_, err = copyContext(ctx, downloaded, progress.Reader(r))
	progress.Finish()
	r.Close()
	downloaded.Close()
	if err != nil {
		return "", err
	}
	hash, _, err := archive.Hash(logger.WithProgressLabel(ctx, "Checking downloaded catalog"), downloaded.Name())
	if err != nil || hash != rev.Hash {
		return "", fmt.Errorf("backup: metadata checksum mismatch")
	}
	encrypted, err := os.Open(downloaded.Name())
	if err != nil {
		return "", err
	}
	defer encrypted.Close()
	body, err := metadataReader(ctx, encrypted, rev.Key, c.password)
	if err != nil {
		return "", err
	}
	defer body.Close()
	plain, err := fsutil.Temp(c.cache)
	if err != nil {
		return "", err
	}
	_, err = copyContext(ctx, plain, body)
	plain.Close()
	if err != nil {
		os.Remove(plain.Name())
		return "", err
	}
	return plain.Name(), nil
}

// descriptorFile returns the cached descriptor for rev and its key, and
// whether it had to be downloaded.
func (c *remoteCatalog) descriptorFile(ctx context.Context, rev revision) (file, key string, downloaded bool, err error) {
	if cached, err := c.downloadedState(); err == nil && cached.Authority == c.remote && cached.Revisions[len(cached.Revisions)-1].ID == rev.ID && rev.DescriptorHash != "" {
		file, downloaded, err := c.cachedMetadata(ctx, cached.Descriptor, rev.DescriptorHash)
		return file, cached.Descriptor, downloaded, err
	}

	key = c.prefix + "descriptors/" + fmt.Sprintf("%020d-%s.json.gz", rev.Number, rev.ID)
	if _, err := c.store.Head(ctx, key+".gpg", ""); err == nil {
		key += ".gpg"
	} else if !cloud.NotFound(err) && !errors.Is(err, os.ErrNotExist) {
		return "", "", false, err
	}
	file, downloaded, err = c.cachedMetadata(ctx, key, rev.DescriptorHash)
	return file, key, downloaded, err
}
