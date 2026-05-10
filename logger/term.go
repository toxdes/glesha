package L

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// cursor sequences
const (
	CURSOR_ESCAPE    = "\x1B"
	CURSOR_CLEAR_LINE = "\x1B[2K"
	CURSOR_UP        = "\x1B[1A"
)

// decides if colors should be enabled based on color mode and terminal capabilities
func shouldUseColors(cm ColorMode) bool {
	if cm != COLOR_MODE_AUTO {
		return cm == COLOR_MODE_ALWAYS
	}

	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("CLICOLOR") == "0" && os.Getenv("CLICOLOR_FORCE") == "" {
		return false
	}
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return false
	}
	if os.Getenv("CI") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return true
}

// removes the current footer from terminal
// must be called while holding footerMutex
func clearFooter() {
	if footerLines == 0 {
		return
	}

	for i := 0; i < footerLines; i++ {
		fmt.Printf("%s", CURSOR_UP)
	}

	for i := 0; i < footerLines; i++ {
		fmt.Printf("\r%s\n", CURSOR_CLEAR_LINE)
	}

	for i := 0; i < footerLines; i++ {
		fmt.Printf("%s", CURSOR_UP)
	}
}

// reprints the footer after a log message
// must be called while holding footerMutex
func printFooter() int {
	if len(footerText) == 0 {
		return 0
	}
	_, color := getLoggerAndStyle(footerLevel)
	lineCnt := 0
	for line := range strings.SplitSeq(footerText, "\n") {
		if len(strings.TrimSpace(line)) == 0 {
			continue
		}
		rendered := colorize(strings.TrimSpace(line), color)
		fmt.Printf("%s\n", rendered)
		lineCnt++
	}
	return lineCnt
}
