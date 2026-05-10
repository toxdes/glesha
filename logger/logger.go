package L

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

// NOTE: populated at build time with -ldflags (-X)
var printCallerLocation string

// log levels
type LogLevel byte

const (
	DEBUG LogLevel = iota
	NORMAL
	WARN
	ERROR
	PANIC
	SILENT
)

// prefixes
const (
	debugPrefix  string = "debug: "
	normalPrefix string = ""
	warnPrefix   string = "warn: "
	errorPrefix  string = "error: "
	panicPrefix  string = "panic: "
)

var (
	level        = WARN
	colorMode    = COLOR_MODE_AUTO
	debugLogger  = log.New(os.Stdout, colorize(debugPrefix, ansiBlue), log.Lmsgprefix)
	normalLogger = log.New(os.Stdout, colorize(normalPrefix, ansiNone), log.Lmsgprefix)
	warnLogger   = log.New(os.Stdout, colorize(warnPrefix, ansiYellow), log.Lmsgprefix)
	errorLogger  = log.New(os.Stderr, colorize(errorPrefix, ansiRed), log.Lmsgprefix)
	panicLogger  = log.New(os.Stderr, colorize(panicPrefix, ansiRed), log.Lmsgprefix)
	footerMutex  = &sync.Mutex{}
	footerText   = ""
	footerLines  = 0
	footerLevel  = WARN
)

func SetLevelFromString(l string) error {
	switch strings.ToLower(l) {
	case "debug":
		level = DEBUG
	case "normal":
		level = NORMAL
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
	case DEBUG, NORMAL, WARN, ERROR, PANIC, SILENT:
		level = l
	default:
		return fmt.Errorf("unsupported log level: %d", l)
	}
	return nil
}

func Debug(v ...any) {
	if level <= DEBUG {
		// FIXME: race conditions
		clearFooter()
		if printCallerLocation == "true" {
			printWithCallerLocation(debugLogger, ansiBlue, fmt.Sprintf("%s\n", v...))
		} else {
			printMultiline(debugLogger, ansiBlue, fmt.Sprintf("%s\n", v...))
		}
		printFooter()
	}
}

func Warn(v ...any) {
	if level <= WARN {
		clearFooter()
		printMultiline(warnLogger, ansiYellow, fmt.Sprintf("%s\n", v...))
		printFooter()
	}
}

func Error(v ...any) {
	if level <= ERROR {
		clearFooter()
		if printCallerLocation == "true" {
			printWithCallerLocation(errorLogger, ansiRed, fmt.Sprintf("%s\n", v...))
		} else {
			printMultiline(errorLogger, ansiRed, fmt.Sprintf("%s\n", v...))
		}
		printFooter()
	}
}

func Panic(v ...any) {
	printMultiline(panicLogger, ansiRed, fmt.Sprintf("%s\n", v...))
	os.Exit(1)
}

func GetLogLevel() LogLevel {
	return level
}

func IsVerbose() bool {
	return level <= DEBUG
}

func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "debug"
	case NORMAL:
		return "normal"
	case WARN:
		return "warn"
	case ERROR:
		return "error"
	case SILENT:
		return "silent"
	case PANIC:
		return "panic"
	default:
		return "Unknown log level, indicates a bug. Please report"
	}
}

func Printf(format string, v ...any) (int, error) {
	if level < SILENT {
		return printMultiline(normalLogger, ansiNone, fmt.Sprintf(format, v...)), nil
	}
	return 0, nil
}

func Print(a ...any) (int, error) {
	if level < SILENT {
		return printMultiline(normalLogger, ansiNone, fmt.Sprint(a...)), nil
	}
	return 0, nil
}

func Println(a ...any) (int, error) {
	if level < SILENT {
		return printMultiline(normalLogger, ansiNone, fmt.Sprintln(a...)), nil
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
