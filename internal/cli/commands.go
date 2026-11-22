package cli

// command is one CLI command: its handler, help and accepted options.
type command struct {
	name string
	// run is nil for help and version, which Execute handles itself
	run     handler
	help    commandHelp
	actions []helpAction
	// options are the accepted non-global flags, in help order
	options []string
	// overrides adjust how an option is described for this command
	overrides map[string]optionHelp
	setup     setup
	// recordsResult saves the outcome in the set's operation history
	recordsResult bool
	// listsResults prints a possibly empty list, so JSON output needs no completion record
	listsResults bool
}

// setup says how much of the environment a command needs before it runs.
type setup int

const (
	// fullSetup loads configuration and credentials, sizes memory and opens local state.
	fullSetup setup = iota
	// localSetup is fullSetup without local state, for standalone files.
	localSetup
	// configSetup loads configuration only.
	configSetup
)

// commandHelp is the single source for --help output and manual pages.
type commandHelp struct {
	// synopsis lines, without the leading "glesha COMMAND"
	synopsis []string
	// one-line summary, lowercase, no trailing period
	summary string
	// description paragraphs
	description []string
	examples    []string
}

type helpAction struct {
	name, arguments, summary string
	// options limits the command's options to those this action accepts
	options     []string
	description []string
}

// optionHelp overrides parts of an option's help; empty fields keep the default.
type optionHelp struct{ usage, value, group string }

var commands []command

