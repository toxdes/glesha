package L

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

type progressKey struct{}
type progressLabelKey struct{}
type uploadProgressKey struct{}

// counters never perform terminal I/O; one renderer refreshes every 100ms
type Progress struct {
	bytes    atomic.Int64
	label    string
	total    int64
	start    time.Time
	renderer *ProgressRenderer
	finished bool
}

type ProgressRenderer struct {
	mu     sync.Mutex
	out    io.Writer
	color  bool
	active map[*Progress]struct{}
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
	width  int
}

func NewProgressRenderer(out io.Writer, color bool) *ProgressRenderer {
	r := &ProgressRenderer{out: out, color: color, active: make(map[*Progress]struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	if out == os.Stderr {
		state.Lock()
		state.progress = r
		state.Unlock()
	}
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.draw()
			case <-r.stop:
				return
			}
		}
	}()
	return r
}
func WithProgress(ctx context.Context, renderer *ProgressRenderer) context.Context {
	return context.WithValue(ctx, progressKey{}, renderer)
}
func WithProgressLabel(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, progressLabelKey{}, label)
}
func WithUploadProgress(ctx context.Context, progress *Progress) context.Context {
	return context.WithValue(ctx, uploadProgressKey{}, progress)
}
func UploadProgress(ctx context.Context, fallback *Progress) *Progress {
	if progress, ok := ctx.Value(uploadProgressKey{}).(*Progress); ok {
		return progress
	}
	return fallback
}

// a negative total marks work without a known byte count
func StartProgress(ctx context.Context, label string, total int64) *Progress {
	if override, ok := ctx.Value(progressLabelKey{}).(string); ok {
		label = override
	}
	Debug("starting " + strings.ToLower(label))
	r, _ := ctx.Value(progressKey{}).(*ProgressRenderer)
	if r == nil {
		return nil
	}
	p := &Progress{label: ASCII(label), total: total, start: time.Now(), renderer: r}
	r.mu.Lock()
	newStep := true
	for active := range r.active {
		if active.label == p.label {
			newStep = false
			break
		}
	}
	if newStep {
		r.clearLocked()
		text := "[+] " + p.label
		r.width = utf8.RuneCountInString(text)
		if r.color {
			text = "\x1b[36m" + text + "\x1b[0m"
		}
		fmt.Fprint(r.out, text)
	}
	r.active[p] = struct{}{}
	r.mu.Unlock()
	return p
}
func (p *Progress) Add(n int64) {
	if p != nil && n > 0 {
		p.bytes.Add(n)
	}
}
func (p *Progress) Finish() {
	if p == nil {
		return
	}
	r := p.renderer
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.finished {
		return
	}
	p.finished = true
	groupActive := false
	for other := range r.active {
		if other.label == p.label && !other.finished {
			groupActive = true
			break
		}
	}
	if !groupActive {
		r.clearLocked()
		text := "[+] " + p.label
		if r.color {
			text = "\x1b[36m" + text + "\x1b[0m"
		}
		fmt.Fprintln(r.out, text)
		for other := range r.active {
			if other.label == p.label {
				delete(r.active, other)
			}
		}
	}
	if len(r.active) == 0 {
		r.clearLocked()
	}
}
func (p *Progress) Reader(r io.Reader) io.Reader {
	if p == nil {
		return r
	}
	return progressReader{r, p}
}

type progressReader struct {
	reader   io.Reader
	progress *Progress
}

