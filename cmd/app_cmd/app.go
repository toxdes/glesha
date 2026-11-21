package app_cmd

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
	"golang.org/x/term"

	"glesha/backup"
	"glesha/cloud"
	"glesha/config"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
	"glesha/memory"
	"glesha/state"
)

type destinations []string

func (d *destinations) String() string { return strings.Join(*d, ",") }
func (d *destinations) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("cli: empty remote name")
		}
		found := false
		for _, name := range *d {
			found = found || name == part
		}
		if !found {
			*d = append(*d, part)
		}
	}
	return nil
}

type mappings map[string]string

func (m *mappings) String() string { return "" }
func (m *mappings) Set(value string) error {
	name, p, ok := strings.Cut(value, "=")
	if !ok || name == "" || p == "" {
		return fmt.Errorf("cli: expected NAME=PATH")
	}
	if old, ok := (*m)[name]; ok && old != p {
		return fmt.Errorf("cli: conflicting mapping for %q", name)
	}
	(*m)[name] = p
	return nil
}

type AppCmdEnv struct {
	CancelRestore                                                                                                                                                                                                         bool
	StateDirectory                                                                                                                                                                                                        string
	Chunked                                                                                                                                                                                                               bool
	SpoolMax                                                                                                                                                                                                              string
	Progress, Format, Keep, Authority                                                                                                                                                                                     string
	All, DryRun                                                                                                                                                                                                           bool
	Config, Env, LogLevel, Color, Output, Into, File, Archive, Key, CatalogTo, Compression, Prefix, Mode, Class, IncrementalClass, Snapshot, Filter, Memory, PasswordFile, CatalogPasswordFile, Date, Baseline, Retrieval string
	To, From                                                                                                                                                                                                              destinations
	Roots, Passwords                                                                                                                                                                                                      mappings
	Level, HashWorkers, TransferWorkers, Days                                                                                                                                                                             int
	Incremental, Full, Recursive, Refresh, Remote, Force, AssumeYes, RequestRetrieval, Deep, Help, Version, KeepArchive                                                                                                   bool
	present                                                                                                                                                                                                               map[string]bool
}

type runtime struct {
	emitted                int
	command                string
	catalogCheckProgress   *L.Progress
	catalogCheckActive     bool
	catalogCacheUpdated    bool
	catalogReplicasUpdated int
	remoteScope            *remoteScope
	operationLocked        bool
	statusCopies           *sql.DB
	statusArchiveBytes     int64
	statusArchiveCost      float64
	ctx                    context.Context
	env                    *AppCmdEnv
	config                 config.Config
	budget                 memory.Budget
	registry               repository.Registryor
	root                   string
	service                *backup.Service
	out                    io.Writer
	errOut                 io.Writer
	limit                  chan struct{}
}
type handler func(*runtime, []string) error

func Usage() string {
	var out strings.Builder
	out.WriteString("usage: glesha [global options] COMMAND [ARGS] [options]\n\n")
	out.WriteString("Encrypted archives and incremental cloud backups.\n")
	for _, group := range overview {
		fmt.Fprintf(&out, "\n%s:\n", group.title)
		for _, entry := range group.entries {
			out.WriteString(helpEntry(entry.usage, entry.summary) + "\n")
		}
	}
	out.WriteString("\nGlobal options:\n")
	for _, o := range helpOptions(&command{}, nil) {
		out.WriteString(helpEntry(o.label, o.usage) + "\n")
	}
	out.WriteString("\nRun 'glesha help COMMAND' for the options of a command.\n")
	return out.String()
}

