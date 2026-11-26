package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"glesha/internal/archive"
	"glesha/internal/backup"
	"glesha/internal/cloud"
	"glesha/internal/database/model"
	"glesha/internal/fsutil"
	"glesha/internal/logger"
)

func (r *runtime) retrieval() (backup.RetrievalOptions, error) {
	tier := r.env.Retrieval
	if !r.env.given("retrieval") {
		tier = strings.ToLower(r.config.Cold.Tier)
	}
	if tier == "" {
		tier = "bulk"
	}
	if tier != "bulk" && tier != "standard" && tier != "expedited" {
		return backup.RetrievalOptions{}, fmt.Errorf("cli: retrieval must be bulk, standard or expedited")
	}
	days := r.env.Days
	if days == 0 && !r.env.given("days") {
		days = int(r.config.Cold.Days)
	}
	if days < 1 || int64(days) > 2147483647 {
		return backup.RetrievalOptions{}, fmt.Errorf("cli: availability days must be positive and fit int32")
	}
	return backup.RetrievalOptions{Tier: tier, Days: int32(days), UseSavedDays: !r.env.given("days"), Request: r.env.RequestRetrieval, Confirm: func(ctx context.Context, message string) error { return confirm(ctx, message, false) }}, nil
}
func importArchive(r *runtime, args []string) error {
	if err := count(args, 1); err != nil {
		return err
	}
	if err := checkImportFlags(r.env); err != nil {
		return err
	}
	ids, err := r.remoteIDs(r.env.From)
	if err != nil {
		return err
	}
	if err = r.openImportSet(args[0]); err != nil {
		return err
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = r.stores(ids); err != nil {
		return err
	}
	if r.service.Pending() {
		return r.resumeImport()
	}
	defer func() { wipe(r.service.CatalogPassword) }()
	if err = r.prepareMutation(); err != nil {
		return err
	}
	retrieval, err := r.retrieval()
	if err != nil {
		return err
	}
	file := r.env.File
	remote := ""
	var object cloud.Object
	if file == "" {
		file, remote, object, err = r.service.PrepareImport(r.ctx, ids, r.env.Key, retrieval)
		if err != nil {
			return err
		}
	}
	if file, err = absolutePath(file); err != nil {
		return err
	}
	roots, err := rootMappings(r.env.Root)
	if err != nil {
		return err
	}
	date, err := backup.ParseDate(r.env.Date)
	if err != nil {
		return err
	}
	if r.env.File != "" && len(ids) > 0 {
		if remote, object, err = r.matchRemoteCopy(ids, file); err != nil {
			return err
		}
	}
	pw, err := password(r.ctx, r.env.PassphraseFile, "Archive passphrase", false)
	if err != nil {
		return err
	}
	defer wipe(pw)
	location := model.Location{}
	if remote != "" {
		location = backup.Location("", remote, object)
		if r.env.File != "" && !r.env.DeepVerify {
			location.Verification = "metadata"
		}
	}
	v, err := r.service.Import(r.ctx, backup.ImportOptions{File: file, Remote: remote, Object: location, Password: pw, Roots: roots, Date: date, Baseline: r.env.Baseline == "new"})
	if err != nil {
		return err
	}
	if r.service.Set.CatalogTo != "" && r.service.Set.CatalogTo != "local" {
		if err = r.publishImport(); err != nil {
			return err
		}
	}
	if err = r.publishState(); err != nil {
		return err
	}
	return r.emit("snapshot", v, fmt.Sprintf("Imported %s into %q.", v.ID, r.service.Set.Name))
}

func checkImportFlags(e *AppCmdEnv) error {
	if e.DeepVerify && len(e.From) == 0 {
		return fmt.Errorf("cli: --deep-verify requires --from")
	}
	if e.File == "" && e.Key == "" {
		return fmt.Errorf("cli: import requires --file or --key")
	}
	if e.Key != "" && len(e.From) == 0 {
		return fmt.Errorf("cli: remote import requires --from")
	}
	if e.Baseline != "" && e.Baseline != "new" {
		return fmt.Errorf("cli: import --baseline accepts new; select an existing snapshot with configure --baseline ID")
	}
	return nil
}

// openImportSet opens the set named name, discovering it on the --from remotes
// or creating it when it is not known locally.
func (r *runtime) openImportSet(name string) error {
	_, err := r.registry.Set(r.ctx, name)
	if errors.Is(err, sql.ErrNoRows) && len(r.env.From) > 0 {
		if err = r.discoverState(nil); err != nil {
			return err
		}
	}
	if _, err = r.registry.Set(r.ctx, name); errors.Is(err, sql.ErrNoRows) {
		var set model.Set
		if set, err = backup.NewSet(name, nil, r.config); err != nil {
			return err
		}
		err = r.registry.Create(r.ctx, set)
	}
	if err != nil {
		return err
	}
	return r.openSet(name)
}

// resumeImport finishes publishing an import interrupted after its local commit.
func (r *runtime) resumeImport() error {
	if err := r.syncStores(); err != nil {
		return err
	}
	if err := r.catalogSecret(true); err != nil {
		return err
	}
	defer wipe(r.service.CatalogPassword)
	file := r.env.File
	if file != "" {
		var err error
		if file, err = absolutePath(file); err != nil {
			return err
		}
	}
	if err := r.service.ResumeImportPublication(r.ctx, r.env.Key, file); err != nil {
		return err
	}
	return r.publishState()
}

// matchRemoteCopy finds the first remote whose object matches the local archive
// file: by size, or by content with --deep-verify.
func (r *runtime) matchRemoteCopy(ids []string, file string) (string, cloud.Object, error) {
	key := r.env.Key
	if key == "" {
		key = filepath.Base(file)
	}
	hash, size, err := archive.Hash(r.ctx, file)
	if err != nil {
		return "", cloud.Object{}, err
	}
	var last error
	for _, id := range ids {
		h, err := r.service.Stores[id].Head(r.ctx, key, "")
		if err != nil {
			last = err
			continue
		}
		if h.Size != size {
			last = fmt.Errorf("cli: associated object size differs")
			continue
		}
		if r.env.DeepVerify {
			if err = r.sameRemoteContent(id, key, h.Version, hash); err != nil {
				last = err
				continue
			}
		}
		return id, h, nil
	}
	return "", cloud.Object{}, last
}

// sameRemoteContent downloads one object version and compares its hash with hash.
func (r *runtime) sameRemoteContent(id, key, version, hash string) error {
	temp, err := fsutil.Temp(r.service.Directory)
	if err != nil {
		return err
	}
	name := temp.Name()
	temp.Close()
	os.Remove(name)
	defer os.Remove(name)
	if _, err = r.service.Download(r.ctx, []string{id}, key, version, name); err != nil {
		return err
	}
	remoteHash, _, err := archive.Hash(r.ctx, name)
	if err == nil && remoteHash != hash {
		err = fmt.Errorf("cli: associated object checksum differs")
	}
	return err
}

// publishImport publishes a local import to the set's catalog remote.
func (r *runtime) publishImport() error {
	pending := func(err error) error {
		return fmt.Errorf("%w: imported locally; publication pending: %w", backup.ErrPending, err)
	}
	if err := r.service.PrepareImportPublication(r.ctx); err != nil {
		return pending(err)
	}
	if err := r.syncStores(); err != nil {
		return pending(err)
	}
	if err := r.catalogSecret(true); err != nil {
		return pending(err)
	}
	// importArchive wipes the catalog passphrase after publishState, which still needs it
	return r.service.PublishImport(r.ctx)
}

// absolutePath expands '~' in p and makes it absolute.
func absolutePath(p string) (string, error) {
	p, err := fsutil.Expand(p)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

func restore(r *runtime, args []string) error {
	if r.env.Cancel {
		return cancelRestore(r, args)
	}
	if r.env.Into == "" {
		return fmt.Errorf("cli: restore requires --into DIR, or --cancel to clear unfinished tasks")
	}
	unlock, err := requireSet(r, args)
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { wipe(r.service.CatalogPassword) }()
	if err = r.readableCatalog(); err != nil {
		return err
	}
	o, err := restoreTarget(r)
	if err != nil {
		return err
	}
	retrieval, err := r.retrieval()
	if err != nil {
		return err
	}
	var job string
	o.ID, job, err = r.service.PrepareRestore(r.ctx, o.ID, o.Output, o.From, retrieval)
	if err != nil {
		return err
	}
	var stdin []byte
	defer func() { wipe(stdin) }()
	o.Password = func(v model.Snapshot) ([]byte, error) {
		file := r.env.PassphraseFile
		if specific, ok := r.env.ArchivePassphraseFiles[v.ID]; ok {
			file = specific
		}
		if file == "-" {
			if stdin == nil {
				var err error
				stdin, err = password(r.ctx, file, "Archive passphrase", false)
				if err != nil {
					return nil, err
				}
			}
			return append([]byte(nil), stdin...), nil
		}
		return password(r.ctx, file, "Archive "+v.ID+" passphrase", false)
	}
	if err = r.service.Restore(r.ctx, o); err != nil {
		return err
	}
	os.Remove(job)
	return r.emit("restore", struct {
		Snapshot string `json:"snapshot"`
		Path     string `json:"path"`
	}{o.ID, o.Output}, fmt.Sprintf("Restored %q to %q.", r.service.Set.Name, o.Output))
}

func cancelRestore(r *runtime, args []string) error {
	if r.env.given("snapshot") || r.env.RequestRetrieval {
		return fmt.Errorf("cli: --cancel selects restore tasks by --into, not snapshot or retrieval flags")
	}
	unlock, err := requireSet(r, args)
	if err != nil {
		return err
	}
	defer unlock()
	output, err := fsutil.Expand(r.env.Into)
	if err != nil {
		return err
	}
	result, err := r.service.CancelRestore(r.ctx, output)
	if err != nil {
		return err
	}
	text := "No unfinished restore tasks found."
	if result.Tasks > 0 {
		noun := "restore tasks"
		if result.Tasks == 1 {
			noun = "restore task"
		}
		text = fmt.Sprintf("Cancelled %d %s. Restored and downloaded files were kept.", result.Tasks, noun)
	}
	return r.emit("restore_cancelled", result, text)
}

// restoreTarget resolves the snapshot, destination and remotes of a restore and
// opens the remotes when the archive must be downloaded.
func restoreTarget(r *runtime) (backup.RestoreOptions, error) {
	var o backup.RestoreOptions
	var err error
	if o.Output, err = absolutePath(r.env.Into); err != nil {
		return o, err
	}
	if o.ID, err = r.service.RestoreSelection(r.ctx, r.env.Snapshot, o.Output); err != nil {
		return o, err
	}
	if err = r.service.CheckRestoreDestination(r.ctx, o.ID, o.Output); err != nil {
		return o, err
	}
	if o.From, err = r.remoteIDs(r.env.From); err != nil {
		return o, err
	}
	if len(o.From) == 0 {
		o.From = r.service.Set.To
	}
	download, err := r.service.RestoreNeedsDownload(r.ctx, o.ID, o.Output)
	if err != nil || !download {
		return o, err
	}
	return o, r.stores(o.From)
}
func rawService(r *runtime) {
	r.service = &backup.Service{Config: r.config, Budget: r.budget, Registry: r.registry, Directory: r.root, Stores: map[string]cloud.Store{}}
}
func upload(r *runtime, args []string) error {
	if err := count(args, 1); err != nil {
		return err
	}
	if len(r.env.To) == 0 {
		return fmt.Errorf("cli: upload requires --to")
	}
	rawService(r)
	to, err := r.remoteIDs(r.env.To)
	if err != nil {
		return err
	}
	if err = r.stores(to); err != nil {
		return err
	}
	key := r.env.Key
	if key == "" {
		key = filepath.Base(args[0])
	}
	class := r.env.StorageClass
	if class == "" {
		class = "STANDARD"
	}
	if err = backup.ValidateClass(class); err != nil {
		return err
	}
	if class != "STANDARD" {
		supported := false
		for _, id := range to {
			supported = supported || r.service.Capabilities(id).StorageClasses
		}
		if !supported {
			return fmt.Errorf("cli: storage class requires a destination with storage classes")
		}
	}
	file, err := fsutil.Expand(args[0])
	if err != nil {
		return err
	}
	if err = r.service.Upload(cloud.WithTransferLabel(r.ctx, "file"), to, key, file, class, r.env.Force); err != nil {
		return err
	}
	return r.completed(file, key, fmt.Sprintf("Uploaded %q to %s as %q.", file, strings.Join(r.env.To, ", "), key))
}
func download(r *runtime, args []string) error {
	if err := count(args, 1); err != nil {
		return err
	}
	if len(r.env.From) == 0 {
		return fmt.Errorf("cli: download requires --from")
	}
	rawService(r)
	from, err := r.remoteIDs(r.env.From)
	if err != nil {
		return err
	}
	if err = r.stores(from); err != nil {
		return err
	}
	output := r.env.Output
	if output == "" {
		output = filepath.Base(args[0])
	}
	output, err = fsutil.Expand(output)
	if err != nil {
		return err
	}
	object, err := r.service.Download(r.ctx, from, args[0], "", output)
	if err != nil {
		return err
	}
	return r.emit("download", struct {
		Path   string       `json:"path"`
		Object cloud.Object `json:"object"`
	}{output, object}, fmt.Sprintf("Downloaded %q to %q.", args[0], output))
}
func check(r *runtime, args []string) error {
	if err := count(args, 0); err != nil {
		return err
	}
	if len(r.env.To) == 0 {
		return fmt.Errorf("cli: check requires --to")
	}
	rawService(r)
	to, err := r.remoteIDs(r.env.To)
	if err != nil {
		return err
	}
	passed, failed := 0, 0
	for _, id := range to {
		remote, err := r.registry.Remote(r.ctx, id)
		if err != nil {
			return err
		}
		result, err := checkProvider(r, remote)
		if err != nil {
			return err
		}
		level := logger.INFO
		if result.passed() {
			passed++
		} else {
			failed++
			level = logger.ERROR
		}
		if err := r.emitAtLevel("provider_check", struct {
			Provider   string            `json:"provider"`
			Passed     bool              `json:"passed"`
			SetupError string            `json:"setup_error,omitempty"`
			Report     cloud.CheckReport `json:"report"`
		}{remote.Name, result.passed(), errorText(result.setup), result.report}, result.text(), level); err != nil {
			return err
		}
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
	}
	level := logger.INFO
	if failed > 0 {
		level = logger.ERROR
	}
	if err := r.emitAtLevel("check_summary", struct {
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	}{passed, failed}, fmt.Sprintf("Summary: %d passed, %d failed.", passed, failed), level); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("cloud: %d provider checks failed; see summary", failed)
	}
	return nil
}

// providerCheck is the outcome of checking one remote. setup is the error from
// opening its store; failure is the first error of the whole check.
type providerCheck struct {
	remote  model.Remote
	report  cloud.CheckReport
	setup   error
	failure error
}

func (c providerCheck) passed() bool { return c.failure == nil }

// checkProvider returns an error only when the probe file cannot be created.
func checkProvider(r *runtime, remote model.Remote) (providerCheck, error) {
	c := providerCheck{remote: remote, report: cloud.NewCheckReport()}
	if c.setup = r.stores([]string{remote.ID}); c.setup != nil {
		c.failure = c.setup
		return c, nil
	}
	temp, err := fsutil.Temp(r.root)
	if err != nil {
		return c, err
	}
	defer os.Remove(temp.Name())
	_, err = temp.WriteString("glesha provider check\n")
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		c.failure = err
		return c, nil
	}
	c.report, c.failure = cloud.CheckAccess(r.ctx, r.service.Stores[remote.ID], "glesha/check/"+filepath.Base(temp.Name()), temp.Name())
	return c, nil
}

