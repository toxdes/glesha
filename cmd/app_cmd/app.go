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

var appCmdEnv *AppCmdEnv

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

var handlers = map[string]handler{
	"create": create, "configure": configure, "run": run, "retry": retry, "list": list, "status": status, "cleanup": cleanup, "history": history, "browse": browse, "restore": restore, "import": importArchive, "move": move,
	"archive": localArchive, "decrypt": decrypt, "extract": extract, "upload": upload, "download": download, "catalog": catalog, "storage-class": storageClass, "check": check, "state": migrateState,
}

func Usage() string {
	return `glesha - encrypted archives and cloud backups

Usage:
  glesha [global options] COMMAND [arguments] [options]

Back up and recover:
  create SET PATH...          Register sources and a backup destination
  run SET                     Create and upload a backup
  restore SET --into DIR      Restore files without overwriting existing files
  import SET                  Register an existing archive

Inspect:
  list                        List locally known sets and IDs
  status [SET]                Show backup state, sizes, and estimated cost
  history SET                 List snapshots
  browse SET                  List files in a snapshot
  catalog sync [SET]          Discover sets or synchronize one catalog
  catalog unlock SET          Open a downloaded encrypted catalog locally

Manage:
  configure SET               Change saved settings or bind local sources
  retry SET                   Resume pending work
  cleanup SET|--all           Remove eligible local archive copies
  move SET --from A --to B     Copy history and switch destinations
  storage-class SET           Change an AWS archive's storage class
  check --to REMOTE...        Test bucket access
  state migrate              Move legacy local state to the current location

Standalone files:
  archive PATH...             Create an encrypted archive
  decrypt FILE                Decrypt to compressed tar
  extract FILE --into DIR     Extract into a new directory
  upload FILE --to REMOTE...  Upload a file
  download KEY --from REMOTE  Download a file

Help and version:
  help [COMMAND [ACTION]]     Show options and examples
  version                     Show version and build revision

Global options:
  --config FILE               TOML settings
  --state-dir DIR             Local state directory override
  --env FILE                  Credential file; process variables take priority
  --color MODE                auto, yes, or no (default: auto)
  -L, --log-level LEVEL       debug, info, warn, error, silent (default: info)
  --format FORMAT             plain or json; status also supports table/csv
  --progress MODE             auto, plain or off (default: auto)
  -h, --help                  Show help
  -v, --version               Show version

Examples:
  glesha create docs ~/Documents --to b2
  glesha run docs
  glesha help catalog

`
}

