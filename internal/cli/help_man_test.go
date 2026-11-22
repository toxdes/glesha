package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// NOTE: man/glesha-*.1 are generated from the help data by this file.
// Regenerate them with: GLESHA_UPDATE_MAN=1 go test ./cmd/app_cmd -run TestManualsMatchHelp

// manFlagDetails adds sentences that are too long for --help; key "" applies to every command.
var manFlagDetails = map[string]map[string]string{
	"": {
		"archive-mode":            "With memory, the encrypted archive is held in memory and the command fails if it does not fit. With stream, it is written to a temporary file. With auto, it starts in memory and moves to a file when needed.",
		"memory-max":              "SIZE takes a unit such as MiB or GiB and must be at least 48MiB. On Linux, the default is half of the available memory. On other systems, or if the available memory cannot be read, the default is 128MiB.",
		"transfer-workers":        "The memory limit may lower the effective number.",
		"retrieval":               "The default comes from the [cold] tier setting.",
		"days":                    "The default comes from the [cold] days setting. A pending request keeps its original duration.",
		"spool-max":               "Requires --chunked.",
		"passphrase-file":         "A trailing newline is ignored. Without this option, the passphrase is read from a hidden prompt.",
		"catalog-passphrase-file": "Without this option, GLESHA_CATALOG_PASSPHRASE or a hidden prompt is used.",
		"archive-passphrase-file": "Use it when the snapshots of an incremental chain have different passphrases. May be repeated.",
		"root":                    "May be repeated.",
		"date":                    "For example, 2024-05-01T12:00:00Z.",
	},
	"create": {
		"chunked": "Chunked snapshots cannot be kept with --output or --keep-archive, cannot use memory archive mode, cannot be moved with glesha move, and cannot be changed with glesha storage-class.",
	},
	"run": {
		"incremental":   "Requires exactly one remote.",
		"storage-class": "Overrides the set's setting. Classes other than STANDARD require an AWS remote.",
		"assume-yes":    "Confirms removed paths and type changes in an incremental snapshot.",
	},
	"upload": {
		"storage-class": "Classes other than STANDARD require an AWS remote.",
	},
}

var manSeeAlso = map[string][]string{
	"create":        {"glesha-configure", "glesha-run"},
	"configure":     {"glesha-create", "glesha-move"},
	"run":           {"glesha-retry", "glesha-history", "glesha-restore"},
	"retry":         {"glesha-run", "glesha-catalog"},
	"list":          {"glesha-status", "glesha-catalog"},
	"status":        {"glesha-list", "glesha-history"},
	"history":       {"glesha-browse", "glesha-restore"},
	"browse":        {"glesha-history", "glesha-restore"},
	"restore":       {"glesha-history", "glesha-browse"},
	"import":        {"glesha-configure", "glesha-archive"},
	"move":          {"glesha-configure", "glesha-storage-class"},
	"archive":       {"glesha-decrypt", "glesha-extract"},
	"decrypt":       {"glesha-archive", "glesha-extract"},
	"extract":       {"glesha-archive", "glesha-decrypt"},
	"upload":        {"glesha-download", "glesha-check"},
	"download":      {"glesha-upload"},
	"catalog":       {"glesha-list", "glesha-history"},
	"cleanup":       {"glesha-status"},
	"state":         {"glesha-list"},
	"storage-class": {"glesha-move", "glesha-restore"},
	"check":         {"glesha-upload"},
	"help":          {},
	"version":       {},
}

