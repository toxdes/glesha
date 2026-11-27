package cli

import "flag"

// help groups, in display order
const (
	groupGeneral     = "Options"
	groupRemote      = "Remote options"
	groupArchive     = "Archive options"
	groupPassphrase  = "Passphrase options"
	groupRetrieval   = "Retrieval options"
	groupPerformance = "Performance options"
	groupGlobal      = "Global options"
)

var helpGroupOrder = []string{groupGeneral, groupRemote, groupArchive, groupPassphrase, groupRetrieval, groupPerformance, groupGlobal}

// option is one command-line flag: how it parses and how help presents it.
type option struct {
	name string
	// aliases are other spellings, shown before name in help
	aliases []string
	// value is the help placeholder; empty for boolean flags
	value string
	group string
	// usage is lowercase, without a trailing period
	usage string
	bind  func(f *flag.FlagSet, e *AppCmdEnv, name string)
}

// options lists every flag. Commands accept the global ones and those they name.
var options = []option{
	{name: "class", value: "CLASS", group: groupGeneral, usage: "new AWS storage class",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.StorageClass, n, "", "") }},
	{name: "file", value: "FILE", group: groupGeneral, usage: "local archive to import",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.File, n, "", "") }},
	{name: "key", value: "KEY", group: groupGeneral, usage: "object key (default with --file: the file name)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Key, n, "", "") }},
	{name: "into", value: "DIR", group: groupGeneral, usage: "unpack into DIR, which must not exist",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Into, n, "", "") }},
	{name: "snapshot", value: "ID", group: groupGeneral, usage: "snapshot ID, or 'latest' (default: latest)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Snapshot, n, "latest", "") }},
	{name: "full", group: groupGeneral, usage: "create a full snapshot (default)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Full, n, false, "") }},
	{name: "incremental", group: groupGeneral, usage: "store only the changes since the baseline",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Incremental, n, false, "") }},
	{name: "root", value: "NAME=PATH", group: groupGeneral, usage: "bind root NAME to directory PATH; repeatable",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) {
			e.Root = mappings{}
			f.Var(&e.Root, n, "")
		}},
	{name: "baseline", value: "ID", group: groupGeneral, usage: "use snapshot ID as the next incremental's baseline",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Baseline, n, "", "") }},
	{name: "path", value: "PATH", group: groupGeneral, usage: "list entries under PATH (default: the roots)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Path, n, "", "") }},
	{name: "recursive", group: groupGeneral, usage: "include all descendants",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Recursive, n, false, "") }},
	{name: "refresh", group: groupGeneral, usage: "query the remotes for archive availability",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Refresh, n, false, "") }},
	{name: "all", group: groupGeneral, usage: "process every set",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.All, n, false, "") }},
	{name: "cancel", group: groupGeneral, usage: "cancel unfinished restore tasks",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Cancel, n, false, "") }},
	{name: "dry-run", group: groupGeneral, usage: "list the files without deleting them",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.DryRun, n, false, "") }},
	{name: "now", group: groupGeneral, usage: "delete permanently now, without the trash period",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Now, n, false, "") }},
	{name: "keep", value: "local|remote", group: groupGeneral, usage: "settle conflicts using local or remote values",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Keep, n, "", "") }},
	{name: "authority", value: "REMOTE", group: groupGeneral, usage: "make the catalog on REMOTE authoritative",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Authority, n, "", "") }},
	{name: "archive", value: "FILE", group: groupGeneral, usage: "use the completed archive moved to FILE",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Archive, n, "", "") }},
	{name: "date", value: "DATE", group: groupGeneral, usage: "original backup time, in RFC 3339 format",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Date, n, "", "") }},
	{name: "deep-verify", group: groupGeneral, usage: "compare content with the uploaded copy, not size",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.DeepVerify, n, false, "") }},
	{name: "output", value: "FILE", group: groupGeneral, usage: "write the archive to FILE, which must not exist",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Output, n, "", "") }},
	{name: "force", group: groupGeneral, usage: "replace an existing object",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Force, n, false, "") }},
	{name: "storage-class", value: "CLASS", group: groupArchive, usage: "AWS storage class of snapshots",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.StorageClass, n, "", "") }},
	{name: "remote", group: groupGeneral, usage: "search --from remotes (deprecated: use catalog sync)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Remote, n, false, "") }},
	{name: "assume-yes", group: groupGeneral, usage: "answer yes to all confirmations",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.AssumeYes, n, false, "") }},
	{name: "to", value: "REMOTE...", group: groupRemote, usage: "limit bucket access to REMOTE",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.Var(&e.To, n, "") }},
	{name: "from", value: "REMOTE...", group: groupRemote, usage: "limit bucket access to REMOTE",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.Var(&e.From, n, "") }},
	{name: "catalog-to", value: "REMOTE|local", group: groupRemote, usage: "where to keep the catalog (default: the first --to remote)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.CatalogTo, n, "", "") }},
	{name: "compression", value: "FORMAT", group: groupArchive, usage: "compression: xz, gzip, or bzip2 (default: xz)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Compression, n, "", "") }},
	{name: "compression-level", value: "N", group: groupArchive, usage: "compression level, 1 to 9 (default: 6)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.IntVar(&e.CompressionLevel, n, 0, "") }},
	{name: "prefix", value: "PREFIX", group: groupArchive, usage: "name prefix: glesha or encb (default: glesha)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Prefix, n, "", "") }},
	{name: "archive-mode", value: "MODE", group: groupArchive, usage: "buffering: auto, memory, or stream (default: auto)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.ArchiveMode, n, "", "") }},
	{name: "incremental-storage-class", value: "CLASS", group: groupArchive, usage: "AWS storage class of incremental snapshots (default: --storage-class)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.IncrementalStorageClass, n, "", "") }},
	{name: "chunked", group: groupArchive, usage: "upload in chunks using bounded local space",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Chunked, n, false, "") }},
	{name: "spool-max", value: "SIZE", group: groupArchive, usage: "local space for chunks (default: 128MiB, min: 2MiB)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.SpoolMax, n, "", "") }},
	{name: "keep-archive", group: groupArchive, usage: "keep the local archive after a successful upload",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.KeepArchive, n, false, "") }},
	{name: "passphrase-file", value: "FILE", group: groupPassphrase, usage: "read the archive passphrase from FILE ('-': stdin)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.PassphraseFile, n, "", "") }},
	{name: "archive-passphrase-file", value: "ID=FILE", group: groupPassphrase, usage: "read the passphrase of snapshot ID from FILE",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) {
			e.ArchivePassphraseFiles = mappings{}
			f.Var(&e.ArchivePassphraseFiles, n, "")
		}},
	{name: "catalog-passphrase-file", value: "FILE", group: groupPassphrase, usage: "read the catalog passphrase from FILE ('-': stdin)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.CatalogPassphraseFile, n, "", "") }},
	{name: "request-retrieval", group: groupRetrieval, usage: "allow charged cold retrieval without asking",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.RequestRetrieval, n, false, "") }},
	{name: "retrieval", value: "TIER", group: groupRetrieval, usage: "tier: bulk, standard, or expedited (default: bulk)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Retrieval, n, "bulk", "") }},
	{name: "days", value: "N", group: groupRetrieval, usage: "keep retrieved copies for N days (default: 3)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.IntVar(&e.Days, n, 0, "") }},
	{name: "memory-max", value: "SIZE", group: groupPerformance, usage: "memory limit (default: half of available memory, or 128MiB)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.MemoryMax, n, "", "") }},
	{name: "transfer-workers", aliases: []string{"j", "jobs"}, value: "N", group: groupPerformance, usage: "run up to N transfers at once (default: 4)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.IntVar(&e.TransferWorkers, n, 0, "") }},
	{name: "hash-workers", value: "N", group: groupPerformance, usage: "hash up to N files at once (default: 4)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.IntVar(&e.HashWorkers, n, 0, "") }},
	{name: "config", value: "FILE", group: groupGlobal, usage: "read settings from FILE",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Config, n, "", "") }},
	{name: "state-dir", value: "DIR", group: groupGlobal, usage: "keep local state in DIR",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.StateDir, n, "", "") }},
	{name: "env", value: "FILE", group: groupGlobal, usage: "load credentials from FILE",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Env, n, "", "") }},
	{name: "format", value: "FORMAT", group: groupGlobal, usage: "output format: plain or json (default: plain)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Format, n, "plain", "") }},
	{name: "progress", value: "MODE", group: groupGlobal, usage: "progress: auto, plain, or off (default: auto)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Progress, n, "auto", "") }},
	{name: "color", value: "WHEN", group: groupGlobal, usage: "color: auto, yes, or no (default: auto)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.Color, n, "auto", "") }},
	{name: "log-level", aliases: []string{"L"}, value: "LEVEL", group: groupGlobal, usage: "debug, info, warn, error, or silent (default: info)",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.StringVar(&e.LogLevel, n, "info", "") }},
	{name: "help", aliases: []string{"h"}, group: groupGlobal, usage: "show this help",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Help, n, false, "") }},
	{name: "version", aliases: []string{"v"}, group: groupGlobal, usage: "show version information",
		bind: func(f *flag.FlagSet, e *AppCmdEnv, n string) { f.BoolVar(&e.Version, n, false, "") }},
}

var optionIndex = indexOptions()

func indexOptions() map[string]*option {
	index := map[string]*option{}
	for i := range options {
		o := &options[i]
		for _, name := range append([]string{o.name}, o.aliases...) {
			index[name] = o
		}
	}
	return index
}

// globalOption reports whether name, or an alias, is accepted before the command.
func globalOption(name string) bool {
	o, ok := optionIndex[name]
	return ok && o.group == groupGlobal
}

func (o *option) register(f *flag.FlagSet, e *AppCmdEnv) {
	for _, name := range append([]string{o.name}, o.aliases...) {
		o.bind(f, e, name)
	}
}

// given reports whether the user set the option name, under any of its spellings.
func (e *AppCmdEnv) given(name string) bool {
	o := optionIndex[name]
	for _, spelling := range append([]string{o.name}, o.aliases...) {
		if e.present[spelling] {
			return true
		}
	}
	return false
}
