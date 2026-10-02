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
	Config, Env, LogLevel, Color, Output, Into, File, Archive, Key, CatalogTo, Compression, Prefix, Mode, Class, IncrementalClass, Snapshot, Filter, Memory, PasswordFile, CatalogPasswordFile, Date, Baseline, Retrieval string
	To, From                                                                                                                                                                                                              destinations
	Roots, Passwords                                                                                                                                                                                                      mappings
	Level, HashWorkers, TransferWorkers, Days                                                                                                                                                                             int
	Incremental, Full, Recursive, Refresh, Remote, JSON, Force, AssumeYes, RequestRetrieval, Deep, Help, Version, KeepArchive                                                                                             bool
	present                                                                                                                                                                                                               map[string]bool
}

var appCmdEnv *AppCmdEnv

type runtime struct {
	ctx      context.Context
	env      *AppCmdEnv
	config   config.Config
	budget   memory.Budget
	registry repository.Registryor
	root     string
	service  *backup.Service
	out      io.Writer
	limit    chan struct{}
}
type handler func(*runtime, []string) error

var handlers = map[string]handler{
	"create": create, "configure": configure, "run": run, "retry": retry, "list": list, "status": status, "history": history, "browse": browse, "restore": restore, "import": importArchive, "move": move,
	"archive": localArchive, "decrypt": decrypt, "extract": extract, "upload": upload, "download": download, "catalog": catalog, "storage-class": storageClass, "check": check,
}

