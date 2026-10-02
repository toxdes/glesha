package L

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestColorModesAndReadableBytes(t *testing.T) {
	for _, test := range []struct {
		input string
		want  ColorMode
	}{
		{"auto", COLOR_MODE_AUTO}, {"yes", COLOR_MODE_ALWAYS}, {"no", COLOR_MODE_NEVER},
		{"always", COLOR_MODE_ALWAYS}, {"never", COLOR_MODE_NEVER},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseColorMode(test.input)
			if err != nil || got != test.want {
				t.Fatal(got, err)
			}
		})
	}
	for _, test := range []struct {
		bytes int64
		want  string
	}{
		{0, "0.00 B"}, {1023, "1023.00 B"}, {1024, "1.00 KiB"}, {5 << 20, "5.00 MiB"}, {1 << 40, "1.00 TiB"},
	} {
		if got := Bytes(test.bytes); got != test.want {
			t.Fatalf("%d: %s", test.bytes, got)
		}
	}
}
func TestConcurrentProgressAndCleanShutdown(t *testing.T) {
	var output bytes.Buffer
	r := NewProgressRenderer(&output, false)
	ctx := WithProgress(context.Background(), r)
	p := StartProgress(ctx, "Uploading", 1024)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 128; j++ {
				p.Add(1)
			}
		}()
	}
	wg.Wait()
	r.draw()
	p.Finish()
	r.Close()
	r.Close()
	if p.bytes.Load() != 1024 {
		t.Fatal("lost byte updates")
	}
	if !strings.Contains(output.String(), "1.00 KiB / 1.00 KiB 100.00%") {
		t.Fatal(output.String())
	}
	if strings.Contains(output.String(), "\x1b") {
		t.Fatal("color-disabled progress contains ANSI escapes")
	}
	select {
	case <-r.done:
	default:
		t.Fatal("renderer still running")
	}
}

func TestStepHeadingAndProgressShareOneLine(t *testing.T) {
	var output bytes.Buffer
	r := NewProgressRenderer(&output, false)
	defer r.Close()
	p := StartProgress(WithProgress(context.Background(), r), "Archiving", 1024)
	p.Add(512)
	r.draw()
	r.mu.Lock()
	live := output.String()
	r.mu.Unlock()
	if strings.Contains(live, "\n") || !strings.Contains(live, "512.00 B / 1.00 KiB 50.00%") {
		t.Fatal("heading occupies a separate line from progress", live)
	}
	p.Finish()
	p.Finish()
	r.Close()
	if strings.Count(output.String(), "[+] Archiving\n") != 1 || strings.Count(output.String(), "\n") != 1 {
		t.Fatal("step completion was printed more than once", output.String())
	}
}
func TestTransferProgressPrecisionAndRate(t *testing.T) {
	for _, label := range []string{"Uploading to b2", "Downloading"} {
		t.Run(label, func(t *testing.T) {
			line := progressLine(label, 5<<20, 10<<20, true, 2*time.Second)
			if !strings.HasPrefix(line, "[+] "+label) || !strings.Contains(line, "5.00 MiB / 10.00 MiB 50.00%") || !strings.HasSuffix(line, "2.50 MiB/s") {
				t.Fatal(line)
			}
		})
	}
	if line := progressLine("Downloading", 1, 2, true, 2*time.Second); !strings.HasSuffix(line, "0.50 B/s") {
		t.Fatal("fractional bandwidth lost", line)
	}
}

func TestProgressReaderPreservesBytesAndUnknownTotal(t *testing.T) {
	var output bytes.Buffer
	r := NewProgressRenderer(&output, false)
	defer r.Close()
	p := StartProgress(WithProgress(context.Background(), r), "Scanning", -1)
	defer p.Finish()
	data, err := io.ReadAll(p.Reader(strings.NewReader("content")))
	if err != nil || string(data) != "content" || p.bytes.Load() != 7 {
		t.Fatal(string(data), err)
	}
	line := progressLine("Archiving", 7, 0, false, time.Second)
	if strings.Contains(line, "%") || !strings.Contains(line, "7.00 B") {
		t.Fatal(line)
	}
	var disabled *Progress
	input := strings.NewReader("value")
	if disabled.Reader(input) != input {
		t.Fatal("disabled progress adds a reader wrapper")
	}
}

