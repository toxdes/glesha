package app_cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"glesha/backup"
	"glesha/database/model"
	"glesha/database/repository"
	L "glesha/logger"
)

type outputFormat string

const (
	formatPlain outputFormat = "plain"
	formatJSON  outputFormat = "json"
	formatTable outputFormat = "table"
	formatCSV   outputFormat = "csv"
)

const (
	resultOutputVersion      = 2
	catalogSyncOutputVersion = 3
)

func (f outputFormat) String() string { return string(f) }
func parseOutputFormat(value string) (outputFormat, error) {
	switch f := outputFormat(value); f {
	case formatPlain, formatJSON, formatTable, formatCSV:
		return f, nil
	default:
		return "", fmt.Errorf("cli: unknown output format %q", value)
	}
}
func (e *AppCmdEnv) jsonOutput() bool { return e.Format == formatJSON.String() }

func normalizeOutput(command string, e *AppCmdEnv) error {
	f, err := parseOutputFormat(e.Format)
	if err != nil {
		return err
	}
	if command != "status" && (f == formatTable || f == formatCSV) {
		return fmt.Errorf("cli: %s supports --format=plain or json", command)
	}
	switch e.Progress {
	case "auto", "plain", "off":
	default:
		return fmt.Errorf("cli: progress must be auto, plain or off")
	}
	return nil
}

type operationResult struct {
	Command string       `json:"command"`
	State   model.Status `json:"state"`
	Set     string       `json:"set,omitempty"`
	Path    string       `json:"path,omitempty"`
	Key     string       `json:"key,omitempty"`
}

func (r *runtime) emit(kind string, data any, text string) error {
	return r.emitAtLevel(kind, data, text, L.INFO)
}

func (r *runtime) emitAtLevel(kind string, data any, text string, level L.Level) error {
	if r.env.jsonOutput() {
		version := resultOutputVersion
		switch kind {
		case "catalog_state", "catalog_sync_summary", "catalog_sync_error":
			version = catalogSyncOutputVersion
		}
		if err := json.NewEncoder(r.out).Encode(struct {
			Version int    `json:"version"`
			Kind    string `json:"kind"`
			Data    any    `json:"data"`
		}{version, kind, data}); err != nil {
			return err
		}
		r.emitted++
		return nil
	}
	if text == "" {
		return nil
	}
	return L.WriteHumanText(r.ctx, r.out, text, term.IsTerminal(int(os.Stdout.Fd())), level)
}
func (r *runtime) completed(path, key, text string) error {
	data := operationResult{Command: r.command, State: model.STATUS_COMPLETED, Path: path, Key: key}
	if r.service != nil {
		data.Set = r.service.Set.ID
	}
	return r.emit("result", data, text)
}
func (r *runtime) help(args []string) error {
	text, err := helpText(args)
	if err != nil {
		return err
	}
	if !r.env.jsonOutput() {
		_, err = fmt.Fprint(r.out, text)
		return err
	}
	return r.emit("help", struct {
		Command string `json:"command"`
		Text    string `json:"text"`
	}{strings.Join(args, " "), text}, "")
}

// inspect flags without loading configuration or mistaking a flag value for an option
func requestedJSON(args []string) bool {
	command, _, _ := ResolveCommand(args)
	flags := flagSet(command, &AppCmdEnv{})
	format := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		f := flags.Lookup(name)
		if f == nil {
			continue
		}
		boolean := false
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok {
			boolean = b.IsBoolFlag()
		}
		if !hasValue && !boolean && i+1 < len(args) {
			i++
			value = args[i]
		}
		if name == "format" {
			format = value
		}

	}
	return format == "json"
}
func PrintError(args []string, err error) {
	if !requestedJSON(args) {
		L.Error(err.Error())
		return
	}
	code := "FAILED"
	switch {
	case errors.Is(err, context.Canceled):
		code = "CANCELED"
	case errors.Is(err, backup.ErrConfirmationRequired):
		code = "CONFIRMATION_REQUIRED"
	case errors.Is(err, backup.ErrConflict):
		code = "CONFLICT"
	case errors.Is(err, backup.ErrPending):
		code = "PENDING"
	case ExitCode(err) == 2:
		code = "INVALID_ARGUMENT"
	}
	_ = L.WriteDiagnostic("error", struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		ExitCode int    `json:"exit_code"`
	}{code, err.Error(), ExitCode(err)})
}

type reportedCommandError struct {
	command string
	cause   error
}

func (e *reportedCommandError) Error() string {
	return e.command + ": incomplete; see report above"
}
func (e *reportedCommandError) Unwrap() error { return e.cause }

func reportedError(command string, cause error) error {
	if cause == nil {
		return nil
	}
	return &reportedCommandError{command: command, cause: cause}
}

type setLookupError struct {
	name  string
	cause error
}

func (e *setLookupError) Error() string {
	if errors.Is(e.cause, repository.ErrAmbiguousSetName) {
		return fmt.Sprintf("cli: set name %q matches multiple sets; use a UUID from glesha list", e.name)
	}
	return fmt.Sprintf("cli: unknown set %q; run glesha list to see known sets", e.name)
}
func (e *setLookupError) Unwrap() error { return e.cause }

func setError(name string, err error) error {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrAmbiguousSetName) {
		return &setLookupError{name: name, cause: err}
	}
	return err
}

func (r *runtime) result(text string) error {
	if r.env.jsonOutput() {
		return nil
	}
	return r.emit("", nil, text)
}

func (r *runtime) activity(text string) error {
	if r.env.jsonOutput() || r.env.Progress == "off" || !L.ProgressEnabled() {
		return nil
	}
	out := r.errOut
	if out == nil {
		out = os.Stderr
	}
	return L.WriteHumanText(r.ctx, out, text, term.IsTerminal(int(os.Stderr.Fd())), L.INFO)
}

func (r *runtime) startActivity(label string) (*L.Progress, error) {
	if r.env.Progress == "off" || !L.ProgressEnabled() {
		return nil, nil
	}
	progress := L.StartProgress(r.ctx, label, -1)
	if progress == nil {
		return nil, r.activity(label + "...")
	}
	return progress, nil
}

func (r *runtime) emptyResult(subject string) error {
	return r.result(fmt.Sprintf("No %s found.", subject))
}

func (r *runtime) remoteLabel(id string) string {
	remote, err := r.registry.Remote(r.ctx, id)
	if err != nil {
		return id
	}
	if remote.Name != "" && remote.Name != remote.ID {
		return remote.Name
	}
	for _, name := range r.configuredRemotes() {
		provider := r.config.RemotesByName()[name]
		identity := model.Remote{Kind: provider.Kind, Bucket: provider.Bucket, Endpoint: provider.Endpoint, Region: provider.Region}
		if identity.Identity() == id {
			return name
		}
	}
	if remote.Bucket != "" {
		return remote.Kind + "/" + remote.Bucket
	}
	return remote.Kind
}

func (r *runtime) emptyInventory() error {
	return r.result("No sets found locally.\n  Discover backups: glesha catalog sync\n  Start a backup:   glesha create SET PATH --to REMOTE")
}
