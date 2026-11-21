package app_cmd

import (
	"fmt"
	"strings"
)

// overview is the command list shown by plain 'glesha help'.
var overview = []struct {
	title   string
	entries []overviewEntry
}{
	{"Backup sets", []overviewEntry{
		{"create SET PATH...", "register a new backup set"},
		{"configure SET", "change the settings of a set"},
		{"run SET", "create a snapshot and upload it"},
		{"retry SET", "resume interrupted work"},
		{"restore SET", "restore a snapshot into a directory"},
		{"import SET", "add an existing archive to a set"},
		{"move SET", "copy a set's history to another remote"},
		{"storage-class SET", "change the AWS storage class of a snapshot"},
		{"cleanup SET|--all", "delete local copies already stored remotely"},
	}},
	{"Inspection", []overviewEntry{
		{"list", "list known sets"},
		{"status [SET]", "show state, size, and estimated cost"},
		{"history SET", "list the snapshots of a set"},
		{"browse SET", "list the files in a snapshot"},
	}},
	{"Catalogs", []overviewEntry{
		{"catalog sync [SET]", "discover sets, or synchronize one catalog"},
		{"catalog unlock SET", "open a downloaded catalog"},
		{"catalog resolve SET", "review or settle catalog conflicts"},
		{"catalog ACTION SET", "pull, push, reconcile, or abandon"},
	}},
	{"Standalone files", []overviewEntry{
		{"archive PATH...", "create an encrypted archive"},
		{"decrypt FILE", "decrypt an archive to a compressed tar"},
		{"extract FILE", "unpack an archive into a new directory"},
		{"upload FILE", "upload a file to a bucket"},
		{"download KEY", "download an object from a bucket"},
		{"check", "test access to a bucket"},
	}},
	{"Other", []overviewEntry{
		{"state migrate", "move state from the legacy location"},
		{"help [COMMAND [ACTION]]", "show help for a command"},
		{"version", "show version information"},
	}},
}

type overviewEntry struct{ usage, summary string }

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
	if _, ok := lookupCommand(args[0]); !ok {
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
