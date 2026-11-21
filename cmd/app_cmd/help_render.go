package app_cmd

import (
	"flag"
	"fmt"
	"sort"
	"strings"
)

type helpAction struct {
	name, arguments, summary, flags string
	description                     []string
}

var helpActions = map[string][]helpAction{
	"catalog": {
		{"sync", "[SET]", "discover sets, or synchronize one catalog", "from to all catalog-passphrase-file", []string{
			"Without SET, search the remotes for sets and download their catalog metadata. No passphrase is needed. --all is the same as giving no SET.",
			"With SET, merge remote changes into the local catalog and publish local changes. This writes to the catalog remote.",
		}},
		{"unlock", "SET", "open a downloaded catalog", "catalog-passphrase-file", []string{
			"Decrypt the downloaded catalog of SET so that history, browse, and restore can read it. Makes no cloud requests. Requires the catalog passphrase; archives stay encrypted.",
		}},
		{"resolve", "SET", "review or settle catalog conflicts", "from to keep authority assume-yes catalog-passphrase-file", []string{
			"Without options, show the metadata that differs between the local and remote catalogs of SET.",
			"With --keep, settle the differences using local or remote values. With --authority, make the catalog on REMOTE the authoritative one. Both histories are preserved.",
		}},
		{"pull", "SET", "replace the local catalog with the remote one", "from to catalog-passphrase-file", []string{
			"Replace the local catalog of SET with the remote catalog. The previous local catalog is kept.",
		}},
		{"push", "SET", "publish local catalog changes", "from to catalog-passphrase-file", []string{
			"Publish the pending local catalog changes of SET to its catalog remote.",
		}},
		{"reconcile", "SET", "merge compatible catalog histories", "from to catalog-passphrase-file", []string{
			"Merge the local and remote histories of SET when they do not conflict.",
		}},
		{"abandon", "SET", "discard the local work of a pending operation", "", []string{
			"Discard the local work of the pending operation of SET. The catalog and remote objects are kept; spooled chunks may be deleted.",
		}},
	},
	"state": {
		{"migrate", "", "move local state from the legacy location", "", []string{
			"Move local state from the legacy location, USER_CONFIG/glesha/state-v2, to the current one: the --state-dir directory, the [catalog] directory setting, or DATA/glesha/state.",
			"Archives are moved without re-encryption. A destination that already holds state is never merged into; choose an empty one with --state-dir. No cloud access is needed.",
		}},
	},
}

var helpGroupOrder = []string{"Options", "Remote options", "Archive options", "Passphrase options", "Retrieval options", "Performance options", "Global options"}

func helpFlagGroup(command, name string) string {
	if globalFlag(name) {
		return "Global options"
	}
	switch name {
	case "from", "to", "catalog-to":
		return "Remote options"
	case "storage-class", "incremental-storage-class":
		if command == "upload" || command == "storage-class" {
			return "Options"
		}
		return "Archive options"
	case "compression", "compression-level", "prefix", "archive-mode", "chunked", "spool-max", "keep-archive":
		return "Archive options"
	case "passphrase-file", "catalog-passphrase-file", "archive-passphrase-file":
		return "Passphrase options"
	case "request-retrieval", "retrieval", "days":
		return "Retrieval options"
	case "memory-max", "hash-workers", "transfer-workers", "jobs", "j":
		return "Performance options"
	default:
		return "Options"
	}
}

// helpFlagLabel returns the flag spelling shown in --help.
func helpFlagLabel(command string, f *flag.Flag) string {
	label := "--" + f.Name
	switch f.Name {
	case "log-level":
		label = "-L, --log-level"
	case "help":
		label = "-h, --help"
	case "version":
		label = "-v, --version"
	case "transfer-workers":
		label = "-j, --jobs, --transfer-workers"
	}
	if value := flagValueName(command, f); value != "" {
		label += " " + value
	}
	return label
}

