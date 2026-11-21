package app_cmd

import (
	"flag"
	"fmt"
	"strings"
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

var commandHelps = map[string]commandHelp{
	"create": {
		synopsis: []string{"SET PATH..."},
		summary:  "register a new backup set",
		description: []string{
			"Register a backup set named SET whose sources are the directories PATH. Each PATH becomes a root named after its last path component. No data is archived; use 'glesha run SET' to create the first snapshot.",
			"Remotes given with --to become the set's destinations. The catalog is stored on the first of them unless --catalog-to says otherwise. A set without --to gets its destinations from the first run.",
		},
		examples: []string{"glesha create docs ~/Documents --to b2", "glesha create photos ~/Pictures --to aws --storage-class DEEP_ARCHIVE"},
	},
	"configure": {
		synopsis: []string{"SET"},
		summary:  "change the settings of a backup set",
		description: []string{
			"Change the saved settings of SET. Only the given options change; new values apply to later runs. Existing snapshots are not rewritten.",
			"Use --root NAME=PATH to bind an unbound root, such as one from an imported or discovered set, to a directory on this computer. A bound root cannot be rebound.",
			"The destinations of an incremental set and a catalog that was already published can only be changed with 'glesha move'. Finish pending work before configuring.",
		},
		examples: []string{"glesha configure docs --compression gzip", "glesha configure docs --root Documents=~/Documents"},
	},
	"run": {
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
	"retry": {
		synopsis: []string{"SET"},
		summary:  "resume the pending operation of a set",
		description: []string{
			"Resume the interrupted operation of SET. Completed archives and finished uploads are reused. If nothing is pending but registry copies are out of date, they are repaired.",
			"Use --archive FILE if the completed archive of a pending run was moved.",
		},
		examples: []string{"glesha retry docs", "glesha retry docs --archive /mnt/spare/docs.tar.xz.gpg"},
	},
	"list": {
		summary: "list known backup sets",
		description: []string{
			"Print the name and ID of every set in local state. A set whose catalog has not been opened yet is shown as '(locked catalog)'; refer to it by its ID.",
			"To find sets created on another computer, run 'glesha catalog sync' first.",
		},
		examples: []string{"glesha list"},
	},
	"status": {
		synopsis: []string{"[SET]"},
		summary:  "show the state, size, and cost of backup sets",
		description: []string{
			"Show the condition, snapshot count, stored size, and estimated yearly storage cost of every set, or of SET only.",
			"Values come from local state. Use --refresh to query the remotes for archive availability; no retrieval is requested. A '-' marks a value that is unknown, not zero.",
		},
		examples: []string{"glesha status", "glesha status docs --refresh", "glesha status --format csv"},
	},
	"history": {
		synopsis: []string{"SET"},
		summary:  "list the snapshots of a set",
		description: []string{
			"List the completed snapshots of SET, newest first, with their type, size, and remote copies.",
			"If the catalog is encrypted and not yet open on this computer, it is downloaded and the catalog passphrase is requested.",
		},
		examples: []string{"glesha history docs", "glesha history docs --refresh"},
	},
	"browse": {
		synopsis: []string{"SET"},
		summary:  "list the files in a snapshot",
		description: []string{
			"List the entries of a snapshot of SET, by default the latest. Without --path the roots are listed; without --recursive only the direct children of the path are listed.",
			"If the catalog is encrypted and not yet open on this computer, it is downloaded and the catalog passphrase is requested.",
		},
		examples: []string{"glesha browse docs", "glesha browse docs --path Documents --recursive", "glesha browse docs --snapshot ID"},
	},
	"restore": {
		synopsis: []string{"SET --into DIR", "SET --cancel [--into DIR]"},
		summary:  "restore a snapshot into a directory",
		description: []string{
			"Restore a snapshot of SET, by default the latest, into DIR. DIR may exist; existing files are never replaced. An incremental snapshot is restored together with the snapshots it depends on.",
			"Archives are read from local copies when possible, otherwise from the remotes in --from order. Objects in cold storage must be retrieved first, which may be charged; this is confirmed interactively or with --request-retrieval.",
			"With --cancel, unfinished restore tasks of SET are dropped, optionally only those for DIR. Restored files and downloaded data are kept, and retrievals already requested continue.",
		},
		examples: []string{"glesha restore docs --into ~/recovered", "glesha restore docs --snapshot ID --into .", "glesha restore docs --cancel"},
	},
	"import": {
		synopsis: []string{"SET --file FILE", "SET --file FILE --from REMOTE [--key KEY]", "SET --from REMOTE --key KEY"},
		summary:  "add an existing full archive to a set",
		description: []string{
			"Add an existing full archive as a snapshot of SET. SET is created if it does not exist.",
			"With --file only, the local archive is imported. With --file and --from, the archive is also associated with its uploaded copy at KEY, which is checked by size, or by content with --deep-verify; nothing is uploaded. With --from and --key only, the object is downloaded and imported.",
			"Imported roots are not bound to local directories. Use --root, or 'glesha configure SET --root', before running backups.",
		},
		examples: []string{"glesha import old-docs --file old-backup.tar.gz.gpg", "glesha import old-docs --from b2 --key backups/old-backup.tar.gz.gpg"},
	},
	"move": {
		synopsis: []string{"SET --to REMOTE"},
		summary:  "copy the history of a set to another remote",
		description: []string{
			"Copy every snapshot and the catalog of SET to REMOTE, verify the copies, and make REMOTE the set's destination. Copies on the old remotes are kept. Sets with chunked snapshots cannot be moved.",
			"Because --to limits bucket access, name the current remotes with --from. Objects in cold storage must be retrieved first.",
		},
		examples: []string{"glesha move docs --from aws --to b2"},
	},
	"archive": {
		synopsis: []string{"PATH..."},
		summary:  "create a standalone encrypted archive",
		description: []string{
			"Write an encrypted, compressed tar archive of PATH without registering a set. The output file must not exist.",
			"The output is an OpenPGP message holding a compressed tar archive, so gpg and tar can also read it.",
		},
		examples: []string{"glesha archive ~/Documents", "glesha archive ~/Documents --output documents.tar.xz.gpg"},
	},
	"decrypt": {
		synopsis: []string{"FILE"},
		summary:  "decrypt an archive to a compressed tar file",
		description: []string{
			"Decrypt the archive FILE and write the compressed tar file it contains. The output file must not exist. Use 'glesha extract' to unpack files instead.",
		},
		examples: []string{"glesha decrypt documents.tar.xz.gpg", "glesha decrypt documents.tar.xz.gpg --output documents.tar.xz"},
	},
	"extract": {
		synopsis: []string{"FILE --into DIR"},
		summary:  "unpack an archive into a new directory",
		description: []string{
			"Unpack FILE into DIR, which must not exist. FILE may be an encrypted archive or a compressed tar file; the passphrase is only requested for encrypted input.",
		},
		examples: []string{"glesha extract documents.tar.xz.gpg --into ~/recovered"},
	},
	"upload": {
		synopsis: []string{"FILE --to REMOTE..."},
		summary:  "upload a file to remote buckets",
		description: []string{
			"Upload FILE to every remote named by --to, in parallel. No backup set is involved. An existing object is only replaced with --force.",
		},
		examples: []string{"glesha upload documents.tar.xz.gpg --to b2", "glesha upload documents.tar.xz.gpg --to aws,b2 --key backups/documents.gpg"},
	},
	"download": {
		synopsis: []string{"KEY --from REMOTE..."},
		summary:  "download an object from remote buckets",
		description: []string{
			"Download the object KEY from the first remote in --from that has it. The output file must not exist.",
		},
		examples: []string{"glesha download backups/documents.gpg --from b2", "glesha download backups/documents.gpg --from b2,aws --output documents.gpg"},
	},
	"catalog": {
		synopsis: []string{"ACTION [SET]"},
		summary:  "discover, open, and synchronize catalogs",
		description: []string{
			"A catalog records the snapshots and files of a set. It is kept in local state and, unless the set is local-only, encrypted on a remote. These actions manage catalogs; they never restore files.",
			"Run 'glesha help catalog ACTION' for the options of one action.",
		},
		examples: []string{"glesha catalog sync --from b2", "glesha catalog unlock ID", "glesha catalog sync docs"},
	},
	"cleanup": {
		synopsis: []string{"SET", "--all"},
		summary:  "delete local archive copies that are stored remotely",
		description: []string{
			"Delete the archive copies kept in the local state of SET, or of every set with --all, once a remote copy has been verified. Remote objects and archives outside the state directory are never touched.",
			"Sets with pending or unresolved work are skipped. The files are listed and confirmed before deletion.",
		},
		examples: []string{"glesha cleanup docs --dry-run", "glesha cleanup --all --assume-yes"},
	},
	"state": {
		synopsis: []string{"migrate"},
		summary:  "move local state from the legacy location",
		examples: []string{"glesha state migrate", "glesha state migrate --state-dir /srv/glesha"},
	},
	"storage-class": {
		synopsis: []string{"SET --class CLASS"},
		summary:  "change the AWS storage class of a snapshot",
		description: []string{
			"Change the storage class of the AWS objects of one snapshot of SET, by default the latest. Objects in a cold class may need to be retrieved first.",
			"CLASS is one of STANDARD, STANDARD_IA, ONEZONE_IA, INTELLIGENT_TIERING, GLACIER_IR, GLACIER, DEEP_ARCHIVE, or REDUCED_REDUNDANCY.",
		},
		examples: []string{"glesha storage-class docs --class DEEP_ARCHIVE", "glesha storage-class docs --snapshot ID --class STANDARD"},
	},
	"check": {
		synopsis: []string{"--to REMOTE..."},
		summary:  "test access to remote buckets",
		description: []string{
			"For each remote, write a small object under glesha/check/, read its metadata and content back, and delete it. Each step is reported as OK, FAILED, or SKIPPED. An object that could not be deleted is named in the report.",
		},
		examples: []string{"glesha check --to aws,b2"},
	},
	"help": {
		synopsis: []string{"[COMMAND [ACTION]]"},
		summary:  "show help for a command",
		description: []string{
			"Show the options and examples of COMMAND, or of one catalog or state ACTION. Without arguments, list all commands. 'glesha COMMAND --help' is equivalent.",
		},
		examples: []string{"glesha help run", "glesha help catalog unlock"},
	},
	"version": {
		summary:     "show version information",
		description: []string{"Print the version and the source revision of this build."},
		examples:    []string{"glesha version", "glesha --version"},
	},
}

// commandOrder groups commands for the overview.
var commandOrder = []struct {
	title    string
	commands []string
}{
	{"Backup sets", []string{"create", "configure", "run", "retry", "restore", "import", "move", "storage-class", "cleanup"}},
	{"Inspection", []string{"list", "status", "history", "browse"}},
	{"Catalogs", []string{"catalog sync", "catalog unlock", "catalog resolve", "catalog ACTION"}},
	{"Standalone files", []string{"archive", "decrypt", "extract", "upload", "download", "check"}},
	{"Other", []string{"state migrate", "help", "version"}},
}

var overviewEntries = map[string][2]string{
	"create":          {"create SET PATH...", "register a new backup set"},
	"configure":       {"configure SET", "change the settings of a set"},
	"run":             {"run SET", "create a snapshot and upload it"},
	"retry":           {"retry SET", "resume interrupted work"},
	"restore":         {"restore SET", "restore a snapshot into a directory"},
	"import":          {"import SET", "add an existing archive to a set"},
	"move":            {"move SET", "copy a set's history to another remote"},
	"storage-class":   {"storage-class SET", "change the AWS storage class of a snapshot"},
	"cleanup":         {"cleanup SET|--all", "delete local copies already stored remotely"},
	"list":            {"list", "list known sets"},
	"status":          {"status [SET]", "show state, size, and estimated cost"},
	"history":         {"history SET", "list the snapshots of a set"},
	"browse":          {"browse SET", "list the files in a snapshot"},
	"catalog sync":    {"catalog sync [SET]", "discover sets, or synchronize one catalog"},
	"catalog unlock":  {"catalog unlock SET", "open a downloaded catalog"},
	"catalog resolve": {"catalog resolve SET", "review or settle catalog conflicts"},
	"catalog ACTION":  {"catalog ACTION SET", "pull, push, reconcile, or abandon"},
	"archive":         {"archive PATH...", "create an encrypted archive"},
	"decrypt":         {"decrypt FILE", "decrypt an archive to a compressed tar"},
	"extract":         {"extract FILE", "unpack an archive into a new directory"},
	"upload":          {"upload FILE", "upload a file to a bucket"},
	"download":        {"download KEY", "download an object from a bucket"},
	"check":           {"check", "test access to a bucket"},
	"state migrate":   {"state migrate", "move state from the legacy location"},
	"help":            {"help [COMMAND [ACTION]]", "show help for a command"},
	"version":         {"version", "show version information"},
}

func showHelp(args []string) error {
	text, err := helpText(args)
	if err != nil {
		return err
	}
	_, err = fmt.Print(text)
	return err
}
func helpText(args []string) (string, error) {
	if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return Usage(), nil
	}
	if _, ok := commandHelps[args[0]]; !ok {
		return "", fmt.Errorf("cli: unknown help command %q", args[0])
	}
	if len(args) > 1 {
		if len(args) > 2 || !knownHelpAction(args[0], args[1]) {
			return "", fmt.Errorf("cli: help accepts one command or a catalog/state operation")
		}
		return CommandActionUsage(args[0], args[1]), nil
	}
	return CommandUsage(args[0]), nil
}

func globalFlag(name string) bool {
	switch name {
	case "config", "state-dir", "env", "log-level", "L", "color", "format", "progress", "help", "h", "version", "v":
		return true
	}
	return false
}

// flag value placeholders; per-command entries take precedence
var flagValueNames = map[string]string{
	"format": "FORMAT", "keep": "local|remote",
	"config": "FILE", "state-dir": "DIR", "env": "FILE", "log-level": "LEVEL", "color": "WHEN", "progress": "MODE",
	"to": "REMOTE...", "from": "REMOTE...", "catalog-to": "REMOTE|local", "authority": "REMOTE",
	"compression": "FORMAT", "compression-level": "N", "prefix": "PREFIX",
	"spool-max": "SIZE", "archive-mode": "MODE", "storage-class": "CLASS", "class": "CLASS",
	"incremental-storage-class": "CLASS", "output": "FILE", "into": "DIR",
	"file": "FILE", "key": "KEY", "date": "DATE", "baseline": "ID",
	"root": "NAME=PATH", "archive": "FILE", "retrieval": "TIER", "days": "N",
	"snapshot": "ID", "path": "PATH", "passphrase-file": "FILE",
	"catalog-passphrase-file": "FILE", "archive-passphrase-file": "ID=FILE",
	"memory-max": "SIZE", "hash-workers": "N", "transfer-workers": "N", "jobs": "N",
}

var commandFlagValueNames = map[string]map[string]string{
	"import": {"baseline": "new"},
}

func flagValueName(command string, f *flag.Flag) string {
	if value, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && value.IsBoolFlag() {
		return ""
	}
	if name := commandFlagValueNames[command][f.Name]; name != "" {
		return name
	}
	if name := flagValueNames[f.Name]; name != "" {
		return name
	}
	return "VALUE"
}
func wrapHelp(text, indent string, width int) string {
	return wrapHanging(text, indent, indent, width)
}

// wrapHanging wraps text; the first line starts with first, later lines with rest.
func wrapHanging(text, first, rest string, width int) string {
	var lines []string
	line, prefix := first, first
	for _, word := range strings.Fields(text) {
		if len(line) > len(prefix) && len(line)+1+len(word) > width {
			lines = append(lines, line)
			line, prefix = rest, rest
		}
		if len(line) > len(prefix) {
			line += " "
		}
		line += word
	}
	if len(line) > len(prefix) {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
func wrapExample(text string) string {
	return strings.ReplaceAll(wrapHelp(text, "  ", 76), "\n", " \\\n  ")
}
