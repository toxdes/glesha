package L

import (
	"fmt"
	"log"
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

func Debug(v ...interface{}) {
	if level <= DEBUG {
		_, file, line, _ := runtime.Caller(1)
		debugLogger.Printf("%s:%d", file, line)
		debugLogger.Println(fmt.Sprint(v...))
	}
}

func Info(v ...interface{}) {
	if level <= INFO {
		infoLogger.Println(v...)
	}
}

func Warn(v ...interface{}) {
	if level <= WARN {
		warnLogger.Println(v...)
	}
}

func Error(v ...interface{}) {
	if level <= ERROR {
		// _, file, line, _ := runtime.Caller(1)
		// errorLogger.Printf("%s:%d", file, line)
		errorLogger.Println(fmt.Sprint(v...))
		errorLogger.Println(v...)
	}
}

func Fatal(v ...interface{}) {
	errorLogger.Println(v...)
	os.Exit(1)
}