func (c providerCheck) text() string {
	var text strings.Builder
	fmt.Fprintf(&text, "Provider %q (%s)\n", c.remote.Name, c.remote.Kind)
	if c.setup != nil {
		fmt.Fprintf(&text, "  Connection  FAILED: %s\n", c.setup)
	}
	errorShown := c.setup != nil
	for _, step := range c.report.Steps {
		fmt.Fprintf(&text, "  %-10s %-7s", step.Name, step.Status)
		if step.Status == cloud.CHECK_FAILED {
			errorShown = true
			if step.Detail != "" {
				fmt.Fprintf(&text, "  %s", step.Detail)
			}
		}
		text.WriteByte('\n')
	}
	switch {
	case c.passed():
		text.WriteString("  Result      PASS\n")
	case !errorShown:
		fmt.Fprintf(&text, "  Result      FAIL: %s\n", c.failure)
	default:
		text.WriteString("  Result      FAIL\n")
	}
	if c.report.Key != "" && c.report.Steps[4].Status != cloud.CHECK_PASSED {
		fmt.Fprintf(&text, "  Remaining object: %s (version %s)\n", c.report.Key, c.report.Version)
	}
	return text.String()
}

func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func storageClass(r *runtime, args []string) error {
	if r.env.StorageClass == "" {
		return fmt.Errorf("cli: --class is required")
	}
	unlock, err := requireSet(r, args)
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { wipe(r.service.CatalogPassword) }()
	if err = r.prepareMutation(); err != nil {
		return err
	}
	if err = backup.ValidateClass(r.env.StorageClass); err != nil {
		return err
	}
	ids, err := r.service.ClassRemotes(r.ctx, r.env.Snapshot)
	if err != nil {
		return err
	}
	if err = r.stores(ids); err != nil {
		return err
	}
	if err = r.syncStores(); err != nil {
		return err
	}
	if err = r.catalogSecret(true); err != nil {
		return err
	}
	defer wipe(r.service.CatalogPassword)
	if err = r.service.StorageClass(r.ctx, r.env.Snapshot, r.env.StorageClass); err != nil {
		return err
	}
	if err := r.publishState(); err != nil {
		return err
	}
	return r.result(fmt.Sprintf("Storage class updated to %s for %q.", r.env.StorageClass, r.service.Set.Name))
}
func move(r *runtime, args []string) error {
	if len(r.env.To) != 1 {
		return fmt.Errorf("cli: move requires one --to remote")
	}
	unlock, err := requireSet(r, args)
	if err != nil {
		return err
	}
	defer unlock()
	defer func() { wipe(r.service.CatalogPassword) }()
	if err = r.prepareMutation(); err != nil {
		return err
	}
	to, err := r.remoteIDs(r.env.To)
	if err != nil {
		return err
	}
	ids := append(append([]string{}, r.service.Set.To...), to...)
	if err = r.stores(ids); err != nil {
		return err
	}
	if err = r.syncStores(); err != nil {
		return err
	}
	if err = r.catalogSecretFor(to[0], true); err != nil {
		return err
	}
	defer wipe(r.service.CatalogPassword)
	retrieval, err := r.retrieval()
	if err != nil {
		return err
	}
	if err = r.service.Move(r.ctx, to[0], retrieval); err != nil {
		return err
	}
	if err := r.publishState(); err != nil {
		return err
	}
	return r.result(fmt.Sprintf("Moved %q to %s. Copies on the old remotes were kept.", r.service.Set.Name, r.env.To[0]))
}
