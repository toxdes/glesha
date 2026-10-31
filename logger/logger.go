package L

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/term"
)

type Level string

const (
	DEBUG  Level = "debug"
	INFO   Level = "info"
	WARN   Level = "warn"
	ERROR  Level = "error"
	SILENT Level = "silent"
)

func (l Level) String() string { return string(l) }
func ParseLevel(value string) (Level, error) {
	if value == "normal" {
		value = "info"
	}
	level := Level(strings.ToLower(value))
	switch level {
	case DEBUG, INFO, WARN, ERROR, SILENT:
		return level, nil
	}
	return "", fmt.Errorf("logger: unknown level %q", value)
}

type ColorMode string

const (
	COLOR_MODE_AUTO   ColorMode = "auto"
	COLOR_MODE_ALWAYS ColorMode = "always"
	COLOR_MODE_NEVER  ColorMode = "never"
)

func (c ColorMode) String() string { return string(c) }
func ParseColorMode(value string) (ColorMode, error) {
	if value == "yes" {
		value = "always"
	}
	if value == "no" {
		value = "never"
	}
	mode := ColorMode(strings.ToLower(value))
	switch mode {
	case COLOR_MODE_AUTO, COLOR_MODE_ALWAYS, COLOR_MODE_NEVER:
		return mode, nil
	}
	return "", fmt.Errorf("logger: unknown color mode %q", value)
}

var state = struct {
	sync.Mutex
	level      Level
	color      ColorMode
	progress   *ProgressRenderer
	structured bool
}{level: INFO, color: COLOR_MODE_AUTO}

func SetLevelFromString(value string) error {
	level, err := ParseLevel(value)
	if err != nil {
		return err
	}
	state.Lock()
	defer state.Unlock()
	state.level = level
	return nil
}
func SetColorModeFromString(value string) error {
	mode, err := ParseColorMode(value)
	if err != nil {
		return err
	}
	state.Lock()
	defer state.Unlock()
	state.color = mode
	return nil
}
func Error(message string) {
	message = ASCII(strings.TrimPrefix(message, "cli: "))
	state.Lock()
	defer state.Unlock()
	resume := pauseProgressLocked()
	defer resume()
	if state.level == SILENT {
		return
	}
	if state.structured {
		_ = writeDiagnosticLocked("error", struct {
			Message string `json:"message"`
		}{message})
		return
	}
	if state.color == COLOR_MODE_ALWAYS || state.color == COLOR_MODE_AUTO && term.IsTerminal(int(os.Stderr.Fd())) {
		fmt.Fprintf(os.Stderr, "\x1b[31merror: %s\x1b[0m\n", message)
	} else {
		fmt.Fprintf(os.Stderr, "error: %s\n", message)
	}
}

func Debug(message string) {
	message = ASCII(message)
	state.Lock()
	defer state.Unlock()
	if state.level == DEBUG {
		resume := pauseProgressLocked()
		defer resume()
		if state.structured {
			_ = writeDiagnosticLocked("debug", struct {
				Message string `json:"message"`
			}{message})
		} else {
			fmt.Fprintf(os.Stderr, "debug: %s\n", message)
		}
	}
}

func Warn(message string) {
	state.Lock()
	defer state.Unlock()
	if state.level == ERROR || state.level == SILENT {
		return
	}
	resume := pauseProgressLocked()
	defer resume()
	if state.structured {
		_ = writeDiagnosticLocked("warning", struct {
			Message string `json:"message"`
		}{message})
	} else if state.color == COLOR_MODE_ALWAYS || state.color == COLOR_MODE_AUTO && term.IsTerminal(int(os.Stderr.Fd())) {
		fmt.Fprintf(os.Stderr, "\x1b[33mwarning:\x1b[0m %s\n", ASCII(message))
	} else {
		fmt.Fprintf(os.Stderr, "warning: %s\n", ASCII(message))
	}
}
func ColorEnabled(terminal bool) bool {
	state.Lock()
	defer state.Unlock()
	return state.color == COLOR_MODE_ALWAYS || state.color == COLOR_MODE_AUTO && terminal
}
func ProgressEnabled() bool {
	state.Lock()
	defer state.Unlock()
	return state.level == INFO || state.level == DEBUG
}
func HumanText(text string, terminal bool) string {
	return HumanTextAtLevel(text, terminal, INFO)
}

func HumanTextAtLevel(text string, terminal bool, level Level) string {
	text = ASCII(text)
	if !ColorEnabled(terminal) {
		return text
	}
	code := ""
	switch level {
	case ERROR:
		code = "31"
	case WARN:
		code = "33"
	}
	if code == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func pauseProgressLocked() func() {
	if state.progress == nil {
		return func() {}
	}
	r := state.progress
	r.mu.Lock()
	r.clearLocked()
	return r.mu.Unlock
}

func ASCII(text string) string { return displayText(text, true) }

// Value escapes controls in a single-line label, path, or table cell.
func Value(value string) string { return displayText(value, false) }

func displayText(text string, multiline bool) string {
	var out strings.Builder
	for _, r := range text {
		if r >= 32 && r < 127 || multiline && (r == '\n' || r == '\t') {
			out.WriteRune(r)
			continue
		}
		quoted := strconv.QuoteToASCII(string(r))
		out.WriteString(quoted[1 : len(quoted)-1])
	}
	return out.String()
}
