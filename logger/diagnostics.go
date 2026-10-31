package L

import (
	"encoding/json"
	"fmt"
	"os"
)

func SetStructuredDiagnostics(enabled bool) {
	state.Lock()
	defer state.Unlock()
	state.structured = enabled
}
func WriteDiagnostic(kind string, data any) error {
	state.Lock()
	defer state.Unlock()
	return writeDiagnosticLocked(kind, data)
}
func writeDiagnosticLocked(kind string, data any) error {
	return json.NewEncoder(os.Stderr).Encode(struct {
		Version int    `json:"version"`
		Kind    string `json:"kind"`
		Data    any    `json:"data"`
	}{1, kind, data})
}
func EmitProgress(event ProgressEvent) error { return WriteDiagnostic("progress", event) }

func Info(message string) {
	state.Lock()
	defer state.Unlock()
	if state.level != INFO && state.level != DEBUG {
		return
	}
	if state.structured {
		_ = writeDiagnosticLocked("info", struct {
			Message string `json:"message"`
		}{message})
		return
	}
	resume := pauseProgressLocked()
	defer resume()
	_, _ = fmt.Fprintln(os.Stderr, ASCII(message))
}