// flagSet registers the global options and the options of the named command.
func flagSet(name string, e *AppCmdEnv) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	for i := range options {
		if options[i].group == groupGlobal {
			options[i].register(f, e)
		}
	}
	if c, ok := lookupCommand(name); ok {
		for _, o := range c.options {
			optionIndex[o].register(f, e)
		}
	}
	return f
}
func reorder(f *flag.FlagSet, args []string) ([]string, error) {
	var options, positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			name := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
			v := f.Lookup(name)
			if v == nil {
				return nil, fmt.Errorf("cli: unsupported flag %s; see glesha help %s", a, f.Name())
			}
			options = append(options, a)
			b, ok := v.Value.(interface{ IsBoolFlag() bool })
			if !strings.Contains(a, "=") && !(ok && b.IsBoolFlag()) {
				i++
				if i == len(args) {
					return nil, fmt.Errorf("cli: %s requires a value", a)
				}
				options = append(options, args[i])
			}
		} else {
			positionals = append(positionals, a)
		}
	}
	return append(append(options, "--"), positionals...), nil
}
func resolveCommand(args []string) (string, []string, error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if _, ok := lookupCommand(a); ok {
			return a, append(append([]string{}, args[:i]...), args[i+1:]...), nil
		}
		if a == "--help" || a == "-h" || a == "--version" || a == "-version" || a == "-v" {
			return "", args, nil
		}
		if strings.HasPrefix(a, "-") {
			name := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
			if !globalOption(name) {
				return "", nil, fmt.Errorf("cli: put operation options after the command")
			}
			if !strings.Contains(a, "=") {
				i++
			}
			continue
		}
		return "", nil, fmt.Errorf("cli: unknown command %q; see glesha help", a)
	}
	return "", args, nil
}
func Execute(ctx context.Context, args []string, version, sha string) error {
	command, rest, err := resolveCommand(args)
	if err != nil {
		return err
	}
	// spec is nil only when no command was given, which shows the overview
	spec, _ := lookupCommand(command)
	e := &AppCmdEnv{present: map[string]bool{}}
	f := flagSet(command, e)
	rest, err = reorder(f, rest)
	if err != nil {
		return err
	}
	if err = f.Parse(rest); err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	f.Visit(func(v *flag.Flag) { e.present[v.Name] = true })
	if err = normalizeOutput(command, e); err != nil {
		return err
	}
	L.SetStructuredDiagnostics(e.jsonOutput())
	defer L.SetStructuredDiagnostics(false)
	early := &runtime{ctx: ctx, env: e, out: os.Stdout, command: command}
	if e.present["state-dir"] && e.StateDirectory == "" {
		return fmt.Errorf("cli: --state-dir requires a directory")
	}
	for _, p := range []*string{&e.Output, &e.Into, &e.File, &e.Archive, &e.PasswordFile, &e.CatalogPasswordFile} {
		if *p != "" && *p != "-" {
			expanded, err := file_io.Expand(*p)
			if err != nil {
				return err
			}
			*p = expanded
		}
	}
	if command == "version" && !e.Help {
		if err := count(f.Args(), 0); err != nil {
			return err
		}
		e.Version = true
	}
	if e.Version {
		return early.emit("version", struct {
			Version  string `json:"version"`
			Revision string `json:"revision"`
		}{version, sha}, fmt.Sprintf("glesha %s (%s)", L.ASCII(version), L.ASCII(sha)))
	}
	if command == "help" {
		return early.help(f.Args())
	}
	if e.Help || command == "" {
		if command == "" {
			return early.help(nil)
		}
		helpArgs := []string{command}
		if (command == "catalog" || command == "state") && len(f.Args()) > 0 {
			helpArgs = append(helpArgs, f.Args()[0])
		}
		return early.help(helpArgs)
	}
	if err = L.SetLevelFromString(e.LogLevel); err != nil {
		return err
	}
	if err = L.SetColorModeFromString(e.Color); err != nil {
		return err
	}
	if command == "catalog" {
		if len(f.Args()) == 0 {
			return fmt.Errorf("cli: choose a catalog action: sync, unlock, or resolve; see glesha help catalog")
		}
		if !knownHelpAction("catalog", f.Args()[0]) {
			return fmt.Errorf("cli: unknown catalog action %q; see glesha help catalog", f.Args()[0])
		}
	}

	c, err := config.Load(e.Config)
	if err != nil {
		return err
	}
	if e.StateDirectory != "" {
		c.Catalog.Directory = e.StateDirectory
	}
	if command == "state" {
		early.config = c
		return spec.run(early, f.Args())
	}
	if err = config.Env(e.Env); err != nil {
		return err
	}
	c.ProviderEnv()
	if e.Compression != "" {
		c.Archive.Compression = e.Compression
	}
	if e.Prefix != "" {
		c.Archive.Prefix = e.Prefix
	}
	if e.Level != 0 {
		c.Archive.Level = e.Level
	}
	if e.Mode != "" {
		c.Archive.Mode = e.Mode
	}
	if e.Memory != "" {
		c.Memory.Max = e.Memory
	}
	if e.present["compression-level"] && e.Level < 1 || e.present["hash-workers"] && e.HashWorkers < 1 || (e.present["transfer-workers"] || e.present["jobs"] || e.present["j"]) && e.TransferWorkers < 1 {
		return fmt.Errorf("cli: numeric overrides must be positive")
	}
	if e.HashWorkers > 0 {
		c.Workers.Hash = e.HashWorkers
	}
	if e.TransferWorkers > 0 {
		c.Workers.Transfer = e.TransferWorkers
	}
	if err = c.Validate(); err != nil {
		return err
	}
	budget, err := memory.Resolve(c.Memory.Max, c.Workers.Hash, c.Workers.Transfer, memory.Available)
	if err != nil {
		return err
	}
	budget.Apply()
	ctx = memory.WithBudget(ctx, budget)
	localOnly := command == "archive" || command == "decrypt" || command == "extract"
	root := c.Catalog.Directory
	if root == "" {
		root, err = config.DefaultStatePath()
		if err != nil {
			return err
		}
		legacy, err := config.LegacyStatePath()
		if err != nil {
			return err
		}
		if !localOnly {
			if err = state.CheckDefault(ctx, legacy, root); err != nil {
				return err
			}
		}
	}
	root, err = file_io.Expand(root)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	var registry repository.Registryor
	if !localOnly {
		stateLock, err := state.Lock(root, false)
		if err != nil {
			return err
		}
		defer stateLock.Unlock()
		if err = os.MkdirAll(root, 0700); err != nil {
			return err
		}
		registry, err = repository.NewRegistryRepository(ctx, filepath.Join(root, "registry.db"))
		if err != nil {
			return err
		}
		defer registry.Close()
	}
	ctx = L.WithWorkerDetails(ctx, e.present["j"] || e.present["jobs"] || e.present["transfer-workers"] || e.present["hash-workers"])
	r := &runtime{ctx: ctx, command: command, env: e, config: c, budget: budget, registry: registry, root: root, out: os.Stdout, limit: make(chan struct{}, budget.TransferWorkers)}
	var renderer *L.ProgressRenderer
	if e.Progress != "off" && L.ProgressEnabled() {
		if e.jsonOutput() {
			renderer = L.NewEventProgressRenderer(L.EmitProgress)
		} else if e.Progress == "plain" || !term.IsTerminal(int(os.Stderr.Fd())) || os.Getenv("TERM") == "dumb" {
			renderer = L.NewPlainProgressRenderer(os.Stderr)
		} else {
			renderer = L.NewProgressRenderer(os.Stderr, L.ColorEnabled(true))
		}
		defer renderer.Close()
		r.ctx = L.WithProgress(ctx, renderer)
	}
	L.Debug(fmt.Sprintf("command=%s; memory=%s; hash-workers=%d; transfer-workers=%d", command, L.Bytes(budget.Total), budget.HashWorkers, budget.TransferWorkers))
	if err := r.selectCommandRemotes(); err != nil {
		return err
	}
	err = spec.run(r, f.Args())
	if r.operationLocked && r.service != nil && r.service.Set.ID != "" && strings.Contains(" run retry import restore move storage-class ", " "+command+" ") && !e.All && !e.CancelRestore {
		lock := flock.New(filepath.Join(r.service.Directory, "writer.lock"))
		if free, lockErr := lock.TryLock(); lockErr == nil && free {
			if recordErr := r.service.RecordResult(context.WithoutCancel(r.ctx), command, err); recordErr != nil {
				L.Debug(recordErr.Error())
			}
			lock.Unlock()
		}
	}
	if renderer != nil {
		renderer.Close()
		if err == nil {
			err = renderer.Err()
		}
	}
	if err == nil && e.jsonOutput() && r.emitted == 0 && !strings.Contains(" list status history browse ", " "+command+" ") {
		return r.completed("", "", "")
	}
	return err
}
func (r *runtime) openSet(name string) error {
	r.service = &backup.Service{Config: r.config, Budget: r.budget, Registry: r.registry, Stores: map[string]cloud.Store{}}
	return setError(name, r.service.OpenSet(r.ctx, name, r.root))
}
func (r *runtime) lock() (func(), error) {
	lock := flock.New(filepath.Join(r.service.Directory, "writer.lock"))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("cli: set is busy")
	}
	r.operationLocked = true
	return func() { lock.Unlock() }, nil
}
func (r *runtime) remoteIDs(names []string) ([]string, error) {
	ids := []string{}
	profiles := r.config.RemotesByName()
	for _, name := range names {
		p, ok := profiles[name]
		if !ok || !cloud.Supports(p.Kind) {
			return nil, fmt.Errorf("cli: unknown or unsupported remote %q", name)
		}
		if p.Bucket == "" {
			return nil, fmt.Errorf("cli: remote %q bucket is not configured", name)
		}
		v, err := r.registry.BindRemote(r.ctx, model.Remote{Name: name, Kind: p.Kind, Bucket: p.Bucket, Endpoint: p.Endpoint, Region: p.Region})
		if err != nil {
			return nil, err
		}
		ids = append(ids, v.ID)
	}
	return ids, nil
}
func (r *runtime) stores(ids []string) error {
	if err := r.selectCommandRemotes(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := r.checkRemote(id); err != nil {
			return err
		}
	}
	for _, id := range ids {
		if r.service.Stores[id] != nil {
			continue
		}
		remote, err := r.registry.Remote(r.ctx, id)
		if err != nil {
			return err
		}
		var p config.Provider
		found := false
		for _, name := range r.configuredRemotes() {
			candidate := r.config.RemotesByName()[name]
			identity := model.Remote{Kind: candidate.Kind, Bucket: candidate.Bucket, Endpoint: candidate.Endpoint, Region: candidate.Region}
			if identity.Identity() == remote.ID {
				p, found = candidate, true
				break
			}
		}
		if !found {
			hint := "configure that location before syncing"
			provider, err := cloud.Lookup(remote.Kind)
			if err == nil {
				hint += provider.SetupHint
				err = provider.ValidateCredentials()
			}
			if err != nil {
				return fmt.Errorf("cli: catalog requires access to %s bucket %q; %w; %s", remote.Kind, remote.Bucket, err, hint)
			}
			return fmt.Errorf("cli: catalog requires access to %s bucket %q; %s", remote.Kind, remote.Bucket, hint)
		}
		store, err := cloud.New(r.ctx, p, r.budget.TransferWorkers, r.limit)
		if err != nil {
			return err
		}
		r.service.Stores[id] = store
	}
	return nil
}
func (r *runtime) catalogSecret(confirm bool) error {
	return r.catalogSecretFor(r.service.Set.CatalogTo, confirm)
}
func (r *runtime) catalogSecretFor(remote string, confirm bool) error {
	if len(r.service.CatalogPassword) > 0 {
		return nil
	}
	if remote == "local" {
		return nil
	}
	provided := r.env.CatalogPasswordFile != "" || os.Getenv("GLESHA_CATALOG_PASSPHRASE") != ""
	if !r.service.Config.Catalog.Encrypted && !provided {
		return nil
	}
	if confirm && r.service.Set.ID != "" && r.service.Set.Name != r.service.Set.ID {
		c, err := r.service.Catalog(r.ctx)
		if err != nil {
			return err
		}
		check, err := c.GetMeta(r.ctx, "catalog_key_check")
		c.Close()
		if err != nil {
			return err
		}
		confirm = check == ""
	}
	secret := []byte(os.Getenv("GLESHA_CATALOG_PASSPHRASE"))
	if len(secret) == 0 && r.env.CatalogPasswordFile == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("cli: catalog passphrase needs a terminal; provide --catalog-passphrase-file FILE")
	}
	if r.env.CatalogPasswordFile != "" || len(secret) == 0 {
		var err error
		secret, err = password(r.ctx, r.env.CatalogPasswordFile, "Catalog passphrase", confirm)
		if err != nil {
			return err
		}
	}
	r.service.CatalogPassword = secret
	return nil
}
func (r *runtime) syncStores() error {
	if id := r.service.Set.CatalogTo; id != "" && id != "local" {
		return r.stores([]string{id})
	}
	return nil
}
func count(args []string, n int) error {
	if len(args) != n {
		if n == 0 {
			return fmt.Errorf("cli: unexpected arguments; see --help for usage")
		}
		noun := "arguments"
		if n == 1 {
			noun = "argument"
		}
		return fmt.Errorf("cli: expected %d %s; see --help for usage", n, noun)
	}
	return nil
}
func requireSet(r *runtime, args []string) (func(), error) {
	if err := count(args, 1); err != nil {
		return nil, err
	}
	if err := r.openSet(args[0]); err != nil {
		return nil, err
	}
	return r.lock()
}
func ExitCode(err error) int {
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var syncError *reportedCommandError
	if errors.As(err, &syncError) {
		return ExitCode(syncError.cause)
	}
	if errors.Is(err, backup.ErrConflict) {
		return 4
	}
	if errors.Is(err, backup.ErrPending) {
		return 3
	}
	if strings.HasPrefix(err.Error(), "cli:") {
		return 2
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 2
	}
	return 1
}
