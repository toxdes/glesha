package L

import (
	"fmt"
	"log"
	"strings"
)

// ANSI foreground color codes
const (
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
	ansiReset  = "\033[0m"
	ansiNone   = ""
)

// color modes
type ColorMode int

const (
	COLOR_MODE_AUTO ColorMode = iota
	COLOR_MODE_ALWAYS
	COLOR_MODE_NEVER
)

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

func SetColorModeFromString(colorModeStr string) error {
	switch strings.ToLower(colorModeStr) {
	case "always":
		_ = SetColorMode(COLOR_MODE_ALWAYS)
	case "never":
		_ = SetColorMode(COLOR_MODE_NEVER)
	case "auto":
		_ = SetColorMode(COLOR_MODE_AUTO)
	default:
		return fmt.Errorf("unsupported color mode: %s", colorModeStr)
	}
	return nil
}

func SetColorMode(cm ColorMode) error {
	switch cm {
	case COLOR_MODE_ALWAYS, COLOR_MODE_NEVER, COLOR_MODE_AUTO:
		colorMode = cm
		updateLoggerPrefixColors()
	default:
		return fmt.Errorf("unsupported color mode: %s", cm.String())
	}
	return nil
}

func GetColorMode() ColorMode {
	return colorMode
}

// renders text with ansi color code if colors are enabled
func colorize(s string, color string) string {
	if color == "" || !shouldUseColors(colorMode) {
		return s
	}
	return color + s + ansiReset
}

// updates all logger prefixes by colorizing them
func updateLoggerPrefixColors() {
	debugLogger.SetPrefix(colorize(debugPrefix, ansiBlue))
	infoLogger.SetPrefix(colorize(infoPrefix, ansiGreen))
	normalLogger.SetPrefix(colorize(normalPrefix, ansiNone))
	warnLogger.SetPrefix(colorize(warnPrefix, ansiYellow))
	errorLogger.SetPrefix(colorize(errorPrefix, ansiRed))
	panicLogger.SetPrefix(colorize(panicPrefix, ansiRed))
}

// returns logger and color code for log level
func getLoggerAndStyle(l LogLevel) (*log.Logger, string) {
	switch l {
	case DEBUG:
		return debugLogger, ansiBlue
	case INFO:
		return infoLogger, ansiGreen
	case NORMAL:
		return normalLogger, ansiNone
	case WARN:
		return warnLogger, ansiYellow
	case ERROR:
		return errorLogger, ansiRed
	case PANIC:
		return panicLogger, ansiRed
	default:
		return infoLogger, ansiNone
	}
}