// lowercase, no trailing period
var flagDescriptions = map[string]string{
	"to":                        "limit bucket access to REMOTE",
	"from":                      "limit bucket access to REMOTE",
	"catalog-to":                "where to keep the catalog (default: the first --to remote)",
	"compression":               "compression: xz, gzip, or bzip2 (default: xz)",
	"compression-level":         "compression level, 1 to 9 (default: 6)",
	"prefix":                    "name prefix: glesha or encb (default: glesha)",
	"archive-mode":              "buffering: auto, memory, or stream (default: auto)",
	"storage-class":             "AWS storage class of snapshots",
	"incremental-storage-class": "AWS storage class of incremental snapshots (default: --storage-class)",
	"class":                     "new AWS storage class",
	"chunked":                   "upload in chunks using bounded local space",
	"spool-max":                 "local space for chunks (default: 128MiB, min: 2MiB)",
	"keep-archive":              "keep the local archive after a successful upload",
	"output":                    "write the archive to FILE, which must not exist",
	"into":                      "unpack into DIR, which must not exist",
	"root":                      "bind root NAME to directory PATH; repeatable",
	"baseline":                  "use snapshot ID as the next incremental's baseline",
	"file":                      "local archive to import",
	"key":                       "object key (default with --file: the file name)",
	"date":                      "original backup time, in RFC 3339 format",
	"deep-verify":               "compare content with the uploaded copy, not size",
	"force":                     "replace an existing object",
	"archive":                   "use the completed archive moved to FILE",
	"incremental":               "store only the changes since the baseline",
	"full":                      "create a full snapshot (default)",
	"assume-yes":                "answer yes to all confirmations",
	"retrieval":                 "tier: bulk, standard, or expedited (default: bulk)",
	"days":                      "keep retrieved copies for N days (default: 3)",
	"request-retrieval":         "allow charged cold retrieval without asking",
	"snapshot":                  "snapshot ID, or 'latest' (default: latest)",
	"path":                      "list entries under PATH (default: the roots)",
	"recursive":                 "include all descendants",
	"refresh":                   "query the remotes for archive availability",
	"all":                       "process every set",
	"keep":                      "settle conflicts using local or remote values",
	"authority":                 "make the catalog on REMOTE authoritative",
	"dry-run":                   "list the files without deleting them",
	"remote":                    "search --from remotes (deprecated: use catalog sync)",
	"cancel":                    "cancel unfinished restore tasks",
	"passphrase-file":           "read the archive passphrase from FILE ('-': stdin)",
	"catalog-passphrase-file":   "read the catalog passphrase from FILE ('-': stdin)",
	"archive-passphrase-file":   "read the passphrase of snapshot ID from FILE",
	"memory-max":                "memory limit (default: half of available memory, or 128MiB)",
	"hash-workers":              "hash up to N files at once (default: 4)",
	"transfer-workers":          "run up to N transfers at once (default: 4)",
	"config":                    "read settings from FILE",
	"state-dir":                 "keep local state in DIR",
	"env":                       "load credentials from FILE",
	"format":                    "output format: plain or json (default: plain)",
	"progress":                  "progress: auto, plain, or off (default: auto)",
	"color":                     "color: auto, yes, or no (default: auto)",
	"log-level":                 "debug, info, warn, error, or silent (default: info)",
	"help":                      "show this help",
	"version":                   "show version information",
}

var commandFlagDescriptions = map[string]map[string]string{
	"create": {
		"to": "upload snapshots to REMOTE",
	},
	"configure": {
		"to":                "upload future snapshots to REMOTE",
		"catalog-to":        "where to keep the catalog",
		"compression":       "compression: xz, gzip, or bzip2",
		"compression-level": "compression level, 1 to 9",
		"prefix":            "name prefix: glesha or encb",
		"chunked":           "upload in chunks; --chunked=false turns it off",
		"root":              "bind root NAME to directory PATH; repeatable",
	},
	"run": {
		"to":            "upload to REMOTE (default: the set's remotes)",
		"storage-class": "AWS storage class of this snapshot",
		"chunked":       "upload in chunks using bounded local space",
		"output":        "keep the archive at FILE, which must not exist",
	},
	"retry": {
		"keep-archive": "keep the local archive after a successful upload",
	},
	"list": {
		"from": "remotes to search with --remote",
	},
	"status": {
		"format": "output format: plain, table, csv, or json (default: plain)",
	},
	"restore": {
		"from": "read from REMOTE, in order (default: set's remotes)",
		"into": "restore into DIR; with --cancel, limit to DIR",
	},
	"import": {
		"from":     "remotes holding the archive, tried in order",
		"baseline": "use as the next incremental's baseline",
	},
	"move": {
		"to":   "the new remote",
		"from": "the set's current remotes",
	},
	"archive": {
		"output":        "write to FILE (default: PREFIX-TIME.tar.EXT.gpg in the current directory)",
		"storage-class": "ignored",
	},
	"decrypt": {
		"output": "write to FILE (default: NAME.tar.EXT next to the input)",
	},
	"upload": {
		"to":            "upload to REMOTE",
		"key":           "object key (default: the file name)",
		"storage-class": "AWS storage class (default: STANDARD)",
	},
	"download": {
		"from":   "remotes to download from, tried in order",
		"output": "write to FILE (default: the last component of KEY)",
	},
	"catalog": {
		"from": "search or access only REMOTE",
		"all":  "same as sync without SET",
	},
	"cleanup": {
		"all": "process every set",
	},
	"storage-class": {
		"storage-class": "same as --class",
	},
	"check": {
		"to": "test REMOTE",
	},
}

func helpFlagDescription(command string, f *flag.Flag) string {
	if description, ok := commandFlagDescriptions[command][f.Name]; ok {
		return description
	}
	if description, ok := flagDescriptions[f.Name]; ok {
		return description
	}
	return f.Usage
}

