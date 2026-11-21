package cli

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

	"glesha/internal/backup"
	"glesha/internal/cloud"
	"glesha/internal/config"
	"glesha/internal/database/model"
	"glesha/internal/database/repository"
	"glesha/internal/fsutil"
	"glesha/internal/logger"
	"glesha/internal/memory"
	"glesha/internal/state"
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

// AppCmdEnv holds the parsed command-line options. Each field is named after its
// flag; a command only sets the fields of the options it accepts.
type AppCmdEnv struct {
	// global
	Config   string
	StateDir string
	Env      string
	Format   string
	Progress string
	Color    string
	LogLevel string
	Help     bool
	Version  bool

	// remotes
	To        destinations
	From      destinations
	CatalogTo string

	// archive
	Compression             string
	CompressionLevel        int
	Prefix                  string
	ArchiveMode             string
	StorageClass            string // also --class
	IncrementalStorageClass string
	Chunked                 bool
	SpoolMax                string
	KeepArchive             bool

	// passphrases
	PassphraseFile         string
	CatalogPassphraseFile  string
	ArchivePassphraseFiles mappings

	// retrieval
	Retrieval        string
	Days             int
	RequestRetrieval bool

	// performance
	MemoryMax       string
	HashWorkers     int
	TransferWorkers int // also --jobs and -j

	// command-specific
	Output      string
	Into        string
	File        string
	Key         string
	Archive     string
	Snapshot    string
	Path        string
	Date        string
	Baseline    string
	Keep        string
	Authority   string
	Root        mappings
	Incremental bool
	Full        bool
	Recursive   bool
	Refresh     bool
	Remote      bool
	Force       bool
	AssumeYes   bool
	DeepVerify  bool
	All         bool
	DryRun      bool
	Cancel      bool

	// present records the flags given, by spelling; read it with given
	present map[string]bool
}

