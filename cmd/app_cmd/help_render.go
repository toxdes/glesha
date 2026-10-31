package app_cmd

import (
	"flag"
	"fmt"
	"sort"
	"strings"
)

type helpAction struct {
	name, arguments, description, flags string
	notes                               []string
}

var helpActions = map[string][]helpAction{
	"catalog": {
		{"sync", "[SET]", "Discover catalogs, or synchronize one set.", "from to all json catalog-passphrase-file", []string{"Without SET, download catalog metadata without passphrase prompts. With SET, open the catalog, merge changes, and publish metadata. Sync can write to the selected buckets."}},
		{"unlock", "SET", "Open a downloaded catalog locally.", "json catalog-passphrase-file", []string{"Requires the catalog passphrase. Uses cached metadata and makes no cloud requests. Archive files stay encrypted."}},
		{"resolve", "SET", "Review or resolve conflicting catalog metadata.", "from to json keep authority assume-yes catalog-passphrase-file", []string{"Omit --keep to review differences. Choose --keep local|remote to resolve references, or --authority REMOTE to select a catalog location. Original histories are preserved."}},
		{"pull", "SET", "Pull remote metadata into the local catalog.", "from to json catalog-passphrase-file", nil},
		{"push", "SET", "Publish pending catalog changes.", "from to json catalog-passphrase-file", nil},
		{"reconcile", "SET", "Merge compatible catalog histories.", "from to json catalog-passphrase-file", nil},
		{"abandon", "SET", "Discard a pending operation's local work.", "", []string{"Keeps the current catalog and remote objects. Pending chunk spool files may be removed."}},
	},
	"state": {{"migrate", "", "Move legacy local state to the current location.", "", []string{"No cloud access is required. Archive files move without re-encryption. Empty legacy registries need no migration. Existing destinations are not merged; use --state-dir to select a fresh destination."}}},
}

var commandNotes = map[string][]string{
	"create":        {"Registers sources and publishes initial metadata. Run glesha run SET to create the first archive."},
	"configure":     {"Use --root NAME=PATH to bind sources on this computer. Other changes update the set's saved settings."},
	"run":           {"Creates a full backup by default. --incremental requires an existing baseline. Repeating a pending run resumes it with the original archive passphrase.", "Chunked runs use --spool-max and do not accept --output or --keep-archive."},
	"retry":         {"Resumes pending work or repairs registry copies. Use --archive if a pending archive was moved."},
	"list":          {"Reads local inventory. Run catalog sync to discover sets on another computer; status shows their condition and storage details."},
	"status":        {"Reads cached state by default. --refresh checks remote archive availability. '-' means unavailable, not zero. Locked catalogs need metadata or a catalog passphrase."},
	"history":       {"Reads the local catalog. --refresh checks live archive availability. Open an encrypted catalog first with catalog unlock SET."},
	"browse":        {"Lists entries in the latest snapshot by default. Open an encrypted catalog first with catalog unlock SET."},
	"restore":       {"Restores into a new or existing directory without replacing files. --into . is supported. Cold archives may require retrieval consent.", "Use --cancel to clear unfinished local restore tasks, optionally selecting --into DIR. Restored files and staging data are preserved. Cloud retrieval requests already submitted continue."},
	"import":        {"Registers an existing full archive. Associating an uploaded archive does not upload it again. Use configure --root to bind local sources afterward."},
	"move":          {"Copies complete history and switches future destinations. Source copies are retained. Select every source and catalog bucket with --from."},
	"archive":       {"Creates a standalone encrypted archive. Compression defaults to xz; --output chooses its filename."},
	"decrypt":       {"Produces compressed tar. Use extract to unpack files. Existing output files are never overwritten."},
	"extract":       {"Accepts encrypted archives or plaintext compressed tar. The destination must not exist."},
	"upload":        {"Uploads bytes without creating a backup set. Replacing an object requires --force."},
	"download":      {"Tries sources in the specified order. The output file must not exist."},
	"catalog":       {"sync fetches or synchronizes metadata. unlock opens a cached catalog using its catalog passphrase. resolve handles conflicting metadata. These actions do not restore archive contents."},
	"cleanup":       {"Checks remote copies before removing local archives. Review --dry-run first. Remote backups are retained."},
	"storage-class": {"Changes the AWS storage class of one snapshot, latest by default. Cold source objects may need retrieval first."},
	"check":         {"Writes a temporary test object, verifies it, then deletes it. Failures include the affected step and any object left behind."},
}

