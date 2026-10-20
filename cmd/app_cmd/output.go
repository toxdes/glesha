package app_cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"glesha/backup"
	"glesha/database/model"
	L "glesha/logger"
)

type outputFormat string

const (
	formatPlain outputFormat = "plain"
	formatJSON  outputFormat = "json"
	formatTable outputFormat = "table"
	formatCSV   outputFormat = "csv"
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
	if r.env.jsonOutput() {
		if err := json.NewEncoder(r.out).Encode(struct {
			Version int    `json:"version"`
			Kind    string `json:"kind"`
			Data    any    `json:"data"`
		}{2, kind, data}); err != nil {
			return err
		}
		r.emitted++
		return nil
	}
	if text == "" {
		return nil
	}
	_, err := fmt.Fprintln(r.out, L.HumanText(text, term.IsTerminal(int(os.Stdout.Fd()))))
	return err
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
