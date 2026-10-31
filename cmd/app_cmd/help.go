package app_cmd

import (
	"flag"
	"fmt"
	"strings"
)

var commandDescriptions = map[string]string{
	"version":       "Show version and build revision.",
	"help":          "Show command help and examples.",
	"create":        "Register a backup set.",
	"configure":     "Change saved settings or map imported roots.",
	"run":           "Create and upload a full or incremental backup.",
	"retry":         "Resume a pending operation.",
	"list":          "List locally known backup sets and IDs.",
	"status":        "Show backup state, size, and estimated storage cost.",
	"history":       "List completed snapshots, newest first.",
	"browse":        "Browse a snapshot in the local catalog.",
	"restore":       "Restore files or cancel unfinished local restore tasks.",
	"import":        "Register an existing full archive.",
	"move":          "Copy a set to another provider and switch destinations.",
	"archive":       "Create a standalone encrypted tar archive.",
	"decrypt":       "Decrypt an archive to compressed tar.",
	"extract":       "Extract an encrypted or plaintext archive into a new directory.",
	"upload":        "Upload a file. Replacing an existing key requires --force.",
	"download":      "Download a file using the selected providers in order.",
	"catalog":       "Discover, open, or synchronize backup catalogs.",
	"cleanup":       "Remove eligible local archive copies; remote backups are kept.",
	"state":         "Move existing state out of the config directory.",
	"storage-class": "Change an AWS archive's storage class.",
	"check":         "Check bucket upload, download, and delete access.",
}

var commandExamples = map[string][]string{
	"create":    {"glesha create docs ~/Documents --to b2"},
	"configure": {"glesha configure docs --compression gzip", "glesha configure docs --root Documents=~/Documents"},
	"run":       {"glesha run docs", "glesha run docs --incremental"},
	"retry":     {"glesha retry docs", "glesha retry docs --archive /path/to/backup.gpg"},
	"list":      {"glesha list"}, "status": {"glesha status", "glesha status docs --refresh"},
	"history":       {"glesha history docs", "glesha history docs --format=json"},
	"browse":        {"glesha browse docs --recursive", "glesha browse docs --path Documents --snapshot SNAPSHOT"},
	"restore":       {"glesha restore docs --into ~/recovered", "glesha restore docs --cancel"},
	"import":        {"glesha import old-docs --file old-backup.tar.gz.gpg", "glesha import old-docs --from b2 --key backups/old-backup.gpg"},
	"move":          {"glesha move docs --from aws --to b2"},
	"archive":       {"glesha archive ~/Documents --output documents.tar.xz.gpg"},
	"decrypt":       {"glesha decrypt documents.tar.xz.gpg"},
	"extract":       {"glesha extract documents.tar.xz.gpg --into ~/recovered"},
	"upload":        {"glesha upload documents.tar.xz.gpg --to b2 --key backups/documents.gpg"},
	"download":      {"glesha download backups/documents.gpg --from b2 --output documents.gpg"},
	"cleanup":       {"glesha cleanup docs --dry-run", "glesha cleanup docs"},
	"state":         {"glesha state migrate", "glesha list --state-dir /path/to/state"},
	"catalog":       {"glesha catalog sync --from b2", "glesha catalog unlock SET", "glesha catalog sync SET"},
	"storage-class": {"glesha storage-class docs --snapshot SNAPSHOT --class DEEP_ARCHIVE"},
	"check":         {"glesha check --to aws,b2"}, "help": {"glesha help run", "glesha help catalog unlock"},
	"version": {"glesha --version"},
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
	if args[0] == "help" && len(args) == 1 {
		return CommandUsage("help"), nil
	}
	if _, ok := handlers[args[0]]; !ok && args[0] != "version" {
		return "", fmt.Errorf("cli: unknown help command %q", args[0])
	}
	if len(args) > 1 {
		catalogAction := args[0] == "catalog" && len(args) == 2 && strings.Contains(" sync unlock resolve pull push reconcile abandon ", " "+args[1]+" ")
		stateAction := args[0] == "state" && len(args) == 2 && args[1] == "migrate"
		if !catalogAction && !stateAction {
			return "", fmt.Errorf("cli: help accepts one command or a catalog/state operation")
		}
	}
	action := ""
	if len(args) > 1 {
		action = args[1]
	}
	return CommandActionUsage(args[0], action), nil
}

var commandArguments = map[string]string{
	"create": "SET PATH...", "configure": "SET", "run": "SET", "retry": "SET",
	"list": "", "status": "[SET]", "history": "SET", "browse": "SET",
	"restore": "SET (--into DIR | --cancel)", "import": "SET (--file FILE | --from REMOTE --key KEY)",
	"move": "SET --from SOURCE --to DESTINATION", "archive": "PATH...", "decrypt": "FILE",
	"extract": "FILE --into DIR", "upload": "FILE --to REMOTE...", "download": "KEY --from REMOTE...",
	"catalog": "ACTION [SET]", "cleanup": "SET|--all", "state": "migrate",
	"storage-class": "SET --class CLASS", "check": "--to REMOTE...",
	"help": "[COMMAND [ACTION]]", "version": "",
}

func globalFlag(name string) bool {
	switch name {
	case "config", "state-dir", "env", "log-level", "L", "color", "format", "progress", "help", "h", "version", "v":
		return true
	}
	return false
}
func flagValueName(f *flag.Flag) string {
	if value, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && value.IsBoolFlag() {
		return ""
	}
	values := map[string]string{
		"format": "FORMAT", "keep": "local|remote",
		"config": "FILE", "state-dir": "DIR", "env": "FILE", "log-level": "LEVEL", "color": "MODE", "progress": "MODE",
		"to": "REMOTE...", "from": "REMOTE...", "catalog-to": "REMOTE|local", "authority": "REMOTE",
		"compression": "FORMAT", "compression-level": "LEVEL", "prefix": "PREFIX",
		"spool-max": "SIZE", "archive-mode": "MODE", "storage-class": "CLASS", "class": "CLASS",
		"incremental-storage-class": "CLASS", "output": "FILE", "into": "DIR",
		"file": "FILE", "key": "KEY", "date": "RFC3339", "baseline": "ID|new",
		"root": "NAME=PATH", "archive": "FILE", "retrieval": "TIER", "days": "N",
		"snapshot": "ID|latest", "path": "PATH", "passphrase-file": "FILE|-",
		"catalog-passphrase-file": "FILE|-", "archive-passphrase-file": "ID=FILE",
		"memory-max": "SIZE", "hash-workers": "N", "transfer-workers": "N", "jobs": "N",
	}
	if name := values[f.Name]; name != "" {
		return name
	}
	return "VALUE"
}
func wrapHelp(text, indent string, width int) string {
	var lines []string
	line := indent
	for _, word := range strings.Fields(text) {
		if len(line) > len(indent) && len(line)+1+len(word) > width {
			lines = append(lines, line)
			line = indent
		}
		if len(line) > len(indent) {
			line += " "
		}
		line += word
	}
	if len(line) > len(indent) {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
func wrapExample(text string) string {
	return strings.ReplaceAll(wrapHelp(text, "  ", 74), "\n", " \\\n  ")
}
