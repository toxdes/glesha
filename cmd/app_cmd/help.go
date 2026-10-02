package app_cmd

import (
	"flag"
	"fmt"
	"strings"
)

var commandDescriptions = map[string]string{
	"version":       "Show version and build revision.",
	"help":          "Show command help and examples.",
	"create":        "Register a backup set and its source paths.",
	"configure":     "Change saved settings or map imported roots.",
	"run":           "Create and upload a full backup. Use --incremental for changes only.",
	"retry":         "Resume a pending operation.",
	"list":          "List backup sets. Use --remote to discover published sets.",
	"status":        "Show backup state, catalog availability, stored sizes and estimated annual cost.",
	"history":       "List completed snapshots, newest first.",
	"browse":        "Browse a snapshot in the local catalog.",
	"restore":       "Restore a snapshot into a new directory.",
	"import":        "Register an existing full archive.",
	"move":          "Copy a set to another provider and switch destinations.",
	"archive":       "Create a standalone encrypted tar archive.",
	"decrypt":       "Decrypt an archive to compressed tar.",
	"extract":       "Extract an encrypted or plaintext archive into a new directory.",
	"upload":        "Upload a file. Replacing an existing key requires --force.",
	"download":      "Download a file using the selected providers in order.",
	"catalog":       "Pull, push, reconcile or abandon catalog changes.",
	"storage-class": "Change an AWS archive's storage class.",
	"check":         "Test provider read, write and delete access.",
}

var commandExamples = map[string][]string{
	"version":       {"glesha version", "glesha -v", "glesha -version", "glesha --version"},
	"help":          {"glesha help", "glesha help run", "glesha help catalog pull"},
	"create":        {"glesha create docs ~/Documents --to b2", "glesha create photos ~/Pictures --to aws,b2 --compression gzip"},
	"configure":     {"glesha configure docs --compression gzip", "glesha configure imported --root Documents=~/Documents --to b2 --catalog-to b2"},
	"run":           {"glesha run docs", "glesha run docs --incremental", "glesha run docs --keep-archive", "glesha run docs --incremental --assume-yes --passphrase-file /secure/archive-password --catalog-passphrase-file /secure/catalog-password --env /secure/cloud.env"},
	"retry":         {"glesha retry docs", "glesha retry docs --archive /new/location/backup.tar.xz.gpg"},
	"list":          {"glesha list", "glesha list --remote --from b2 --env /secure/cloud.env"},
	"status":        {"glesha status", "glesha status docs --refresh --env /secure/cloud.env"},
	"history":       {"glesha history docs", "glesha history docs --refresh --json"},
	"browse":        {"glesha browse docs", "glesha browse docs --path Documents --recursive", "glesha browse docs --snapshot SNAPSHOT_ID --recursive --json"},
	"restore":       {"glesha restore docs --into ~/recovered", "glesha restore docs --snapshot SNAPSHOT_ID --into ~/recovered --from b2,aws", "glesha restore docs --into ~/recovered --request-retrieval --retrieval bulk"},
	"import":        {"glesha import old-docs --file encb-old.tar.gz.gpg", "glesha import old-docs --from b2 --key backups/encb-old.tar.gz.gpg", "glesha import old-docs --file encb-old.tar.gz.gpg --from b2 --key backups/encb-old.tar.gz.gpg --deep-verify"},
	"move":          {"glesha move docs --to b2", "glesha move docs --to other-remote --request-retrieval --retrieval bulk"},
	"archive":       {"glesha archive ~/Documents", "glesha archive ~/Documents ~/Pictures --compression gzip --output personal.tar.gz.gpg", "glesha archive ~/Documents --archive-mode stream --memory-max 128MiB --passphrase-file /secure/archive-password"},
	"decrypt":       {"glesha decrypt personal.tar.xz.gpg", "glesha decrypt encb-old.tar.gz.gpg --output recovered.tar.gz --passphrase-file /secure/archive-password"},
	"extract":       {"glesha extract personal.tar.xz.gpg --into ~/recovered", "glesha extract recovered.tar.gz --into ~/recovered"},
	"upload":        {"glesha upload personal.tar.xz.gpg --to b2", "glesha upload personal.tar.xz.gpg --to aws,b2 --key backups/personal.tar.xz.gpg"},
	"download":      {"glesha download backups/personal.tar.xz.gpg --from b2,aws", "glesha download backups/personal.tar.xz.gpg --from b2 --output personal.tar.xz.gpg"},
	"catalog":       {"glesha catalog pull docs", "glesha catalog push old-docs", "glesha catalog reconcile docs", "glesha catalog abandon docs"},
	"storage-class": {"glesha storage-class docs --snapshot SNAPSHOT_ID --class DEEP_ARCHIVE"},
	"check":         {"glesha check --to b2 --env /secure/cloud.env", "glesha check --to aws,b2 --env /secure/cloud.env", "glesha check --to b2 --json --env /secure/cloud.env"},
}

func showHelp(args []string) error {
	if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Print(Usage())
		return nil
	}
	if args[0] == "help" && len(args) == 1 {
		fmt.Print(CommandUsage("help"))
		return nil
	}
	if _, ok := handlers[args[0]]; !ok && args[0] != "version" {
		return fmt.Errorf("cli: unknown help command %q", args[0])
	}
	if len(args) > 1 {
		if args[0] != "catalog" || len(args) != 2 || !strings.Contains(" pull push reconcile abandon ", " "+args[1]+" ") {
			return fmt.Errorf("cli: help accepts one command or catalog operation")
		}
	}
	fmt.Print(CommandUsage(args[0]))
	return nil
}

var commandArguments = map[string]string{
	"version": "Show version and build revision.",
	"help":    "Show command help and examples.",
}

func globalFlag(name string) bool {
	switch name {
	case "config", "env", "log-level", "L", "color", "help", "h", "version", "v":
		return true
	}
	return false
}
func flagValueName(f *flag.Flag) string {
	if value, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && value.IsBoolFlag() {
		return ""
	}
	values := map[string]string{
		"config": "FILE", "env": "FILE", "log-level": "LEVEL", "color": "MODE",
		"to": "REMOTE...", "from": "REMOTE...", "catalog-to": "REMOTE|local",
		"compression": "FORMAT", "compression-level": "LEVEL", "prefix": "PREFIX",
		"archive-mode": "MODE", "storage-class": "CLASS", "class": "CLASS",
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
