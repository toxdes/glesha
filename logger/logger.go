package L

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// NOTE: populated at build time with -ldflags (-X)
var printCallerLocation string

// log levels
type LogLevel byte

const (
	DEBUG LogLevel = iota
	INFO
	NORMAL
	WARN
	ERROR
	PANIC
	SILENT
)

// color modes
type ColorMode int

const (
	COLOR_MODE_AUTO ColorMode = iota
	COLOR_MODE_ALWAYS
	COLOR_MODE_NEVER
)

// styles
// debug - blue
var debugStyle = lipgloss.NewStyle().Padding(0).Margin(0).
	Foreground(lipgloss.Color("4"))

// info - green
var infoStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("2"))

// no color - normal
var noColorStyle = lipgloss.NewStyle()

// warn - yellow
var warnStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("3"))

// error,panic - red
var errorStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("1"))

// prefixes
const (
	debugPrefix  string = "DBG  "
	infoPrefix   string = "INF  "
	normalPrefix string = "     "
	warnPrefix   string = "WRN  "
	errorPrefix  string = "ERR  "
	panicPrefix  string = "PNC  "
)

var (
	level        = INFO
	colorMode    = COLOR_MODE_AUTO
	debugLogger  = log.New(os.Stdout, colorize(debugPrefix, &debugStyle), log.Lmsgprefix)
	infoLogger   = log.New(os.Stdout, colorize(infoPrefix, &infoStyle), log.Lmsgprefix)
	normalLogger = log.New(os.Stdout, colorize(normalPrefix, &noColorStyle), log.Lmsgprefix)
	warnLogger   = log.New(os.Stdout, colorize(warnPrefix, &warnStyle), log.Lmsgprefix)
	errorLogger  = log.New(os.Stderr, colorize(errorPrefix, &errorStyle), log.Lmsgprefix)
	panicLogger  = log.New(os.Stderr, colorize(panicPrefix, &errorStyle), log.Lmsgprefix)
	footerMutex  = &sync.Mutex{}
	footerText   = ""
	footerLines  = 0
	footerLevel  = INFO
)

// cursor sequences
const (
	c_escape     string = "\x1B"
	c_clear_line string = c_escape + "[2K"
	c_up         string = c_escape + "[1A"
)

func SetLevelFromString(l string) error {
	switch strings.ToLower(l) {
	case "debug":
		level = DEBUG
	case "info":
		level = INFO
	case "warn":
		level = WARN
	case "error":
		level = ERROR
	case "panic":
		level = PANIC
	case "silent":
		level = SILENT
	default:
		return fmt.Errorf("unsupported log level: %s", l)
	}
	return nil
}

func SetLevel(l LogLevel) error {
	switch l {
	case DEBUG, INFO, WARN, ERROR, PANIC, SILENT:
		level = l
	default:
		return fmt.Errorf("unsupported log level: %d", l)
	}
	return nil
}

func SetColorModeFromString(colorModeStr string) error {
	switch strings.ToLower(colorModeStr) {
	case "always":
		colorMode = COLOR_MODE_ALWAYS
	case "never":
		colorMode = COLOR_MODE_NEVER
	case "auto":
		colorMode = COLOR_MODE_AUTO
	default:
		return fmt.Errorf("unsupported color mode: %s", colorModeStr)
	}
	updateLoggerPrefixColors()
	return nil
}

func SetColorMode(cm ColorMode) error {
	switch cm {
	case COLOR_MODE_ALWAYS, COLOR_MODE_NEVER, COLOR_MODE_AUTO:
		colorMode = cm
	default:
		return fmt.Errorf("unsupported color mode: %s", cm)
	}
	updateLoggerPrefixColors()
	return nil
}

func (cm ColorMode) String() string {
	switch cm {
	case COLOR_MODE_ALWAYS:
		return "always"
	case COLOR_MODE_NEVER:
		return "never"
	case COLOR_MODE_AUTO:
		return "auto"
	default:
		return "auto"
	}
}

func Debug(v ...any) {
	if level <= DEBUG {
		// FIXME: race conditions
		clearFooter()
		if printCallerLocation == "true" {
			printWithCallerLocation(debugLogger, &debugStyle, fmt.Sprintf("%s\n", v...))
		} else {
			printMultiline(debugLogger, &debugStyle, fmt.Sprintf("%s\n", v...))
		}
		printFooter()
	}
}

func Info(v ...any) {
	if level <= INFO {
		clearFooter()
		printMultiline(infoLogger, &infoStyle, fmt.Sprintf("%s\n", v...))
		printFooter()
	}
}

func Warn(v ...any) {
	if level <= WARN {
		clearFooter()
		printMultiline(warnLogger, &warnStyle, fmt.Sprintf("%s\n", v...))
		printFooter()
	}
}

func Error(v ...any) {
	if level <= ERROR {
		clearFooter()
		if printCallerLocation == "true" {
			printWithCallerLocation(errorLogger, &errorStyle, fmt.Sprintf("%s\n", v...))
		} else {
			printMultiline(errorLogger, &errorStyle, fmt.Sprintf("%s\n", v...))
		}
		printFooter()
	}
}

func Panic(v ...any) {
	printMultiline(panicLogger, &errorStyle, fmt.Sprintf("%s\n", v...))
	os.Exit(1)
}

func GetLogLevel() LogLevel {
	return level
}

func IsVerbose() bool {
	return level < INFO
}

func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "debug"
	case INFO:
		return "info"
	case WARN:
		return "warn"
	case ERROR:
		return "error"
	case SILENT:
		return "silent"
	default:
		return "Unknown log level, indicates a bug. Please report"
	}
}

func Printf(format string, v ...any) (int, error) {
	if level < SILENT {
		return printMultiline(normalLogger, &noColorStyle, fmt.Sprintf(format, v...)), nil
	}
	return 0, nil
}

func Print(a ...any) (int, error) {
	if level < SILENT {
		return printMultiline(normalLogger, &noColorStyle, fmt.Sprint(a...)), nil
	}
	return 0, nil
}

func Println(a ...any) (int, error) {
	if level < SILENT {
		return printMultiline(normalLogger, &noColorStyle, fmt.Sprintln(a...)), nil
	}
	return 0, nil
}

// prints a persistent string "s" at the bottom of the terminal output.
// previous "footer" is cleared before each log and reprinted after.
// passing "s" as an empty string removes the footer.
func Footer(l LogLevel, s string) {
	// acquire lock
	footerMutex.Lock()
	defer footerMutex.Unlock()

	footerText = strings.TrimSpace(s)
	footerLevel = l

	// clear previous footer output and reprint
	clearFooter()
	footerLines = printFooter()
}