var helpGroupOrder = []string{"Options", "Buckets", "Archive settings", "Passphrases", "Cloud retrieval", "Performance", "Confirmations", "Output", "Compatibility", "Global options"}

func helpFlagGroup(name string) string {
	if globalFlag(name) {
		return "Global options"
	}
	switch name {
	case "from", "to", "catalog-to", "prefix":
		return "Buckets"
	case "compression", "compression-level", "archive-mode", "chunked", "spool-max", "keep-archive":
		return "Archive settings"
	case "passphrase-file", "catalog-passphrase-file", "archive-passphrase-file":
		return "Passphrases"
	case "request-retrieval", "retrieval", "days", "storage-class", "incremental-storage-class":
		return "Cloud retrieval"
	case "memory-max", "hash-workers", "transfer-workers", "jobs", "j":
		return "Performance"
	case "assume-yes", "force":
		return "Confirmations"
	case "format", "json":
		return "Output"
	case "remote":
		return "Compatibility"
	default:
		return "Options"
	}
}

func helpFlagLabel(f *flag.Flag) string {
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
	if value := flagValueName(f); value != "" {
		label += " " + value
	}
	return label
}

func helpFlagDescription(command string, f *flag.Flag) string {
	description := map[string]string{
		"from":                    "Source or permitted remotes; comma-separated or repeated.",
		"to":                      "Destination or permitted remotes; comma-separated or repeated.",
		"catalog-to":              "Catalog location: a remote name or local.",
		"passphrase-file":         "Archive passphrase file; '-' reads stdin.",
		"catalog-passphrase-file": "Catalog passphrase file; '-' reads stdin.",
		"archive-passphrase-file": "Archive passphrase override: SNAPSHOT=FILE.",
		"state-dir":               "Local state directory; overrides catalog.directory.",
		"config":                  "TOML settings file.",
		"env":                     "Load credentials; existing process variables take priority.",
		"log-level":               "Diagnostic verbosity: debug, info, warn, error, or silent.",
		"help":                    "Show this command's help.",
		"version":                 "Show version and build revision.",
		"color":                   "Color mode: auto, yes, or no.",
		"json":                    "Versioned JSON records, one per line.",
		"format":                  "Result format: plain or json; status also supports table and csv.",
		"progress":                "Progress on stderr: auto, plain, or off.",
		"memory-max":              "Memory budget, e.g. 128MiB.",
		"transfer-workers":        "Maximum parallel transfers; memory may lower this limit.",
		"hash-workers":            "Maximum parallel file hashing workers.",
		"remote":                  "Deprecated. Use catalog sync, then list.",
		"deep-verify":             "Download associated remote bytes and verify them.",
		"request-retrieval":       "Allow retrieval charges for cold cloud objects.",
		"prefix":                  "Prefix for new archive names: glesha or encb.",
		"full":                    "Create a full snapshot.",
		"incremental":             "Create changes relative to an existing baseline.",
		"compression":             "Archive compression: xz, gzip, or bzip2.",
		"archive-mode":            "Archive memory mode: auto, memory, or stream.",
		"recursive":               "Include entries below the selected path.",
		"cancel":                  "Cancel local restore tasks; preserve restored and staged files.",
		"assume-yes":              "Skip confirmation prompts for this command.",
	}[f.Name]
	if f.Name == "to" {
		description = "Additional buckets this command may access."
		if strings.Contains(" create configure run upload move ", " "+command+" ") {
			description = "Destination remotes; comma-separated or repeated."
		}
		if command == "check" {
			description = "Buckets to test; comma-separated or repeated."
		}
	}
	if f.Name == "from" {
		description = "Source or catalog buckets this command may access."
		if strings.Contains(" restore import download ", " "+command+" ") {
			description = "Archive source remotes, in fallback order."
		}
		if command == "catalog" {
			description = "Buckets to discover or access; comma-separated or repeated."
		}
	}
	if f.Name == "all" && command == "catalog" {
		description = "Alias for bare sync; discover only the allowed buckets."
	}
	if description == "" {
		description = f.Usage
		if description != "" {
			description = strings.ToUpper(description[:1]) + description[1:]
		}
	}
	if f.Name == "into" && command == "restore" {
		description = "Restore destination; with --cancel, select a task destination."
	}
	description = strings.TrimSuffix(description, ".")
	if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
		description += " (default: " + f.DefValue + ")"
	}
	return description + "."
}