func PrintUsage() { fmt.Print(Usage()) }
func flagSet(command string, e *AppCmdEnv) *flag.FlagSet {
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&e.Config, "config", "", "TOML configuration file")
	f.StringVar(&e.StateDirectory, "state-dir", "", "local state directory; overrides catalog.directory")
	f.StringVar(&e.Env, "env", "", "explicit credential environment file")
	f.StringVar(&e.LogLevel, "log-level", "info", "debug, info, warn, error or silent")
	f.StringVar(&e.LogLevel, "L", "info", "debug, info, warn, error or silent")
	f.StringVar(&e.Color, "color", "auto", "yes, no or auto; always/never aliases accepted")
	f.StringVar(&e.Format, "format", "plain", "plain or json; status also supports table/csv")
	f.StringVar(&e.Progress, "progress", "auto", "auto, plain or off")
	f.BoolVar(&e.Help, "help", false, "show help")
	f.BoolVar(&e.Help, "h", false, "show help")
	f.BoolVar(&e.Version, "version", false, "show version")
	f.BoolVar(&e.Version, "v", false, "show version")
	allowed := func(names string) bool { return strings.Contains(" "+names+" ", " "+command+" ") }
	if allowed("create configure run retry restore import move storage-class history browse list status catalog upload download check cleanup") {
		f.Var(&e.To, "to", "destination/allowed remotes; combined with --from limits all bucket access")
		f.Var(&e.From, "from", "ordered source/allowed remotes; combined with --to limits all bucket access")
	}
	if allowed("create configure") {
		f.StringVar(&e.CatalogTo, "catalog-to", "", "catalog remote or local")
		f.StringVar(&e.IncrementalClass, "incremental-storage-class", "", "AWS incremental storage class")
	}
	if allowed("create configure run archive") {
		f.StringVar(&e.Compression, "compression", "", "xz, gzip or bzip2")
		f.IntVar(&e.Level, "compression-level", 0, "compression level 1..9")
		f.StringVar(&e.Prefix, "prefix", "", "glesha or encb")
	}
	if allowed("run archive") {
		f.StringVar(&e.Mode, "archive-mode", "", "auto, memory or stream")
	}
	if allowed("create configure run upload archive storage-class") {
		f.StringVar(&e.Class, "storage-class", "", "AWS storage class")
		if command == "storage-class" {
			f.StringVar(&e.Class, "class", "", "AWS storage class")
		}
	}
	if allowed("run archive decrypt download") {
		f.StringVar(&e.Output, "output", "", "new output file")
	}
	if command == "restore" {
		f.BoolVar(&e.CancelRestore, "cancel", false, "cancel local restore tasks; preserve restored files and staging")
	}
	if allowed("restore extract") {
		description := "new destination directory"
		if command == "restore" {
			description = "destination directory; existing files are never overwritten"
		}
		f.StringVar(&e.Into, "into", "", description)
	}
	if allowed("configure import") {
		e.Roots = mappings{}
		f.Var(&e.Roots, "root", "archive root NAME=source PATH")
		f.StringVar(&e.Baseline, "baseline", "", "explicit snapshot baseline")
	}
	if command == "import" {
		f.StringVar(&e.File, "file", "", "local archive")
		f.StringVar(&e.Key, "key", "", "exact remote object key")
		f.StringVar(&e.Date, "date", "", "known backup date in RFC3339")
		f.BoolVar(&e.Deep, "deep-verify", false, "verify associated object bytes")
	}
	if command == "upload" {
		f.StringVar(&e.Key, "key", "", "remote key (default filename)")
		f.BoolVar(&e.Force, "force", false, "explicitly replace an existing remote key")
	}
	if command == "retry" {
		f.StringVar(&e.Archive, "archive", "", "relocated completed archive")
	}
	if allowed("create configure run") {
		f.BoolVar(&e.Chunked, "chunked", false, "upload independent chunks using bounded temporary storage")
		f.StringVar(&e.SpoolMax, "spool-max", "", "maximum temporary chunk payload storage (default 128MiB)")
	}
	if command == "run" {
		f.BoolVar(&e.Incremental, "incremental", false, "require a baseline on one remote")
		f.BoolVar(&e.Full, "full", false, "explicit full snapshot")
	}
	if allowed("run retry") {
		f.BoolVar(&e.KeepArchive, "keep-archive", false, "retain the local archive after a successful backup")
	}
	if allowed("run move catalog cleanup") {
		f.BoolVar(&e.AssumeYes, "assume-yes", false, "approve user-data removal confirmations")
	}
	if allowed("restore import move") {
		f.StringVar(&e.Retrieval, "retrieval", "bulk", "bulk, standard or expedited")
		f.IntVar(&e.Days, "days", 0, "readable-copy availability days (default 3; pending requests keep saved duration)")
		f.BoolVar(&e.RequestRetrieval, "request-retrieval", false, "explicitly authorize retrieval charges")
	}
	if allowed("browse restore storage-class") {
		f.StringVar(&e.Snapshot, "snapshot", "latest", "snapshot ID or latest")
	}
	if command == "browse" {
		f.StringVar(&e.Filter, "path", "", "catalog path")
		f.BoolVar(&e.Recursive, "recursive", false, "include descendants")
	}
	if allowed("status history") {
		f.BoolVar(&e.Refresh, "refresh", false, "check live metadata without requesting retrieval")
	}
	if allowed("catalog cleanup") {
		description := "process all locally registered sets"
		if command == "catalog" {
			description = "discover configured buckets and download catalog state"
		}
		f.BoolVar(&e.All, "all", false, description)
		if command == "catalog" {
			f.StringVar(&e.Keep, "keep", "", "resolve conflicts using local or remote values")
			f.StringVar(&e.Authority, "authority", "", "select the catalog authority during explicit resolution")
		}
	}
	if command == "cleanup" {
		f.BoolVar(&e.DryRun, "dry-run", false, "preview archive cleanup")
	}
	if command == "list" {
		f.BoolVar(&e.Remote, "remote", false, "discover remote set descriptors")
	}
	if allowed("run archive import restore extract decrypt") {
		f.StringVar(&e.PasswordFile, "passphrase-file", "", "passphrase file; '-' reads stdin")
	}
	if command == "restore" {
		e.Passwords = mappings{}
		f.Var(&e.Passwords, "archive-passphrase-file", "snapshot ID=passphrase file")
	}
	if allowed("catalog run retry restore import move list storage-class create configure history browse") {
		f.StringVar(&e.CatalogPasswordFile, "catalog-passphrase-file", "", "separate catalog passphrase file")
	}
	if allowed("run archive import restore upload download move check decrypt extract") {
		f.StringVar(&e.Memory, "memory-max", "", "managed memory budget")
	}
	if allowed("run archive import restore upload download move check") {
		f.IntVar(&e.HashWorkers, "hash-workers", 0, "hash workers")
		f.IntVar(&e.TransferWorkers, "transfer-workers", 0, "maximum concurrent transfer workers")
		f.IntVar(&e.TransferWorkers, "jobs", 0, "use up to N transfer workers; memory budget may lower the limit")
		f.IntVar(&e.TransferWorkers, "j", 0, "alias for --jobs")
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
func ResolveCommand(args []string) (string, []string, error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if _, ok := handlers[a]; ok || a == "help" || a == "version" {
			return a, append(append([]string{}, args[:i]...), args[i+1:]...), nil
		}
		if a == "--help" || a == "-h" || a == "--version" || a == "-version" || a == "-v" {
			return "", args, nil
		}
		if strings.HasPrefix(a, "-") {
			name := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
			if !globalFlag(name) {
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
	command, rest, err := ResolveCommand(args)
	if err != nil {
		return err
	}
	e := &AppCmdEnv{present: map[string]bool{}}
	appCmdEnv = e
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
		return handlers[command](early, f.Args())
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
	err = handlers[command](r, f.Args())
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
	r.service = &backup.Service{Config: r.config, Budget: r.budget, Registry: r.registry, Stores: map[string]cloud.Store{}, Kinds: map[string]string{}}
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
		if !ok || p.Kind != "aws" && p.Kind != "b2" {
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
			return fmt.Errorf("cli: catalog requires access to %s bucket %q; configure that location before syncing", remote.Kind, remote.Bucket)
		}
		store, err := cloud.New(r.ctx, p.Kind, p, r.budget.TransferWorkers, r.limit)
		if err != nil {
			return err
		}
		r.service.Stores[id] = store
		r.service.Kinds[id] = p.Kind
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

func CommandUsage(name string) string { return CommandActionUsage(name, "") }
