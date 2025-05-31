package L

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
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
	debugLogger = log.New(os.Stdout, colorBlue+"[DEBUG] "+colorReset, log.Lmsgprefix)
	infoLogger  = log.New(os.Stdout, colorGreen+"[INFO]  "+colorReset, log.Lmsgprefix)
	warnLogger  = log.New(os.Stdout, colorYellow+"[WARN]  "+colorReset, log.Lmsgprefix)
	errorLogger = log.New(os.Stderr, colorRed+"[ERROR] "+colorReset, log.Lmsgprefix)
)

var printCallerLineNumbers bool = false

func SetLevel(l string) {
	switch l {
	case "debug":
		level = DEBUG
	case "info":
		level = INFO
	case "warn":
		level = WARN
	case "error":
		level = ERROR
	default:
		level = INFO
	}
}

func Debug(v ...any) {
	if level <= DEBUG {
		if printCallerLineNumbers {
			_, file, line, _ := runtime.Caller(1)
			debugLogger.Printf("%s:%d", file, line)
		}
		debugLogger.Println(fmt.Sprint(v...))
	}
}

func Info(v ...any) {
	if level <= INFO {
		infoLogger.Println(v...)
	}
}

func Warn(v ...any) {
	if level <= WARN {
		warnLogger.Println(v...)
	}
}

func Error(v ...any) {
	if level <= ERROR {
		if printCallerLineNumbers {
			_, file, line, _ := runtime.Caller(1)
			errorLogger.Printf("%s:%d", file, line)
		}
		errorLogger.Println(v...)
	}
}

func Panic(v ...any) {
	errorLogger.Println(v...)
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