func roff(text string) string {
	text = strings.ReplaceAll(text, `\`, `\e`)
	text = strings.ReplaceAll(text, "-", `\-`)
	if strings.HasPrefix(text, ".") || strings.HasPrefix(text, "'") {
		text = `\&` + text
	}
	return text
}

func sentence(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}

func manFlagDescription(command string, label, description string, details ...string) string {
	text := sentence(description)
	for _, detail := range details {
		if detail != "" {
			text += " " + detail
		}
	}
	return ".TP\n.B " + roff(label) + "\n" + roff(text) + "\n"
}

func manPage(command string) string {
	spec, _ := lookupCommand(command)
	h := spec.help
	name := "glesha-" + command
	var out strings.Builder
	out.WriteString(`.TH "` + strings.ToUpper(name) + `" "1" "" "glesha" "User Commands"` + "\n.nh\n.ad l\n")
	out.WriteString(".SH NAME\n" + roff(name) + ` \- ` + roff(h.summary) + "\n")
	out.WriteString(".SH SYNOPSIS\n")
	synopsis := h.synopsis
	if command == "state" {
		synopsis = []string{"migrate"}
	}
	if len(synopsis) == 0 {
		synopsis = []string{""}
	}
	for i, line := range synopsis {
		if i > 0 {
			out.WriteString(".br\n")
		}
		out.WriteString(`\fBglesha ` + roff(command) + `\fR ` + roff(strings.TrimSpace(line+" [options]")) + "\n")
	}
	description := h.description
	if command == "state" {
		description = spec.action("migrate").description
	}
	out.WriteString(".SH DESCRIPTION\n")
	for i, paragraph := range description {
		if i > 0 {
			out.WriteString(".PP\n")
		}
		if strings.HasPrefix(paragraph, "Run 'glesha help catalog ACTION'") {
			paragraph = "The options each action accepts are listed with it."
		}
		out.WriteString(roff(paragraph) + "\n")
	}
	if command == "catalog" {
		out.WriteString(".SH ACTIONS\n")
		for _, action := range spec.actions {
			out.WriteString(".TP\n.B " + roff(strings.TrimSpace(action.name+" "+action.arguments)) + "\n")
			out.WriteString(roff(strings.Join(action.description, " ")))
			var flags []string
			for _, f := range helpOptions(spec, &action) {
				if !globalOption(f.name) {
					flags = append(flags, "--"+f.name)
				}
			}
			if len(flags) > 0 {
				out.WriteString(" " + roff("Options: "+strings.Join(flags, ", ")+"."))
			}
			out.WriteString("\n")
		}
	}
	flags := helpOptions(spec, nil)
	first := true
	for _, group := range helpGroupOrder {
		if group == "Global options" {
			continue
		}
		var lines strings.Builder
		for _, f := range flags {
			if f.group != group {
				continue
			}
			label := f.label
			if f.name == "transfer-workers" {
				label = "-j, --jobs, --transfer-workers N"
			}
			lines.WriteString(manFlagDescription(command, label, f.usage, manFlagDetails[""][f.name], manFlagDetails[command][f.name]))
		}
		if lines.Len() == 0 {
			continue
		}
		if first {
			out.WriteString(".SH OPTIONS\n")
			first = false
		}
		if group != "Options" {
			out.WriteString(".SS " + group + "\n")
		}
		out.WriteString(lines.String())
		if group == "Remote options" {
			out.WriteString(".PP\n" + roff(remoteSelectionNote) + "\n")
		}
	}
	if first {
		out.WriteString(".SH OPTIONS\n")
	}
	out.WriteString(".PP\n" + roff("The global options described in glesha(1) are also accepted.") + "\n")
	out.WriteString(".SH EXAMPLES\n.nf\n")
	for _, example := range h.examples {
		out.WriteString(roff(example) + "\n")
	}
	out.WriteString(".fi\n.SH SEE ALSO\n")
	seeAlso := append([]string{"glesha"}, manSeeAlso[command]...)
	sort.Strings(seeAlso[1:])
	for i, page := range seeAlso {
		suffix := ""
		if i < len(seeAlso)-1 {
			suffix = ","
		}
		out.WriteString(".BR " + roff(page) + " (1)" + suffix + "\n")
	}
	return out.String()
}

func TestManualsMatchHelp(t *testing.T) {
	update := os.Getenv("GLESHA_UPDATE_MAN") == "1"
	for _, spec := range commands {
		command := spec.name
		t.Run(command, func(t *testing.T) {
			file := filepath.Join("..", "..", "man", "glesha-"+command+".1")
			want := manPage(command)
			for _, r := range want {
				if r > 127 {
					t.Fatal("manual contains non-ASCII text")
				}
			}
			if update {
				if err := os.WriteFile(file, []byte(want), 0644); err != nil {
					t.Fatal(err)
				}
				return
			}
			got, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Fatalf("%s is out of date; run GLESHA_UPDATE_MAN=1 go test ./cmd/app_cmd -run TestManualsMatchHelp", file)
			}
		})
	}
}