// helpFlags returns the documented flags of a command or action, in display order.
func helpFlags(command string, selected *helpAction) []*flag.Flag {
	var ordered []*flag.Flag
	flagSet(command, &AppCmdEnv{}).VisitAll(func(f *flag.Flag) {
		switch f.Name {
		case "L", "h", "v", "j", "jobs":
			return
		}
		if selected != nil && !globalFlag(f.Name) && !strings.Contains(" "+selected.flags+" ", " "+f.Name+" ") {
			return
		}
		ordered = append(ordered, f)
	})
	sort.SliceStable(ordered, func(i, j int) bool {
		return helpFlagRank(command, ordered[i].Name) < helpFlagRank(command, ordered[j].Name)
	})
	return ordered
}

const (
	helpWidth  = 79
	helpColumn = 28
)

func helpEntry(label, description string) string {
	first := "  " + label
	if len(first)+2 > helpColumn {
		return first + "\n" + wrapHelp(description, strings.Repeat(" ", helpColumn), helpWidth)
	}
	first += strings.Repeat(" ", helpColumn-len(first))
	return wrapHanging(description, first, strings.Repeat(" ", helpColumn), helpWidth)
}

func selectedAction(command, action string) *helpAction {
	for i := range helpActions[command] {
		if helpActions[command][i].name == action {
			return &helpActions[command][i]
		}
	}
	return nil
}

func CommandActionUsage(command, action string) string {
	h := commandHelps[command]
	synopsis, description, examples := h.synopsis, h.description, h.examples
	name := command
	if command == "state" && action == "" {
		action = "migrate"
	}
	selected := selectedAction(command, action)
	if selected != nil {
		name += " " + action
		synopsis = []string{selected.arguments}
		description = selected.description
		examples = nil
		for _, example := range h.examples {
			if strings.HasPrefix(example, "glesha "+name) {
				examples = append(examples, example)
			}
		}
		if len(examples) == 0 {
			example := "glesha " + name
			if selected.arguments != "" {
				example += " " + strings.Trim(selected.arguments, "[]")
			}
			examples = []string{example}
		}
	}
	if len(synopsis) == 0 {
		synopsis = []string{""}
	}
	var out strings.Builder
	for i, line := range synopsis {
		prefix := "usage: "
		if i > 0 {
			prefix = "       "
		}
		out.WriteString(wrapHanging(strings.TrimSpace("glesha "+name+" "+line)+" [options]", prefix, "         ", helpWidth) + "\n")
	}
	for _, paragraph := range description {
		out.WriteString("\n" + wrapHelp(paragraph, "", helpWidth) + "\n")
	}
	if selected == nil && len(helpActions[command]) > 0 {
		out.WriteString("\nActions:\n")
		for _, entry := range helpActions[command] {
			out.WriteString(helpEntry(strings.TrimSpace(entry.name+" "+entry.arguments), entry.summary) + "\n")
		}
	}
	flags := helpFlags(command, selected)
	for _, group := range helpGroupOrder {
		var lines strings.Builder
		for _, f := range flags {
			if helpFlagGroup(command, f.Name) == group {
				lines.WriteString(helpEntry(helpFlagLabel(command, f), helpFlagDescription(command, f)) + "\n")
			}
		}
		if lines.Len() == 0 {
			continue
		}
		fmt.Fprintf(&out, "\n%s:\n%s", group, lines.String())
		if group == "Remote options" {
			out.WriteString("\n" + wrapHelp(remoteSelectionNote, "  ", helpWidth) + "\n")
		}
	}
	out.WriteString("\nExamples:\n")
	for _, example := range examples {
		out.WriteString(wrapExample(example) + "\n")
	}
	return out.String()
}

const remoteSelectionNote = "REMOTE... takes a comma-separated list and may be repeated. If --from or --to is given, no other remote is accessed."

func knownHelpAction(command, action string) bool {
	return selectedAction(command, action) != nil
}

func helpFlagRank(command, name string) int {
	order := []string{"class", "file", "key", "into", "snapshot", "full", "incremental", "root", "baseline", "path", "recursive", "refresh", "all", "cancel", "dry-run", "keep", "authority", "archive", "date", "deep-verify", "output", "force", "storage-class", "remote", "assume-yes", "to", "from", "catalog-to", "compression", "compression-level", "prefix", "archive-mode", "incremental-storage-class", "chunked", "spool-max", "keep-archive", "passphrase-file", "archive-passphrase-file", "catalog-passphrase-file", "request-retrieval", "retrieval", "days", "memory-max", "transfer-workers", "hash-workers", "config", "state-dir", "env", "format", "progress", "color", "log-level", "help", "version"}
	if strings.Contains(" restore import download catalog list status ", " "+command+" ") {
		switch name {
		case "from":
			name = "to"
		case "to":
			name = "from"
		}
	}
	for i, candidate := range order {
		if candidate == name {
			return i
		}
	}
	return len(order)
}
