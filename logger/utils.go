package L

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func HumanReadableBytes(bytes uint64, precision int) string {
	if bytes == 0 {
		return "0 B"
	}
	if precision <= 0 {
		precision = 2
	}
	val := float64(bytes)
	suffixes := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	unit := float64(1024)
	i := 0
	for val >= unit && i < len(suffixes)-1 {
		val /= unit
		i += 1
	}
	return fmt.Sprintf("%.*f%s", precision, val, suffixes[i])
}

func HttpResponseString(resp *http.Response) string {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("[%s] Status:%s\n    Content: Cannot read response body %v",
			resp.Request.URL.String(),
			resp.Status, err)
	}

	var sb strings.Builder
	sb.WriteString("\n---Req---\n")
	sb.WriteString(fmt.Sprintf("URL:%s\n", resp.Request.URL))
	sb.WriteString("\n---Req. Headers---\n")
	for key, values := range resp.Request.Header {
		sb.WriteString(fmt.Sprintf("%s : ", key))
		for _, value := range values {
			sb.WriteString(value)
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("Resp. Status: %d", resp.StatusCode))
	sb.WriteString("\n---Resp. Headers---\n")
	for key, values := range resp.Header {
		sb.WriteString(fmt.Sprintf("%s : ", key))
		for _, value := range values {
			sb.WriteString(value)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n---Resp. Body---\n")
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	sb.Write(bodyBytes)
	return sb.String()
}

// progressPercentage should be a float64 between 0.0 and 100.0 (inclusive).
func ProgressBar(progressPercentage float64, barWidth int) string {
	if barWidth <= 0 {
		barWidth = 16
	}
	fraction := progressPercentage / 100.0
	fraction = max(fraction, 0.0)
	fraction = min(fraction, 1.0)

	filledWidth := int(float64(barWidth) * fraction)
	emptyWidth := barWidth - filledWidth

	filledSymbol := strings.Repeat("█", filledWidth)
	emptySymbol := strings.Repeat("░", emptyWidth)

	return fmt.Sprintf("%s%s", filledSymbol, emptySymbol)
}

func Line(width int) string {
	return strings.Repeat("-", width)
}

type TruncateMode int

const (
	TRUNC_RIGHT  TruncateMode = iota // Truncate from the right: ellipsis will be at the end
	TRUNC_LEFT                       // Truncate from the left; ellipsis will be at the beginning
	TRUNC_CENTER                     // Truncate from the center: ... will be in the middle
)

// returns truncated string for the given "input" string, according to truncate "mode"
func TruncateString(input string, maxLen int, mode TruncateMode) string {
	ellipsis := "..."
	inputLen := utf8.RuneCountInString(input)
	ellipsisLen := utf8.RuneCountInString(ellipsis)

	if maxLen < 0 {
		return ""
	}
	if inputLen <= maxLen {
		return input
	}

	if maxLen < ellipsisLen {
		return string([]rune(ellipsis)[:maxLen])
	}

	runes := []rune(input)

	switch mode {
	case TRUNC_RIGHT:
		return string(runes[:maxLen-ellipsisLen]) + ellipsis

	case TRUNC_LEFT:
		return ellipsis + string(runes[inputLen-(maxLen-ellipsisLen):])

	case TRUNC_CENTER:
		halfLen := (maxLen - ellipsisLen) / 2
		leftPart := string(runes[:halfLen])
		rightPart := string(runes[inputLen-(maxLen-ellipsisLen-halfLen):])
		return leftPart + ellipsis + rightPart

	default:
		return string(runes[:maxLen-ellipsisLen]) + ellipsis
	}
}

// converts a duration in milliseconds to a human-readable string.
// Examples: "1h 5m", "1h 50s", "1m 5s", "1m".
func HumanReadableTime(millis int64) string {
	if millis < 0 {
		return fmt.Sprintf("-%s", HumanReadableTime(-millis))
	}
	if millis == 0 {
		return "0s"
	}

	d := time.Duration(millis) * time.Millisecond
	// TODO: show days as well
	hours := int64(d / time.Hour)
	d %= time.Hour
	minutes := int64(d / time.Minute)
	d %= time.Minute
	seconds := int64(d / time.Second)
	d %= time.Second
	ms := int64(d / time.Millisecond)

	parts := []string{}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	if ms > 0 || (len(parts) == 0 && millis > 0) {
		parts = append(parts, fmt.Sprintf("%dms", ms))
	}

	return strings.Join(parts, " ")
}

// returns "count" as a string with correct "singular" or "plural" unit
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

// decides if colors should be enabled based on "cm" and terminal color profile
func shouldUseColors(cm ColorMode) bool {
	if cm != COLOR_MODE_AUTO {
		return cm == COLOR_MODE_ALWAYS
	}
	return lipgloss.DefaultRenderer().ColorProfile() != termenv.Ascii
}

// renders "s" with "colorStyle" if colors are enabled, otherwise with noColorStyle
func colorize(s string, colorStyle *lipgloss.Style) string {
	if shouldUseColors(colorMode) {
		return colorStyle.Render(s)
	}
	return noColorStyle.Render(s)
}

// prints input with mulitple lines with stripped spaces and with prefix for each line
// returns number of characters printed
func printMultiline(l *log.Logger, style *lipgloss.Style, v ...any) int {
	message := fmt.Sprint(v...)

	// Track if original message ended with newline
	endsWithNewline := strings.HasSuffix(message, "\n")

	// Get prefix from logger
	prefix := l.Prefix()
	writer := l.Writer()

	// keep track of actual characters printed for return value
	cnt := 0
	lines := strings.Split(message, "\n")

	for i, line := range lines {
		stripped := strings.TrimSpace(line)

		// Skip leading/trailing empty lines
		if len(stripped) == 0 && (i == 0 || i == len(lines)-1) {
			continue
		}

		// print prefix + styled message
		fmt.Fprint(writer, prefix, colorize(stripped, style))
		cnt += len(stripped)

		// Add newline if:
		// - Not first/last line OR
		// - Last line AND original message ended with \n
		if (i > 0 && i < len(lines)-1) || endsWithNewline {
			fmt.Fprint(writer, "\n")
		}
	}
	return cnt
}

// appends "rel_path:line" to the log msg and prints the result to logger "l" with style "s"
func printWithCallerLocation(l *log.Logger, s *lipgloss.Style, v ...any) int {
	_, file, line, _ := runtime.Caller(2)
	cwd, err := os.Getwd()
	relPath := file
	if err == nil {
		rp, err := filepath.Rel(cwd, file)
		if err == nil {
			relPath = rp
		}
	}

	message := fmt.Sprint(v...)
	msg := fmt.Sprintf("%s:%d %s", relPath, line, message)

	return printMultiline(l, s, msg)
}

// updates all logger prefixes by colorizing them with their styles
// ideally this should be called whenever 'colorMode' changes
func updateLoggerPrefixColors() {
	debugLogger.SetPrefix(colorize(debugPrefix, &debugStyle))
	infoLogger.SetPrefix(colorize(infoPrefix, &infoStyle))
	normalLogger.SetPrefix(colorize(normalPrefix, &noColorStyle))
	warnLogger.SetPrefix(colorize(warnPrefix, &warnStyle))
	errorLogger.SetPrefix(colorize(errorPrefix, &errorStyle))
	panicLogger.SetPrefix(colorize(panicPrefix, &errorStyle))
}

// returns logger,style for LogLevel "l"
func getLoggerAndStyle(l LogLevel) (*log.Logger, *lipgloss.Style) {
	switch l {
	case DEBUG:
		return debugLogger, &debugStyle
	case INFO:
		return infoLogger, &infoStyle
	case NORMAL:
		return normalLogger, &noColorStyle
	case WARN:
		return warnLogger, &warnStyle
	case ERROR:
		return errorLogger, &errorStyle
	case PANIC:
		return panicLogger, &errorStyle
	default:
		return infoLogger, &noColorStyle
	}
}

// removes the current footer from terminal
// Must be called while holding footerMutex
func clearFooter() {
	if footerLines == 0 {
		return
	}

	// Move cursor up to start of footer
	for i := 0; i < footerLines; i++ {
		fmt.Printf("%s", c_up)
	}

	// Clear each footer line
	for i := 0; i < footerLines; i++ {
		fmt.Printf("\r%s\n", c_clear_line)
	}

	// Move cursor back up to where footer started
	for i := 0; i < footerLines; i++ {
		fmt.Printf("%s", c_up)
	}
}

// reprints the footer after a log message
// must be called while holding `footerMutex`
func printFooter() int {
	if len(footerText) == 0 {
		return 0
	}
	_, style := getLoggerAndStyle(footerLevel)
	lineCnt := 0
	for line := range strings.SplitSeq(footerText, "\n") {
		rendered := style.Render(strings.TrimSpace(line))
		fmt.Printf("%s\n", rendered)
		lineCnt++
	}
	return lineCnt
}
