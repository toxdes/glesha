package app_cmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"glesha/archive"
	"glesha/backup"
	"glesha/cloud"
	"glesha/database/model"
	"glesha/file_io"
	L "glesha/logger"
)

func (r *runtime) retrieval() (backup.RetrievalOptions, error) {
	tier := r.env.Retrieval
	if !r.env.present["retrieval"] {
		tier = strings.ToLower(r.config.Cold.Tier)
	}
	if tier == "" {
		tier = "bulk"
	}
	if tier != "bulk" && tier != "standard" && tier != "expedited" {
		return backup.RetrievalOptions{}, fmt.Errorf("cli: retrieval must be bulk, standard or expedited")
	}
	days := r.env.Days
	if days == 0 && !r.env.present["days"] {
		days = int(r.config.Cold.Days)
	}
	if days < 1 || int64(days) > 2147483647 {
		return backup.RetrievalOptions{}, fmt.Errorf("cli: availability days must be positive and fit int32")
	}
	return backup.RetrievalOptions{Tier: tier, Days: int32(days), UseSavedDays: !r.env.present["days"], Request: r.env.RequestRetrieval, Confirm: func(ctx context.Context, message string) error { return confirm(ctx, message, false) }}, nil
}
func importArchive(r *runtime, args []string) error {
	if err := count(args, 1); err != nil {
		return err
	}
	if r.env.Deep && len(r.env.From) == 0 {
		return fmt.Errorf("cli: --deep-verify requires --from")
	}
	if r.env.File == "" && r.env.Key == "" {
		return fmt.Errorf("cli: import requires --file or --key")
	}
	if r.env.Key != "" && len(r.env.From) == 0 {
		return fmt.Errorf("cli: remote import requires --from")
	}
	if r.env.Baseline != "" && r.env.Baseline != "new" {
		return fmt.Errorf("cli: import --baseline accepts new; select an existing snapshot with configure --baseline ID")
	}
	ids, err := r.remoteIDs(r.env.From)
	if err != nil {
		return err
	}
	if _, err = r.registry.Set(r.ctx, args[0]); errors.Is(err, sql.ErrNoRows) && len(r.env.From) > 0 {
		if err = r.discoverState(nil); err != nil {
			return err
		}
	}
	if _, err = r.registry.Set(r.ctx, args[0]); errors.Is(err, sql.ErrNoRows) {
		var set model.Set
		set, err = backup.NewSet(args[0], nil, r.config)
		if err != nil {
			return err
		}
		if err = r.registry.Create(r.ctx, set); err != nil {
			return err
		}
	}
	if err != nil {
		return err
	}
	if err = r.openSet(args[0]); err != nil {
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
		if err = r.syncStores(); err != nil {
			return err
		}
		if err = r.catalogSecret(true); err != nil {
			return err
		}
		defer wipe(r.service.CatalogPassword)
		file := r.env.File
		if file != "" {
			file, err = file_io.Expand(file)
			if err != nil {
				return err
			}
			file, err = filepath.Abs(file)
			if err != nil {
				return err
			}
		}
		if err = r.service.ResumeImportPublication(r.ctx, r.env.Key, file); err != nil {
			return err
		}
		return r.publishState()
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
	file, err = file_io.Expand(file)
	if err != nil {
		return err
	}
	file, err = filepath.Abs(file)
	if err != nil {
		return err
	}
	roots, err := rootMappings(r.env.Roots)
	if err != nil {
		return err
	}
	date, err := backup.ParseDate(r.env.Date)
	if err != nil {
		return err
	}
	if r.env.File != "" && len(ids) > 0 {
		key := r.env.Key
		if key == "" {
			key = filepath.Base(file)
		}
		hash, size, err := archive.Hash(r.ctx, file)
		if err != nil {
			return err
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
			if r.env.Deep {
				temp, err := file_io.Temp(r.service.Directory)
				if err != nil {
					return err
				}
				name := temp.Name()
				temp.Close()
				os.Remove(name)
				_, err = backup.Download(r.ctx, r.service.Stores, []string{id}, key, h.Version, name)
				if err == nil {
					remoteHash, _, hashErr := archive.Hash(r.ctx, name)
					err = hashErr
					if err == nil && remoteHash != hash {
						err = fmt.Errorf("cli: associated object checksum differs")
					}
				}
				os.Remove(name)
				if err != nil {
					last = err
					continue
				}
			}
			remote, object = id, h
			break
		}
		if remote == "" {
			return last
		}
	}
	pw, err := password(r.ctx, r.env.PasswordFile, "Archive passphrase", false)
	if err != nil {
		return err
	}
	defer wipe(pw)
	location := model.Location{}
	if remote != "" {
		location = backup.Location("", remote, object)
		if r.env.File != "" && !r.env.Deep {
			location.Verification = "metadata"
		}
	}
	v, err := r.service.Import(r.ctx, backup.ImportOptions{File: file, Remote: remote, Object: location, Password: pw, Roots: roots, Date: date, Baseline: r.env.Baseline == "new"})
	if err != nil {
		return err
	}
	if r.service.Set.CatalogTo != "" && r.service.Set.CatalogTo != "local" {
		if err = r.service.PrepareImportPublication(r.ctx); err != nil {
			return fmt.Errorf("%w: imported locally; publication pending: %w", backup.ErrPending, err)
		}
		if err = r.syncStores(); err != nil {
			return fmt.Errorf("%w: imported locally; publication pending: %w", backup.ErrPending, err)
		}
		if err = r.catalogSecret(true); err != nil {
			return fmt.Errorf("%w: imported locally; publication pending: %w", backup.ErrPending, err)
		}
		defer wipe(r.service.CatalogPassword)
		if err = r.service.PublishImport(r.ctx); err != nil {
			return err
		}
	}
	if err = r.publishState(); err != nil {
		return err
	}
	return r.emit("snapshot", v, fmt.Sprintf("Imported %s into %q.", v.ID, r.service.Set.Name))
}
func restore(r *runtime, args []string) error {
	if r.env.CancelRestore {
		if r.env.present["snapshot"] || r.env.RequestRetrieval {
			return fmt.Errorf("cli: --cancel selects restore tasks by --into, not snapshot or retrieval flags")
		}
		unlock, err := requireSet(r, args)
		if err != nil {
			return err
		}
		defer unlock()
		output, err := file_io.Expand(r.env.Into)
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
			text = fmt.Sprintf("Cancelled %d %s. Restored files and staging data were preserved.", result.Tasks, noun)
		}
		return r.emit("restore_cancelled", result, text)
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
	into, err := file_io.Expand(r.env.Into)
	if err != nil {
		return err
	}
	into, err = filepath.Abs(into)
	if err != nil {
		return err
	}
	snapshot, err := r.service.RestoreSelection(r.ctx, r.env.Snapshot, into)
	if err != nil {
		return err
	}
	if err = r.service.CheckRestoreDestination(r.ctx, snapshot, into); err != nil {
		return err
	}
	from, err := r.remoteIDs(r.env.From)
	if err != nil {
		return err
	}
	if len(from) == 0 {
		from = r.service.Set.To
	}
	needsRemote, err := r.service.RestoreNeedsDownload(r.ctx, snapshot, into)
	if err != nil {
		return err
	}
	if needsRemote {
		if err = r.stores(from); err != nil {
			return err
		}
	}

	retrieval, err := r.retrieval()
	if err != nil {
		return err
	}
	id, job, err := r.service.PrepareRestore(r.ctx, snapshot, into, from, retrieval)
	if err != nil {
		return err
	}
	var stdin []byte
	defer func() { wipe(stdin) }()
	err = r.service.Restore(r.ctx, backup.RestoreOptions{ID: id, Output: into, From: from, Password: func(v model.Snapshot) ([]byte, error) {
		file := r.env.PasswordFile
		if specific, ok := r.env.Passwords[v.ID]; ok {
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
	}})
	if err == nil {
		os.Remove(job)
		return r.emit("restore", struct {
			Snapshot string `json:"snapshot"`
			Path     string `json:"path"`
		}{id, into}, fmt.Sprintf("Restored %q to %q.", r.service.Set.Name, into))
	}
	return err
}
func rawService(r *runtime) {
	r.service = &backup.Service{Config: r.config, Budget: r.budget, Registry: r.registry, Directory: r.root, Stores: map[string]cloud.Store{}, Kinds: map[string]string{}}
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
	class := r.env.Class
	if class == "" {
		class = "STANDARD"
	}
	if err = backup.ValidateClass(class); err != nil {
		return err
	}
	if class != "STANDARD" {
		supported := false
		for _, id := range to {
			supported = supported || r.service.Kinds[id] == "aws"
		}
		if !supported {
			return fmt.Errorf("cli: storage class requires AWS")
		}
	}
	file, err := file_io.Expand(args[0])
	if err != nil {
		return err
	}
	if err = backup.Upload(cloud.WithTransferLabel(r.ctx, "file"), r.service.Stores, to, key, file, class, r.env.Force); err != nil {
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
	output, err = file_io.Expand(output)
	if err != nil {
		return err
	}
	object, err := backup.Download(r.ctx, r.service.Stores, from, args[0], "", output)
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
		report := cloud.NewCheckReport()
		setupErr := r.stores([]string{id})
		var checkErr error
		if setupErr == nil {
			temp, err := file_io.Temp(r.root)
			if err != nil {
				return err
			}
			_, err = temp.WriteString("glesha provider check\n")
			closeErr := temp.Close()
			if err == nil {
				err = closeErr
			}
			if err == nil {
				report, checkErr = cloud.CheckAccess(r.ctx, r.service.Stores[id], "glesha/check/"+filepath.Base(temp.Name()), temp.Name())
			} else {
				checkErr = err
			}
			os.Remove(temp.Name())
		} else {
			checkErr = setupErr
		}
		ok := checkErr == nil
		if ok {
			passed++
		} else {
			failed++
		}
		var text strings.Builder
		fmt.Fprintf(&text, "Provider %q (%s)\n", remote.Name, remote.Kind)
		if setupErr != nil {
			fmt.Fprintf(&text, "  Connection  FAILED: %s\n", setupErr)
		}
		errorShown := setupErr != nil
		for _, step := range report.Steps {
			fmt.Fprintf(&text, "  %-10s %-7s", step.Name, step.Status)
			if step.Status == cloud.CHECK_FAILED {
				errorShown = true
			}
			if step.Detail != "" && step.Status == cloud.CHECK_FAILED {
				fmt.Fprintf(&text, "  %s", step.Detail)
			}
			text.WriteByte('\n')
		}
		if ok {
			text.WriteString("  Result      PASS\n")
		} else if !errorShown {
			fmt.Fprintf(&text, "  Result      FAIL: %s\n", checkErr)
		} else {
			text.WriteString("  Result      FAIL\n")
		}
		if report.Key != "" && report.Steps[4].Status != cloud.CHECK_PASSED {
			fmt.Fprintf(&text, "  Remaining object: %s (version %s)\n", report.Key, report.Version)
		}
		level := L.INFO
		if !ok {
			level = L.ERROR
		}
		if err := r.emitAtLevel("provider_check", struct {
			Provider   string            `json:"provider"`
			Passed     bool              `json:"passed"`
			SetupError string            `json:"setup_error,omitempty"`
			Report     cloud.CheckReport `json:"report"`
		}{remote.Name, ok, errorText(setupErr), report}, text.String(), level); err != nil {
			return err
		}
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
	}
	level := L.INFO
	if failed > 0 {
		level = L.ERROR
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
func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func storageClass(r *runtime, args []string) error {
	if r.env.Class == "" {
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
	if err = backup.ValidateClass(r.env.Class); err != nil {
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
	if err = r.service.StorageClass(r.ctx, r.env.Snapshot, r.env.Class); err != nil {
		return err
	}
	if err := r.publishState(); err != nil {
		return err
	}
	return r.result(fmt.Sprintf("Storage class updated to %s for %q.", r.env.Class, r.service.Set.Name))
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
	return r.result(fmt.Sprintf("Moved %q to %s. Source copies were retained.", r.service.Set.Name, r.env.To[0]))
}
