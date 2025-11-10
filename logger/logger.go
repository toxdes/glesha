package L

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
)

type LogLevel int

var printCallerLocation bool = true

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	SILENT
)

var (
	level       = INFO
	debugLogger = log.New(os.Stdout, colorBlue+"=> ", log.Lmsgprefix)
	infoLogger  = log.New(os.Stdout, colorGreen+"=> ", log.Lmsgprefix)
	warnLogger  = log.New(os.Stdout, colorYellow+"=> ", log.Lmsgprefix)
	errorLogger = log.New(os.Stderr, colorRed+"=> ", log.Lmsgprefix)
)

// cursor sequences
const (
	C_ESCAPE     string = "\x1B"
	C_SAVE              = C_ESCAPE + "7"
	C_RESTORE           = C_ESCAPE + "8"
	C_CLEAR_LINE        = C_ESCAPE + "[2K"
	C_UP                = C_ESCAPE + "[1A"
	C_DOWN              = C_ESCAPE + "[1B"
	C_RIGHT             = C_ESCAPE + "[1C"
	C_LEFT              = C_ESCAPE + "[1D"
)

// colors
const (
	colorReset  string = C_ESCAPE + "[0m"
	colorRed           = C_ESCAPE + "[31m"
	colorGreen         = C_ESCAPE + "[32m"
	colorYellow        = C_ESCAPE + "[33m"
	colorBlue          = C_ESCAPE + "[34m"
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
	case "silent":
		level = SILENT
	default:
		return fmt.Errorf("unsupported log level: %s", l)
	}
	return nil
}

func SetLevel(l LogLevel) error {
	switch l {
	case DEBUG, INFO, WARN, ERROR, SILENT:
		level = l
	default:
		return fmt.Errorf("unsupported log level: %d", l)
	}
	return nil
}

func Debug(v ...any) {
	if level <= DEBUG {
		if printCallerLocation {
			_, file, line, _ := runtime.Caller(1)
			debugLogger.Printf("%s:%d: %s%s", file, line, fmt.Sprint(v...), colorReset)
		} else {
			debugLogger.Print(fmt.Sprint(v...), colorReset)
		}
	}
}

func Info(v ...any) {
	if level <= INFO {
		infoLogger.Print(fmt.Sprint(v...), colorReset)
	}
}

func Warn(v ...any) {
	if level <= WARN {
		warnLogger.Print(fmt.Sprint(v...), colorReset)
	}
}

func Error(v ...any) {
	if level <= ERROR {
		if printCallerLocation {
			_, file, line, _ := runtime.Caller(1)
			errorLogger.Printf("%s:%d: - %s%s", file, line, fmt.Sprint(v...), colorReset)
		} else {
			errorLogger.Print(fmt.Sprint(v...), colorReset)
		}
	}
}

func Panic(v ...any) {
	errorLogger.Print(fmt.Sprint(v...), colorReset)
	os.Exit(1)
}

func GetLogLevel() LogLevel {
	return level
}

func IsVerbose() bool {
	return level < WARN
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
	default:
		return "Unknown log level, indicates a bug. Please report"
	}
}

func HumanReadableCount(
	count int,
	singular string,
	plural string,
) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}