func TestOnlyScanningUsesASCIISpinner(t *testing.T) {
	for _, label := range []string{"Scanning", "Scanning (2 jobs)"} {
		t.Run(label, func(t *testing.T) {
			for i, frame := range []string{"[-]", `[\]`, "[|]", "[/]", "[-]"} {
				line := progressLine(label, 1024, 0, false, time.Duration(i)*100*time.Millisecond)
				if !strings.Contains(line, frame) || strings.ContainsAny(line, ">=#%") || !strings.Contains(line, "1.00 KiB") {
					t.Fatal("incorrect spinner frame", line)
				}
			}
		})
	}
	for _, label := range []string{"Finalizing archive", "Publishing catalog to b2"} {
		line := progressLine(label, 1024, -1, false, time.Second)
		if strings.ContainsAny(line[len("[+] "):], "[]#%") {
			t.Fatal("non-scanning stage contains an indefinite bar", line)
		}
	}
	for _, width := range []int{1, 3, 8, 24, 64, 80, 120} {
		line := progressLineWidth("Finalizing archive", 0, 0, false, time.Second, width)
		if len(line) > width || strings.Contains(line, "0.00") || strings.Contains(line, "/s") {
			t.Fatal("idle spinner contains meaningless byte counters", width, line)
		}
	}
}

func TestEmptyArchiveHasKnownZeroTotal(t *testing.T) {
	line := progressLine("Archiving", 0, 0, true, time.Second)
	if !strings.Contains(line, "0.00 B / 0.00 B 100.00%") || !strings.Contains(line, "################") {
		t.Fatal("empty archive was treated as indefinite", line)
	}
}

// use the same buffered I/O path for both cases, without ReaderFrom/WriterTo shortcuts
type benchmarkSink struct{}

func (benchmarkSink) Write(b []byte) (int, error) { return len(b), nil }
func BenchmarkProgressReader(b *testing.B) {
	data := make([]byte, 8<<20)
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		b.Run(name, func(b *testing.B) {
			buffer := make([]byte, 64<<10)
			var p *Progress
			if enabled {
				p = &Progress{}
			}
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := struct{ io.Reader }{p.Reader(bytes.NewReader(data))}
				if _, err := io.CopyBuffer(benchmarkSink{}, r, buffer); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestCompletedConcurrentTransferRemainsInGroupTotal(t *testing.T) {
	var output bytes.Buffer
	r := NewProgressRenderer(&output, false)
	defer r.Close()
	ctx := WithProgress(context.Background(), r)
	first := StartProgress(ctx, "Uploading to b2", 1024)
	second := StartProgress(ctx, "Uploading to b2", 1024)
	first.Add(1024)
	first.Finish()
	second.Add(512)
	r.draw()
	second.Finish()
	r.Close()
	if !strings.Contains(output.String(), "1.50 KiB / 2.00 KiB 75.00%") {
		t.Fatal(output.String())
	}
	if len(r.active) != 0 {
		t.Fatal("finished tasks retained")
	}
}

func TestASCIIHumanTextPreservesUnicodeAsEscapes(t *testing.T) {
	text := ASCII("music — café 😀\n")
	if strings.ContainsAny(text, "—é😀") || !strings.Contains(text, `\u2014`) || !strings.Contains(text, `\u00e9`) || !strings.Contains(text, `\U0001f600`) {
		t.Fatal(text)
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatal("formatting was lost")
	}
}

func TestProgressAdaptsToLineWidth(t *testing.T) {
	previousBar := 0
	for _, width := range []int{8, 24, 64, 80, 120, 180} {
		line := progressLineWidth("Uploading archive to b2", 5<<20, 10<<20, true, 2*time.Second, width)
		if len(line) > width || strings.Contains(line, "=") {
			t.Fatal(width, line)
		}
		if width >= 64 && (!strings.Contains(line, "5.00 MiB / 10.00 MiB 50.00%") || !strings.HasSuffix(line, "2.50 MiB/s")) {
			t.Fatal("details clipped", width, line)
		}
		if width >= 80 {
			bar := strings.Count(line, "#")
			if bar <= previousBar {
				t.Fatal("bar did not grow", width, line)
			}
			previousBar = bar
		}
	}
}

func TestGroupedUploadProgressSuppressesChildSteps(t *testing.T) {
	var output bytes.Buffer
	r := NewProgressRenderer(&output, false)
	ctx := WithProgress(context.Background(), r)
	parent := StartProgress(ctx, "Publishing catalog to b2", -1)
	childCtx := WithUploadProgress(WithProgress(ctx, nil), parent)
	child := StartProgress(childCtx, "Uploading archive to b2", 1024)
	if child != nil {
		t.Fatal("child step was not suppressed")
	}
	UploadProgress(childCtx, child).Add(512)
	UploadProgress(childCtx, child).Add(512)
	r.draw()
	parent.Finish()
	r.Close()
	if parent.bytes.Load() != 1024 || strings.Contains(output.String(), "Uploading archive") || !strings.Contains(output.String(), "1.00 KiB") {
		t.Fatal("grouped publication did not report uploaded bytes", output.String())
	}
	if UploadProgress(context.Background(), parent) != parent {
		t.Fatal("ordinary upload counter changed")
	}
}