func (r progressReader) Read(b []byte) (int, error) {
	n, err := r.reader.Read(b)
	r.progress.Add(int64(n))
	return n, err
}
func (r *ProgressRenderer) Close() {
	r.once.Do(func() {
		close(r.stop)
		<-r.done
		r.clear()
		state.Lock()
		if state.progress == r {
			state.progress = nil
		}
		state.Unlock()
	})
}
func (r *ProgressRenderer) draw() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.active) == 0 {
		return
	}
	var selected *Progress
	for p := range r.active {
		if !p.finished && (selected == nil || p.start.After(selected.start)) {
			selected = p
		}
	}
	if selected == nil {
		return
	}
	var bytes, total int64
	label := selected.label
	known := true
	start := selected.start
	count := 0
	for p := range r.active {
		if p.label != selected.label {
			continue
		}
		count++
		bytes += p.bytes.Load()
		if p.total < 0 {
			known = false
		} else {
			total += p.total
		}
		if p.start.Before(start) {
			start = p.start
		}
	}
	if count > 1 {
		label = fmt.Sprintf("%s (%d jobs)", label, count)
	}
	width := 79
	if file, ok := r.out.(*os.File); ok {
		if w, _, err := term.GetSize(int(file.Fd())); err == nil && w > 1 {
			width = max(1, w*4/5)
		}
	}
	elapsed := time.Since(start)
	text := progressLineWidth(label, bytes, total, known, elapsed, width)
	r.clearLocked()
	r.width = utf8.RuneCountInString(text)
	if r.color {
		text = "\x1b[36m" + text + "\x1b[0m"
	}
	fmt.Fprint(r.out, text)
}
func progressLine(label string, bytes, total int64, known bool, elapsed time.Duration) string {
	return progressLineWidth(label, bytes, total, known, elapsed, 0)
}
func progressLineWidth(label string, bytes, total int64, known bool, elapsed time.Duration, lineWidth int) string {
	label = ASCII(label)
	width := 16
	bar := ""
	amount := Bytes(bytes)
	rate := float64(0)
	if elapsed > 0 {
		rate = float64(bytes) / elapsed.Seconds()
	}
	known = known && total >= 0
	if !known {
		frames := `-\|/`
		frame := frames[max(0, int(elapsed/(100*time.Millisecond)))%len(frames)]
		marker := ""
		if label == "Scanning" || strings.HasPrefix(label, "Scanning (") {
			marker = fmt.Sprintf(" [%c]", frame)
		}
		details := ""
		if bytes > 0 {
			details = fmt.Sprintf("  %s  %s/s", amount, byteAmount(rate))
		}
		if lineWidth > 0 {
			space := lineWidth - len(details) - len(marker) - 4
			if space < 4 {
				details = ""
				space = lineWidth - len(marker) - 4
			}
			if len(label) > max(0, space) {
				if space >= 4 {
					label = label[:space-3] + "..."
				} else {
					label = ""
				}
			}
		}
		text := "[+] " + label + marker + details
		if lineWidth > 0 && lineWidth < 8 && marker != "" {
			text = string(frame)
		}
		if lineWidth > 0 {
			return text[:min(len(text), lineWidth)]
		}
		return text
	}
	ratio := float64(1)
	if total > 0 {
		ratio = math.Min(1, math.Max(0, float64(bytes)/float64(total)))
	}
	amount = fmt.Sprintf("%s / %s %.2f%%", amount, Bytes(total), ratio*100)
	suffix := fmt.Sprintf("%s  %s/s", amount, byteAmount(rate))
	if lineWidth > 0 {
		labelSpace := lineWidth - len(suffix) - 9 - 8
		if labelSpace < 4 {
			text := "[+] " + suffix
			if len(text) > lineWidth {
				text = fmt.Sprintf("[+] %.2f%% %s/s", ratio*100, byteAmount(rate))
			}
			return text[:min(len(text), lineWidth)]
		}
		if len(label) > labelSpace {
			label = label[:labelSpace-3] + "..."
		}
		width = lineWidth - len(label) - len(suffix) - 8
	}
	fill := int(ratio * float64(width))
	bar = strings.Repeat("#", fill) + strings.Repeat(" ", width-fill)
	return fmt.Sprintf("[+] %s [%s] %s", label, bar, suffix)
}
func Bytes(n int64) string {
	return byteAmount(float64(n))
}
func byteAmount(value float64) string {
	if value < 1024 {
		return fmt.Sprintf("%.2f B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	for i, unit := range units {
		value /= 1024
		if value < 1024 || i == len(units)-1 {
			return fmt.Sprintf("%.2f %s", value, unit)
		}
	}
	return ""
}

func (r *ProgressRenderer) clearLocked() {
	if r.width > 0 {
		fmt.Fprintf(r.out, "\r%s\r", strings.Repeat(" ", r.width))
		r.width = 0
	}
}
func (r *ProgressRenderer) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearLocked()
}