// commands is filled in init because handlers reach the help data, which would
// otherwise be an initialization cycle.
func init() {
	commands = []command{
		{
			name: "create",
			run:  create,
			help: commandHelp{
				synopsis: []string{"SET PATH..."},
				summary:  "register a new backup set",
				description: []string{
					"Register a backup set named SET whose sources are the directories PATH. Each PATH becomes a root named after its last path component. No data is archived; use 'glesha run SET' to create the first snapshot.",
					"Remotes given with --to become the set's destinations. The catalog is stored on the first of them unless --catalog-to says otherwise. A set without --to gets its destinations from the first run.",
				},
				examples: []string{"glesha create docs ~/Documents --to b2", "glesha create photos ~/Pictures --to aws --storage-class DEEP_ARCHIVE"},
			},
			options: []string{"storage-class", "to", "from", "catalog-to", "compression", "compression-level", "prefix", "incremental-storage-class", "chunked", "spool-max", "catalog-passphrase-file"},
			overrides: map[string]optionHelp{
				"to": {usage: "upload snapshots to REMOTE"},
			},
		},
		{
			name: "configure",
			run:  configure,
			help: commandHelp{
				synopsis: []string{"SET"},
				summary:  "change the settings of a backup set",
				description: []string{
					"Change the saved settings of SET. Only the given options change; new values apply to later runs. Existing snapshots are not rewritten.",
					"Use --root NAME=PATH to bind an unbound root, such as one from an imported or discovered set, to a directory on this computer. A bound root cannot be rebound.",
					"The destinations of an incremental set and a catalog that was already published can only be changed with 'glesha move'. Finish pending work before configuring.",
				},
				examples: []string{"glesha configure docs --compression gzip", "glesha configure docs --root Documents=~/Documents"},
			},
			options: []string{"root", "baseline", "storage-class", "to", "from", "catalog-to", "compression", "compression-level", "prefix", "incremental-storage-class", "chunked", "spool-max", "catalog-passphrase-file"},
			overrides: map[string]optionHelp{
				"to":                {usage: "upload future snapshots to REMOTE"},
				"catalog-to":        {usage: "where to keep the catalog"},
				"compression":       {usage: "compression: xz, gzip, or bzip2"},
				"compression-level": {usage: "compression level, 1 to 9"},
				"prefix":            {usage: "name prefix: glesha or encb"},
				"chunked":           {usage: "upload in chunks; --chunked=false turns it off"},
			},
		},
		{
			name:          "run",
			run:           run,
			recordsResult: true,
			help: commandHelp{
				synopsis: []string{"SET"},
				summary:  "create a snapshot and upload it",
				description: []string{
					"Archive the sources of SET, encrypt the archive, and upload it to the set's remotes. The snapshot is full unless --incremental is given.",
					"An incremental snapshot stores the changes since the baseline snapshot and needs exactly one remote. Removed paths and type changes are confirmed first.",
					"The local archive is deleted after a verified upload, unless --output or --keep-archive is given. If no source changed, no snapshot is created.",
					"If a run is interrupted, run it again or use 'glesha retry' to resume it. Options must match the interrupted run, and the same archive passphrase is required.",
				},
				examples: []string{"glesha run docs", "glesha run docs --incremental"},
			},
			options: []string{"full", "incremental", "output", "storage-class", "assume-yes", "to", "from", "compression", "compression-level", "prefix", "archive-mode", "chunked", "spool-max", "keep-archive", "passphrase-file", "catalog-passphrase-file", "memory-max", "transfer-workers", "hash-workers"},
			overrides: map[string]optionHelp{
				"output":        {usage: "keep the archive at FILE, which must not exist"},
				"storage-class": {usage: "AWS storage class of this snapshot"},
				"to":            {usage: "upload to REMOTE (default: the set's remotes)"},
			},
		},
		{
			name:          "retry",
			run:           retry,
			recordsResult: true,
			help: commandHelp{
				synopsis: []string{"SET"},
				summary:  "resume the pending operation of a set",
				description: []string{
					"Resume the interrupted operation of SET. Completed archives and finished uploads are reused. If nothing is pending but registry copies are out of date, they are repaired.",
					"Use --archive FILE if the completed archive of a pending run was moved.",
				},
				examples: []string{"glesha retry docs", "glesha retry docs --archive /mnt/spare/docs.tar.xz.gpg"},
			},
			options: []string{"archive", "to", "from", "keep-archive", "catalog-passphrase-file"},
		},
		{
			name:         "list",
			run:          list,
			listsResults: true,
			help: commandHelp{
				summary: "list known backup sets",
				description: []string{
					"Print the name and ID of every set in local state. A set whose catalog has not been opened yet is shown as '(locked catalog)'; refer to it by its ID.",
					"To find sets created on another computer, run 'glesha catalog sync' first.",
				},
				examples: []string{"glesha list"},
			},
			options: []string{"remote", "from", "to", "catalog-passphrase-file"},
			overrides: map[string]optionHelp{
				"from": {usage: "remotes to search with --remote"},
			},
		},
		{
			name:         "status",
			run:          status,
			listsResults: true,
			help: commandHelp{
				synopsis: []string{"[SET]"},
				summary:  "show the state, size, and cost of backup sets",
				description: []string{
					"Show the condition, snapshot count, stored size, and estimated yearly storage cost of every set, or of SET only.",
					"Values come from local state. Use --refresh to query the remotes for archive availability; no retrieval is requested. A '-' marks a value that is unknown, not zero.",
				},
				examples: []string{"glesha status", "glesha status docs --refresh", "glesha status --format csv"},
			},
			options: []string{"refresh", "from", "to"},
			overrides: map[string]optionHelp{
				"format": {usage: "output format: plain, table, csv, or json (default: plain)"},
			},
		},
		{
			name: "cleanup",
			run:  cleanup,
			help: commandHelp{
				synopsis: []string{"SET", "--all"},
				summary:  "delete local archive copies that are stored remotely",
				description: []string{
					"Delete the archive copies kept in the local state of SET, or of every set with --all, once a remote copy has been verified. Remote objects and archives outside the state directory are never touched.",
					"Sets with pending or unresolved work are skipped. The files are listed and confirmed before deletion.",
				},
				examples: []string{"glesha cleanup docs --dry-run", "glesha cleanup --all --assume-yes"},
			},
			options: []string{"all", "dry-run", "assume-yes", "to", "from"},
		},
		{
			name:         "history",
			run:          history,
			listsResults: true,
			help: commandHelp{
				synopsis: []string{"SET"},
				summary:  "list the snapshots of a set",
				description: []string{
					"List the completed snapshots of SET, newest first, with their type, size, and remote copies.",
					"If the catalog is encrypted and not yet open on this computer, it is downloaded and the catalog passphrase is requested.",
				},
				examples: []string{"glesha history docs", "glesha history docs --refresh"},
			},
			options: []string{"refresh", "to", "from", "catalog-passphrase-file"},
		},
		{
			name:         "browse",
			run:          browse,
			listsResults: true,
			help: commandHelp{
				synopsis: []string{"SET"},
				summary:  "list the files in a snapshot",
				description: []string{
					"List the entries of a snapshot of SET, by default the latest. Without --path the roots are listed; without --recursive only the direct children of the path are listed.",
					"If the catalog is encrypted and not yet open on this computer, it is downloaded and the catalog passphrase is requested.",
				},
				examples: []string{"glesha browse docs", "glesha browse docs --path Documents --recursive", "glesha browse docs --snapshot ID"},
			},
			options: []string{"snapshot", "path", "recursive", "to", "from", "catalog-passphrase-file"},
		},
		{
			name:          "restore",
			run:           restore,
			recordsResult: true,
			help: commandHelp{
				synopsis: []string{"SET --into DIR", "SET --cancel [--into DIR]"},
				summary:  "restore a snapshot into a directory",
				description: []string{
					"Restore a snapshot of SET, by default the latest, into DIR. DIR may exist; existing files are never replaced. An incremental snapshot is restored together with the snapshots it depends on.",
					"Archives are read from local copies when possible, otherwise from the remotes in --from order. Objects in cold storage must be retrieved first, which may be charged; this is confirmed interactively or with --request-retrieval.",
					"With --cancel, unfinished restore tasks of SET are dropped, optionally only those for DIR. Restored files and downloaded data are kept, and retrievals already requested continue.",
				},
				examples: []string{"glesha restore docs --into ~/recovered", "glesha restore docs --snapshot ID --into .", "glesha restore docs --cancel"},
			},
			options: []string{"into", "snapshot", "cancel", "from", "to", "passphrase-file", "archive-passphrase-file", "catalog-passphrase-file", "request-retrieval", "retrieval", "days", "memory-max", "transfer-workers", "hash-workers"},
			overrides: map[string]optionHelp{
				"into": {usage: "restore into DIR; with --cancel, limit to DIR"},
				"from": {usage: "read from REMOTE, in order (default: set's remotes)"},
			},
		},
		{
			name:          "import",
			run:           importArchive,
			recordsResult: true,
			help: commandHelp{
				synopsis: []string{"SET --file FILE", "SET --file FILE --from REMOTE [--key KEY]", "SET --from REMOTE --key KEY"},
				summary:  "add an existing full archive to a set",
				description: []string{
					"Add an existing full archive as a snapshot of SET. SET is created if it does not exist.",
					"With --file only, the local archive is imported. With --file and --from, the archive is also associated with its uploaded copy at KEY, which is checked by size, or by content with --deep-verify; nothing is uploaded. With --from and --key only, the object is downloaded and imported.",
					"Imported roots are not bound to local directories. Use --root, or 'glesha configure SET --root', before running backups.",
				},
				examples: []string{"glesha import old-docs --file old-backup.tar.gz.gpg", "glesha import old-docs --from b2 --key backups/old-backup.tar.gz.gpg"},
			},
			options: []string{"file", "key", "root", "baseline", "date", "deep-verify", "from", "to", "passphrase-file", "catalog-passphrase-file", "request-retrieval", "retrieval", "days", "memory-max", "transfer-workers", "hash-workers"},
			overrides: map[string]optionHelp{
				"baseline": {usage: "use as the next incremental's baseline", value: "new"},
				"from":     {usage: "remotes holding the archive, tried in order"},
			},
		},
		{
			name:          "move",
			run:           move,
			recordsResult: true,
			help: commandHelp{
				synopsis: []string{"SET --to REMOTE"},
				summary:  "copy the history of a set to another remote",
				description: []string{
					"Copy every snapshot and the catalog of SET to REMOTE, verify the copies, and make REMOTE the set's destination. Copies on the old remotes are kept. Sets with chunked snapshots cannot be moved.",
					"Because --to limits bucket access, name the current remotes with --from. Objects in cold storage must be retrieved first.",
				},
				examples: []string{"glesha move docs --from aws --to b2"},
			},
			options: []string{"assume-yes", "to", "from", "catalog-passphrase-file", "request-retrieval", "retrieval", "days", "memory-max", "transfer-workers", "hash-workers"},
			overrides: map[string]optionHelp{
				"to":   {usage: "the new remote"},
				"from": {usage: "the set's current remotes"},
			},
		},
		{
			name:  "archive",
			run:   localArchive,
			setup: localSetup,
			help: commandHelp{
				synopsis: []string{"PATH..."},
				summary:  "create a standalone encrypted archive",
				description: []string{
					"Write an encrypted, compressed tar archive of PATH without registering a set. The output file must not exist.",
					"The output is an OpenPGP message holding a compressed tar archive, so gpg and tar can also read it.",
				},
				examples: []string{"glesha archive ~/Documents", "glesha archive ~/Documents --output documents.tar.xz.gpg"},
			},
			options: []string{"output", "storage-class", "compression", "compression-level", "prefix", "archive-mode", "passphrase-file", "memory-max", "transfer-workers", "hash-workers"},
			overrides: map[string]optionHelp{
				"output":        {usage: "write to FILE (default: PREFIX-TIME.tar.EXT.gpg in the current directory)"},
				"storage-class": {usage: "ignored"},
			},
		},
		{
			name:  "decrypt",
			run:   decrypt,
			setup: localSetup,
			help: commandHelp{
				synopsis: []string{"FILE"},
				summary:  "decrypt an archive to a compressed tar file",
				description: []string{
					"Decrypt the archive FILE and write the compressed tar file it contains. The output file must not exist. Use 'glesha extract' to unpack files instead.",
				},
				examples: []string{"glesha decrypt documents.tar.xz.gpg", "glesha decrypt documents.tar.xz.gpg --output documents.tar.xz"},
			},
			options: []string{"output", "passphrase-file", "memory-max"},
			overrides: map[string]optionHelp{
				"output": {usage: "write to FILE (default: NAME.tar.EXT next to the input)"},
			},
		},
		{
			name:  "extract",
			run:   extract,
			setup: localSetup,
			help: commandHelp{
				synopsis: []string{"FILE --into DIR"},
				summary:  "unpack an archive into a new directory",
				description: []string{
					"Unpack FILE into DIR, which must not exist. FILE may be an encrypted archive or a compressed tar file; the passphrase is only requested for encrypted input.",
				},
				examples: []string{"glesha extract documents.tar.xz.gpg --into ~/recovered"},
			},
			options: []string{"into", "passphrase-file", "memory-max"},
		},
		{
			name: "upload",
			run:  upload,
			help: commandHelp{
				synopsis: []string{"FILE --to REMOTE..."},
				summary:  "upload a file to remote buckets",
				description: []string{
					"Upload FILE to every remote named by --to, in parallel. No backup set is involved. An existing object is only replaced with --force.",
				},
				examples: []string{"glesha upload documents.tar.xz.gpg --to b2", "glesha upload documents.tar.xz.gpg --to aws,b2 --key backups/documents.gpg"},
			},
			options: []string{"key", "force", "storage-class", "to", "from", "memory-max", "transfer-workers", "hash-workers"},
			overrides: map[string]optionHelp{
				"key":           {usage: "object key (default: the file name)"},
				"storage-class": {usage: "AWS storage class (default: STANDARD)", group: groupGeneral},
				"to":            {usage: "upload to REMOTE"},
			},
		},
		{
			name: "download",
			run:  download,
			help: commandHelp{
				synopsis: []string{"KEY --from REMOTE..."},
				summary:  "download an object from remote buckets",
				description: []string{
					"Download the object KEY from the first remote in --from that has it. The output file must not exist.",
				},
				examples: []string{"glesha download backups/documents.gpg --from b2", "glesha download backups/documents.gpg --from b2,aws --output documents.gpg"},
			},
			options: []string{"output", "from", "to", "memory-max", "transfer-workers", "hash-workers"},
			overrides: map[string]optionHelp{
				"output": {usage: "write to FILE (default: the last component of KEY)"},
				"from":   {usage: "remotes to download from, tried in order"},
			},
		},
		{
			name: "catalog",
			run:  catalog,
			help: commandHelp{
				synopsis: []string{"ACTION [SET]"},
				summary:  "discover, open, and synchronize catalogs",
				description: []string{
					"A catalog records the snapshots and files of a set. It is kept in local state and, unless the set is local-only, encrypted on a remote. These actions manage catalogs; they never restore files.",
					"Run 'glesha help catalog ACTION' for the options of one action.",
				},
				examples: []string{"glesha catalog sync --from b2", "glesha catalog unlock ID", "glesha catalog sync docs"},
			},
			actions: []helpAction{
				{name: "sync", arguments: "[SET]", summary: "discover sets, or synchronize one catalog", options: []string{"from", "to", "all", "catalog-passphrase-file"}, description: []string{
					"Without SET, search the remotes for sets and download their catalog metadata. No passphrase is needed. --all is the same as giving no SET.",
					"With SET, merge remote changes into the local catalog and publish local changes. This writes to the catalog remote.",
				}},
				{name: "unlock", arguments: "SET", summary: "open a downloaded catalog", options: []string{"catalog-passphrase-file"}, description: []string{
					"Decrypt the downloaded catalog of SET so that history, browse, and restore can read it. Makes no cloud requests. Requires the catalog passphrase; archives stay encrypted.",
				}},
				{name: "resolve", arguments: "SET", summary: "review or settle catalog conflicts", options: []string{"from", "to", "keep", "authority", "assume-yes", "catalog-passphrase-file"}, description: []string{
					"Without options, show the metadata that differs between the local and remote catalogs of SET.",
					"With --keep, settle the differences using local or remote values. With --authority, make the catalog on REMOTE the authoritative one. Both histories are preserved.",
				}},
				{name: "pull", arguments: "SET", summary: "replace the local catalog with the remote one", options: []string{"from", "to", "catalog-passphrase-file"}, description: []string{
					"Replace the local catalog of SET with the remote catalog. The previous local catalog is kept.",
				}},
				{name: "push", arguments: "SET", summary: "publish local catalog changes", options: []string{"from", "to", "catalog-passphrase-file"}, description: []string{
					"Publish the pending local catalog changes of SET to its catalog remote.",
				}},
				{name: "reconcile", arguments: "SET", summary: "merge compatible catalog histories", options: []string{"from", "to", "catalog-passphrase-file"}, description: []string{
					"Merge the local and remote histories of SET when they do not conflict.",
				}},
				{name: "abandon", arguments: "SET", summary: "discard the local work of a pending operation", options: []string{}, description: []string{
					"Discard the local work of the pending operation of SET. The catalog and remote objects are kept; spooled chunks may be deleted.",
				}},
			},
			options: []string{"all", "keep", "authority", "assume-yes", "from", "to", "catalog-passphrase-file"},
			overrides: map[string]optionHelp{
				"all":  {usage: "same as sync without SET"},
				"from": {usage: "search or access only REMOTE"},
			},
		},
		{
			name:          "storage-class",
			run:           storageClass,
			recordsResult: true,
			help: commandHelp{
				synopsis: []string{"SET --class CLASS"},
				summary:  "change the AWS storage class of a snapshot",
				description: []string{
					"Change the storage class of the AWS objects of one snapshot of SET, by default the latest. Objects in a cold class may need to be retrieved first.",
					"CLASS is one of STANDARD, STANDARD_IA, ONEZONE_IA, INTELLIGENT_TIERING, GLACIER_IR, GLACIER, DEEP_ARCHIVE, or REDUCED_REDUNDANCY.",
				},
				examples: []string{"glesha storage-class docs --class DEEP_ARCHIVE", "glesha storage-class docs --snapshot ID --class STANDARD"},
			},
			options: []string{"class", "snapshot", "storage-class", "to", "from", "catalog-passphrase-file"},
			overrides: map[string]optionHelp{
				"storage-class": {usage: "same as --class", group: groupGeneral},
			},
		},
		{
			name: "check",
			run:  check,
			help: commandHelp{
				synopsis: []string{"--to REMOTE..."},
				summary:  "test access to remote buckets",
				description: []string{
					"For each remote, write a small object under glesha/check/, read its metadata and content back, and delete it. Each step is reported as OK, FAILED, or SKIPPED. An object that could not be deleted is named in the report.",
					"A bucket with a default Object Lock retention is not written to, and its check fails.",
				},
				examples: []string{"glesha check --to aws,b2"},
			},
			options: []string{"to", "from", "memory-max", "transfer-workers", "hash-workers"},
			overrides: map[string]optionHelp{
				"to": {usage: "test REMOTE"},
			},
		},
		{
			name:  "state",
			run:   migrateState,
			setup: configSetup,
			help: commandHelp{
				synopsis: []string{"migrate"},
				summary:  "move local state from the legacy location",
				examples: []string{"glesha state migrate", "glesha state migrate --state-dir /srv/glesha"},
			},
			actions: []helpAction{
				{name: "migrate", arguments: "", summary: "move local state from the legacy location", options: []string{}, description: []string{
					"Move local state from the legacy location, USER_CONFIG/glesha/state-v2, to the current one: the --state-dir directory, the [catalog] directory setting, or DATA/glesha/state.",
					"Archives are moved without re-encryption. A destination that already holds state is never merged into; choose an empty one with --state-dir. No cloud access is needed.",
				}},
			},
		},
		{
			name: "help",
			help: commandHelp{
				synopsis: []string{"[COMMAND [ACTION]]"},
				summary:  "show help for a command",
				description: []string{
					"Show the options and examples of COMMAND, or of one catalog or state ACTION. Without arguments, list all commands. 'glesha COMMAND --help' is equivalent.",
				},
				examples: []string{"glesha help run", "glesha help catalog unlock"},
			},
		},
		{
			name: "version",
			help: commandHelp{
				summary: "show version information",
				description: []string{
					"Print the version and the source revision of this build.",
				},
				examples: []string{"glesha version", "glesha --version"},
			},
		},
	}
}

// lookupCommand returns the command called name.
func lookupCommand(name string) (*command, bool) {
	for i := range commands {
		if commands[i].name == name {
			return &commands[i], true
		}
	}
	return nil, false
}