func CommandActionUsage(command, action string) string {
	arguments, description := commandArguments[command], commandDescriptions[command]
	notes := commandNotes[command]
	var selected *helpAction
	for i := range helpActions[command] {
		candidate := &helpActions[command][i]
		if candidate.name == action {
			selected = candidate
			break
		}
	}
	name := command
	if selected != nil {
		name += " " + action
		arguments = selected.arguments
		description = selected.description
		notes = selected.notes
	}
	var out strings.Builder
	out.WriteString("Usage:\n")
	out.WriteString(wrapHelp(strings.TrimSpace("glesha "+name+" "+arguments)+" [options]", "  ", 78) + "\n\n")
	out.WriteString(wrapHelp(description, "", 78) + "\n")
	for _, note := range notes {
		out.WriteString("\n" + wrapHelp(note, "", 78) + "\n")
	}
	if selected == nil && len(helpActions[command]) > 0 {
		out.WriteString("\nActions:\n")
		for _, entry := range helpActions[command] {
			fmt.Fprintf(&out, "  %-24s %s\n", strings.TrimSpace(entry.name+" "+entry.arguments), entry.description)
		}
	}
	flags := flagSet(command, &AppCmdEnv{})
	if flags.Lookup("from") != nil && (selected == nil || strings.Contains(selected.flags, "from")) {
		out.WriteString("\n" + wrapHelp("With --from or --to, only their combined bucket selection may be accessed.", "", 78) + "\n")
	}
	var ordered []*flag.Flag
	flags.VisitAll(func(f *flag.Flag) { ordered = append(ordered, f) })
	sort.SliceStable(ordered, func(i, j int) bool {
		return helpFlagRank(command, ordered[i].Name) < helpFlagRank(command, ordered[j].Name)
	})
	for _, group := range helpGroupOrder {
		var lines strings.Builder
		for _, f := range ordered {
			if helpFlagGroup(f.Name) != group || f.Name == "L" || f.Name == "h" || f.Name == "v" || f.Name == "j" || f.Name == "jobs" {
				continue
			}
			if selected != nil && !globalFlag(f.Name) && !strings.Contains(" "+selected.flags+" ", " "+f.Name+" ") {
				continue
			}
			fmt.Fprintf(&lines, "  %s\n%s\n", helpFlagLabel(f), wrapHelp(helpFlagDescription(command, f), "    ", 78))
		}
		if lines.Len() > 0 {
			fmt.Fprintf(&out, "\n%s:\n%s", group, lines.String())
		}
	}
	out.WriteString("\nExamples:\n")
	if selected != nil {
		example := "glesha " + name
		if selected.arguments != "" {
			example += " " + strings.Trim(selected.arguments, "[]")
		}
		out.WriteString(wrapExample(example) + "\n")
	} else {
		for _, example := range commandExamples[command] {
			out.WriteString(wrapExample(example) + "\n")
		}
	}
	return out.String()
}

func knownHelpAction(command, action string) bool {
	for _, candidate := range helpActions[command] {
		if candidate.name == action {
			return true
		}
	}
	return false
}

func helpFlagRank(command, name string) int {
	order := []string{"class", "file", "key", "into", "snapshot", "full", "incremental", "root", "baseline", "path", "recursive", "all", "cancel", "dry-run", "keep", "authority", "archive", "date", "output", "to", "from", "catalog-to", "prefix", "chunked", "spool-max", "compression", "compression-level", "archive-mode", "keep-archive", "catalog-passphrase-file", "passphrase-file", "archive-passphrase-file", "request-retrieval", "retrieval", "days", "storage-class", "incremental-storage-class", "memory-max", "transfer-workers", "hash-workers", "assume-yes", "force", "format", "json", "remote", "config", "state-dir", "env", "color", "log-level", "help", "version"}
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