type runtime struct {
	emitted                int
	command                string
	catalogCheckProgress   *logger.Progress
	catalogCheckActive     bool
	catalogCacheUpdated    bool
	catalogReplicasUpdated int
	remoteScope            *remoteScope
	operationLocked        bool
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

// Execute runs one glesha invocation.
func Execute(ctx context.Context, args []string, version, sha string) error {
	command, rest, err := resolveCommand(args)
	if err != nil {
		return err
	}
	// spec is nil only when no command was given, which shows the overview
	spec, _ := lookupCommand(command)
	e, args, err := parseFlags(command, rest)
	if err != nil {
		return err
	}
	logger.SetStructuredDiagnostics(e.jsonOutput())
	defer logger.SetStructuredDiagnostics(false)
	early := &runtime{ctx: ctx, env: e, out: os.Stdout, command: command}
	if e.given("state-dir") && e.StateDir == "" {
		return fmt.Errorf("cli: --state-dir requires a directory")
	}
	if err = e.expandPaths(); err != nil {
		return err
	}
	if done, err := early.showInformation(spec, args, version, sha); done {
		return err
	}
	if err = logger.SetLevelFromString(e.LogLevel); err != nil {
		return err
	}
	if err = logger.SetColorModeFromString(e.Color); err != nil {
		return err
	}
	if err = checkAction(spec, args); err != nil {
		return err
	}
	c, err := config.Load(e.Config)
	if err != nil {
		return err
	}
	if e.StateDir != "" {
		c.Catalog.Directory = e.StateDir
	}
	if spec.setup == configSetup {
		early.config = c
		return spec.run(early, args)
	}
	if err = config.Env(e.Env); err != nil {
		return err
	}
	c.ProviderEnv()
	if err = e.applyTo(&c); err != nil {
		return err
	}
	budget, err := memory.Resolve(c.Memory.Max, c.Workers.Hash, c.Workers.Transfer, memory.Available)
	if err != nil {
		return err
	}
	budget.Apply()
	ctx = memory.WithBudget(ctx, budget)
	root, err := stateRoot(ctx, c, spec.setup == fullSetup)
	if err != nil {
		return err
	}
	var registry repository.Registryor
	if spec.setup == fullSetup {
		var closeState func()
		registry, closeState, err = openState(ctx, root)
		if err != nil {
			return err
		}
		defer closeState()
	}
	ctx = logger.WithWorkerDetails(ctx, e.given("transfer-workers") || e.given("hash-workers"))
	r := &runtime{ctx: ctx, command: command, env: e, config: c, budget: budget, registry: registry, root: root, out: os.Stdout, limit: make(chan struct{}, budget.TransferWorkers)}
	renderer := newProgressRenderer(e)
	if renderer != nil {
		defer renderer.Close()
		r.ctx = logger.WithProgress(ctx, renderer)
	}
	logger.Debug(fmt.Sprintf("command=%s; memory=%s; hash-workers=%d; transfer-workers=%d", command, logger.Bytes(budget.Total), budget.HashWorkers, budget.TransferWorkers))
	if err := r.selectCommandRemotes(); err != nil {
		return err
	}
	err = spec.run(r, args)
	if spec.recordsResult {
		r.recordResult(err)
	}
	if renderer != nil {
		renderer.Close()
		if err == nil {
			err = renderer.Err()
		}
	}
	if err == nil && e.jsonOutput() && r.emitted == 0 && !spec.listsResults {
		return r.completed("", "", "")
	}
	return err
}

// parseFlags parses the options of command and returns the positional arguments.
func parseFlags(command string, args []string) (*AppCmdEnv, []string, error) {
	e := &AppCmdEnv{present: map[string]bool{}}
	f := flagSet(command, e)
	args, err := reorder(f, args)
	if err != nil {
		return nil, nil, err
	}
	if err = f.Parse(args); err != nil {
		return nil, nil, fmt.Errorf("cli: %w", err)
	}
	f.Visit(func(v *flag.Flag) { e.present[v.Name] = true })
	if err = normalizeOutput(command, e); err != nil {
		return nil, nil, err
	}
	return e, f.Args(), nil
}

// expandPaths resolves '~' in path options; '-' keeps meaning stdin.
func (e *AppCmdEnv) expandPaths() error {
	for _, p := range []*string{&e.Output, &e.Into, &e.File, &e.Archive, &e.PassphraseFile, &e.CatalogPassphraseFile} {
		if *p != "" && *p != "-" {
			expanded, err := fsutil.Expand(*p)
			if err != nil {
				return err
			}
			*p = expanded
		}
	}
	return nil
}

// showInformation handles version and help requests; done reports whether it did.
func (r *runtime) showInformation(spec *command, args []string, version, sha string) (done bool, err error) {
	e := r.env
	if r.command == "version" && !e.Help {
		if err := count(args, 0); err != nil {
			return true, err
		}
		e.Version = true
	}
	if e.Version {
		return true, r.emit("version", struct {
			Version  string `json:"version"`
			Revision string `json:"revision"`
		}{version, sha}, fmt.Sprintf("glesha %s (%s)", logger.ASCII(version), logger.ASCII(sha)))
	}
	switch {
	case r.command == "help":
		return true, r.help(args)
	case spec == nil:
		return true, r.help(nil)
	case e.Help:
		helpArgs := []string{r.command}
		if len(spec.actions) > 0 && len(args) > 0 {
			helpArgs = append(helpArgs, args[0])
		}
		return true, r.help(helpArgs)
	}
	return false, nil
}

// checkAction rejects a missing or unknown catalog action before any setup.
func checkAction(spec *command, args []string) error {
	if spec.name != "catalog" {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("cli: choose a catalog action: sync, unlock, or resolve; see glesha help catalog")
	}
	if spec.action(args[0]) == nil {
		return fmt.Errorf("cli: unknown catalog action %q; see glesha help catalog", args[0])
	}
	return nil
}

// applyTo applies command-line overrides to c and validates the result.
func (e *AppCmdEnv) applyTo(c *config.Config) error {
	if e.Compression != "" {
		c.Archive.Compression = e.Compression
	}
	if e.Prefix != "" {
		c.Archive.Prefix = e.Prefix
	}
	if e.CompressionLevel != 0 {
		c.Archive.Level = e.CompressionLevel
	}
	if e.ArchiveMode != "" {
		c.Archive.Mode = e.ArchiveMode
	}
	if e.MemoryMax != "" {
		c.Memory.Max = e.MemoryMax
	}
	if e.given("compression-level") && e.CompressionLevel < 1 || e.given("hash-workers") && e.HashWorkers < 1 || e.given("transfer-workers") && e.TransferWorkers < 1 {
		return fmt.Errorf("cli: numeric overrides must be positive")
	}
	if e.HashWorkers > 0 {
		c.Workers.Hash = e.HashWorkers
	}
	if e.TransferWorkers > 0 {
		c.Workers.Transfer = e.TransferWorkers
	}
	return c.Validate()
}

// stateRoot returns the absolute local state directory. With checkLegacy, it refuses
// to start while state still sits in the legacy location.
func stateRoot(ctx context.Context, c config.Config, checkLegacy bool) (string, error) {
	root := c.Catalog.Directory
	if root == "" {
		var err error
		if root, err = config.DefaultStatePath(); err != nil {
			return "", err
		}
		legacy, err := config.LegacyStatePath()
		if err != nil {
			return "", err
		}
		if checkLegacy {
			if err = state.CheckDefault(ctx, legacy, root); err != nil {
				return "", err
			}
		}
	}
	root, err := fsutil.Expand(root)
	if err != nil {
		return "", err
	}
	return filepath.Abs(root)
}

// openState locks the state directory and opens its registry; closeState undoes both.
func openState(ctx context.Context, root string) (registry repository.Registryor, closeState func(), err error) {
	stateLock, err := state.Lock(root, false)
	if err != nil {
		return nil, nil, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		stateLock.Unlock()
		return nil, nil, err
	}
	registry, err = repository.NewRegistryRepository(ctx, filepath.Join(root, "registry.db"))
	if err != nil {
		stateLock.Unlock()
		return nil, nil, err
	}
	return registry, func() {
		registry.Close()
		stateLock.Unlock()
	}, nil
}

// newProgressRenderer picks the progress output for e; nil means no progress.
func newProgressRenderer(e *AppCmdEnv) *logger.ProgressRenderer {
	switch {
	case e.Progress == "off" || !logger.ProgressEnabled():
		return nil
	case e.jsonOutput():
		return logger.NewEventProgressRenderer(logger.EmitProgress)
	case e.Progress == "plain" || !term.IsTerminal(int(os.Stderr.Fd())) || os.Getenv("TERM") == "dumb":
		return logger.NewPlainProgressRenderer(os.Stderr)
	default:
		return logger.NewProgressRenderer(os.Stderr, logger.ColorEnabled(true))
	}
}

// recordResult saves the outcome of a set operation, if the set is not busy again.
func (r *runtime) recordResult(err error) {
	if !r.operationLocked || r.service == nil || r.service.Set.ID == "" || r.env.All || r.env.Cancel {
		return
	}
	lock := flock.New(filepath.Join(r.service.Directory, "writer.lock"))
	if free, lockErr := lock.TryLock(); lockErr == nil && free {
		if recordErr := r.service.RecordResult(context.WithoutCancel(r.ctx), r.command, err); recordErr != nil {
			logger.Debug(recordErr.Error())
		}
		lock.Unlock()
	}
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
		store, err := cloud.New(r.ctx, cloud.Remote{Kind: p.Kind, Bucket: p.Bucket, Region: p.Region, Endpoint: p.Endpoint}, r.budget.TransferWorkers, r.limit)
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
	provided := r.env.CatalogPassphraseFile != "" || os.Getenv("GLESHA_CATALOG_PASSPHRASE") != ""
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
	if len(secret) == 0 && r.env.CatalogPassphraseFile == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("cli: catalog passphrase needs a terminal; provide --catalog-passphrase-file FILE")
	}
	if r.env.CatalogPassphraseFile != "" || len(secret) == 0 {
		var err error
		secret, err = password(r.ctx, r.env.CatalogPassphraseFile, "Catalog passphrase", confirm)
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
