package L

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
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
		return fmt.Sprintf("[%s] Status:%s\n\t\tContent: Cannot read response body %v",
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
	sb.WriteString(string(bodyBytes))
	return sb.String()
}

func Printf(format string, v ...any) (int, error) {
	if level < SILENT {
		return fmt.Printf(format, v...)
	}
	return 0, nil
}

func Print(a ...any) (int, error) {
	if level < SILENT {
		return fmt.Print(a...)
	}
	return 0, nil
}

func Println(a ...any) (int, error) {
	if level < SILENT {
		return fmt.Println(a...)
	}
	return 0, nil
}

// progressPercentage should be a float64 between 0.0 and 100.0 (inclusive).
func ProgressBar(progressPercentage float64) string {
	const barWidth = 24
	fraction := progressPercentage / 100.0
	fraction = max(fraction, 0.0)
	fraction = min(fraction, 1.0)

	filledWidth := int(float64(barWidth) * fraction)
	emptyWidth := barWidth - filledWidth

	filledSymbol := strings.Repeat("█", filledWidth)
	emptySymbol := strings.Repeat("░", emptyWidth)

	// Combine into the final string
	return fmt.Sprintf("%s%s", filledSymbol, emptySymbol)
}

func Line(width int) string {
	return strings.Repeat("-", width)
}

type TruncateMode int

const (
	TRUNC_RIGHT  TruncateMode = iota // Truncate from the right: ... at the end
	TRUNC_LEFT                       // Truncate from the left; ... at the beginning
	TRUNC_CENTER                     // Truncate from the center: ... in the middle
)

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

	runes := []rune(input) // Convert to slice of runes for easy indexing

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

// HumanReadableTime converts a duration in milliseconds to a human-readable string.
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
