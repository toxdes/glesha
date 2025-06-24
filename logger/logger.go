package L

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
)

var (
	level       = INFO
	debugLogger = log.New(os.Stdout, colorBlue+"[DEBUG] ", log.Lmsgprefix)
	infoLogger  = log.New(os.Stdout, colorGreen+"[INFO]  ", log.Lmsgprefix)
	warnLogger  = log.New(os.Stdout, colorYellow+"[WARN]  ", log.Lmsgprefix)
	errorLogger = log.New(os.Stderr, colorRed+"[ERROR] ", log.Lmsgprefix)
)

var printCallerLocation bool = true

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
	default:
		return fmt.Errorf("Unsupported log level: %s", l)
	}
	return nil
}

func SetLevel(l LogLevel) error {
	switch l {
	case DEBUG, INFO, WARN, ERROR:
		level = l
	default:
		fmt.Errorf("Unsupported log level: %d", l)
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

func HumanReadableBytes(bytes uint64) string {
	if bytes == 0 {
		return "0 B"
	}
	val := float64(bytes)
	suffixes := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	unit := float64(1024)
	i := 0
	for val >= unit && i < len(suffixes)-1 {
		val /= unit
		i += 1
	}
	return fmt.Sprintf("%.2f%s", val, suffixes[i])
}

func HttpResponseString(resp *http.Response) string {

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("[%s] Status:%s\n\t\tContent: Cannot read response body %v", resp.Request.URL.String(), resp.Status, err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return fmt.Sprintf("[%s] Status: %s\n Content: %s", resp.Request.URL.String(), resp.Status, string(bodyBytes))
}

func IsVerbose() bool {
	return level == DEBUG
}

func GetLogLevel() LogLevel {
	return level
}

func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "default"
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