func Usage() string {
	return `glesha - encrypted archives and cloud backups

Usage:
  glesha [global options] COMMAND [arguments] [options]
  glesha help COMMAND

Commands:
  create SET PATH...          Register a backup set
  configure SET               Change settings or map imported sources
  run SET                     Create and upload a backup
  retry SET                   Resume pending work
  list                        List local sets; --remote discovers sets
  status [SET]                Show backup state, sizes and estimated cost
  history SET                 List snapshots and transfer status
  browse SET                  Browse the local catalog
  restore SET --into DIR      Recover a snapshot into a new directory
  import SET                  Register an existing archive
  move SET --to REMOTE        Copy complete history, then switch providers
  archive PATH...             Create a standalone encrypted archive
  decrypt FILE                Validate and decrypt to compressed tar
  extract FILE --into DIR     Extract into a new directory
  upload FILE --to REMOTE     Upload bytes; replacement needs --force
  download KEY --from REMOTE  Download using ordered provider fallback
  catalog ACTION SET          pull, push, reconcile or abandon metadata
  storage-class SET           Change an AWS archive's storage class
  check --to REMOTE           Test provider access
  help [COMMAND]              Show options and examples
  version                     Show version and build revision

Global options:
  --config FILE               Nonsecret TOML settings
  --env FILE                  Credential file; process variables take priority
  -L, --log-level LEVEL       debug, info, warn, error, silent (default: info)
  --color MODE                yes, no, auto (default: auto)
  -h, --help                  Show help
  -v, --version               Show version

Examples:
  glesha create music ~/Music --to b2
  glesha run music --to b2 --env /secure/cloud.env
  glesha run music --incremental --env /secure/cloud.env
  glesha help run

`
}
func PrintUsage() { fmt.Print(Usage()) }
func flagSet(command string, e *AppCmdEnv) *flag.FlagSet {
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&e.Config, "config", "", "TOML configuration file")
	f.StringVar(&e.Env, "env", "", "explicit credential environment file")
	f.StringVar(&e.LogLevel, "log-level", "info", "debug, info, warn, error or silent")
	f.StringVar(&e.LogLevel, "L", "info", "debug, info, warn, error or silent")
	f.StringVar(&e.Color, "color", "auto", "yes, no or auto; always/never aliases accepted")
	f.BoolVar(&e.Help, "help", false, "show help")
	f.BoolVar(&e.Help, "h", false, "show help")
	f.BoolVar(&e.Version, "version", false, "show version")
	f.BoolVar(&e.Version, "v", false, "show version")
	allowed := func(names string) bool { return strings.Contains(" "+names+" ", " "+command+" ") }
	if allowed("create configure run upload move check") {
		f.Var(&e.To, "to", "remote names; comma-separated or repeated")
	}
	if allowed("restore import download list status") {
		f.Var(&e.From, "from", "ordered source remotes; comma-separated or repeated")
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
	if allowed("restore extract") {
		f.StringVar(&e.Into, "into", "", "new destination directory")
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
	if command == "run" {
		f.BoolVar(&e.Incremental, "incremental", false, "require a baseline on one remote")
		f.BoolVar(&e.Full, "full", false, "explicit full snapshot")
	}
	if allowed("run retry") {
		f.BoolVar(&e.KeepArchive, "keep-archive", false, "retain the local archive after a successful backup")
	}
	if allowed("run move") {
		f.BoolVar(&e.AssumeYes, "assume-yes", false, "approve user-data removal confirmations")
	}
	if allowed("restore import move") {
		f.StringVar(&e.Retrieval, "retrieval", "bulk", "bulk, standard or expedited")
		f.IntVar(&e.Days, "days", 0, "readable-copy availability days")
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
	if command == "list" {
		f.BoolVar(&e.Remote, "remote", false, "discover remote set descriptors")
	}
	if allowed("list status history browse create configure run import move check") {
		f.BoolVar(&e.JSON, "json", false, "versioned JSON output")
	}
	if allowed("run archive import restore extract decrypt") {
		f.StringVar(&e.PasswordFile, "passphrase-file", "", "passphrase file; '-' reads stdin")
	}
	if command == "restore" {
		e.Passwords = mappings{}
		f.Var(&e.Passwords, "archive-passphrase-file", "snapshot ID=passphrase file")
	}
	if allowed("catalog run retry restore import move list storage-class") {
		f.StringVar(&e.CatalogPasswordFile, "catalog-passphrase-file", "", "separate catalog passphrase file")
	}
	if allowed("run archive import restore upload download move check decrypt extract") {
		f.StringVar(&e.Memory, "memory-max", "", "managed memory budget")
	}
	if allowed("run archive import restore upload download move check") {
		f.IntVar(&e.HashWorkers, "hash-workers", 0, "hash workers")
		f.IntVar(&e.TransferWorkers, "transfer-workers", 0, "transfer workers")
		f.IntVar(&e.TransferWorkers, "jobs", 0, "alias for --transfer-workers; memory budget may lower this limit")
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
				return nil, fmt.Errorf("cli: unsupported flag %s", a)
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
			if name != "config" && name != "env" && name != "color" && name != "log-level" && name != "L" {
				return "", nil, fmt.Errorf("cli: put operation options after the command")
			}
			if !strings.Contains(a, "=") {
				i++
			}
			continue
		}
		return "", nil, fmt.Errorf("cli: unknown command %q", a)
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
		fmt.Printf("glesha %s (%s)\n", L.ASCII(version), L.ASCII(sha))
		return nil
	}
	if command == "help" {
		return showHelp(f.Args())
	}
	if e.Help || command == "" {
		if command == "" {
			fmt.Print(Usage())
		} else {
			fmt.Print(CommandUsage(command))
		}
		return nil
	}
	if err = L.SetLevelFromString(e.LogLevel); err != nil {
		return err
	}
	if err = L.SetColorModeFromString(e.Color); err != nil {
		return err
	}
	c, err := config.Load(e.Config)
	if err != nil {
		return err
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
	root := c.Catalog.Directory
	if root == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		root = filepath.Join(base, "glesha", "state-v2")
	}
	root, err = file_io.Expand(root)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	registry, err := repository.NewRegistryRepository(ctx, filepath.Join(root, "registry.db"))
	if err != nil {
		return err
	}
	defer registry.Close()
	r := &runtime{ctx: ctx, env: e, config: c, budget: budget, registry: registry, root: root, out: os.Stdout, limit: make(chan struct{}, budget.TransferWorkers)}
	if !e.JSON && term.IsTerminal(int(os.Stderr.Fd())) && L.ProgressEnabled() {
		renderer := L.NewProgressRenderer(os.Stderr, L.ColorEnabled(true))
		defer renderer.Close()
		r.ctx = L.WithProgress(ctx, renderer)
	}
	L.Debug(fmt.Sprintf("command=%s; memory=%s; hash-workers=%d; transfer-workers=%d", command, L.Bytes(budget.Total), budget.HashWorkers, budget.TransferWorkers))
	return handlers[command](r, f.Args())
}
func (r *runtime) openSet(name string) error {
	r.service = &backup.Service{Config: r.config, Budget: r.budget, Registry: r.registry, Stores: map[string]cloud.Store{}, Kinds: map[string]string{}}
	return r.service.OpenSet(r.ctx, name, r.root)
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
	for _, id := range ids {
		if r.service.Stores[id] != nil {
			continue
		}
		remote, err := r.registry.Remote(r.ctx, id)
		if err != nil {
			return err
		}
		p, ok := r.config.RemotesByName()[remote.Name]
		if !ok {
			return fmt.Errorf("cli: remote %q is not configured", remote.Name)
		}
		if _, err = r.registry.BindRemote(r.ctx, model.Remote{Name: remote.Name, Kind: p.Kind, Bucket: p.Bucket, Endpoint: p.Endpoint, Region: p.Region}); err != nil {
			return err
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
	if !r.config.Catalog.Encrypted || remote == "local" {
		return nil
	}
	if confirm && r.service.Set.ID != "" {
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
	if r.env.CatalogPasswordFile != "" || len(secret) == 0 {
		var err error
		secret, err = password(r.env.CatalogPasswordFile, "Catalog passphrase", confirm)
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
		return fmt.Errorf("cli: expected %d positional arguments", n)
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

func CommandUsage(name string) string {
	var out strings.Builder
	out.WriteString("Usage:\n")
	fmt.Fprintf(&out, "  glesha %s [options]\n\n", strings.TrimSpace(name+" "+commandArguments[name]))
	out.WriteString(wrapHelp(commandDescriptions[name], "", 78) + "\n\n")
	flags := flagSet(name, &AppCmdEnv{})
	for _, group := range []struct {
		title  string
		global bool
	}{{"Command options", false}, {"Global options", true}} {
		var lines strings.Builder
		flags.VisitAll(func(f *flag.Flag) {
			if globalFlag(f.Name) != group.global || f.Name == "L" || f.Name == "h" || f.Name == "v" || f.Name == "j" {
				return
			}
			label := "--" + f.Name
			if f.Name == "log-level" {
				label = "-L, " + label
			}
			if f.Name == "help" {
				label = "-h, " + label
			}
			if f.Name == "version" {
				label = "-v, " + label
			}
			if f.Name == "jobs" {
				label = "-j, " + label
			}
			if value := flagValueName(f); value != "" {
				label += " " + value
			}
			description := f.Usage
			if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
				description += " (default: " + f.DefValue + ")"
			}
			fmt.Fprintf(&lines, "  %s\n%s\n", label, wrapHelp(description, "    ", 78))
		})
		if lines.Len() > 0 {
			out.WriteString(group.title + ":\n" + lines.String() + "\n")
		}
	}
	if examples := commandExamples[name]; len(examples) > 0 {
		out.WriteString("Examples:\n")
		for _, example := range examples {
			out.WriteString(wrapExample(example) + "\n")
		}
	}
	return L.ASCII(out.String())
}
