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
	level    Level
	color    ColorMode
	progress *ProgressRenderer
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
	message = ASCII(message)
	state.Lock()
	defer state.Unlock()
	resume := pauseProgressLocked()
	defer resume()
	if state.level == SILENT {
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
		fmt.Fprintf(os.Stderr, "debug: %s\n", message)
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
	fmt.Fprintf(os.Stderr, "warning: %s\n", ASCII(message))
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
	text = ASCII(text)
	if !ColorEnabled(terminal) {
		return text
	}
	return "\x1b[36m" + text + "\x1b[0m"
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

func ASCII(text string) string {
	var out strings.Builder
	for _, r := range text {
		if r < 128 {
			out.WriteRune(r)
			continue
		}
		quoted := strconv.QuoteToASCII(string(r))
		out.WriteString(quoted[1 : len(quoted)-1])
	}
	return out.String()
}
