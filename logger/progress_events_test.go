package L

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestEventProgressPreservesConcurrentIdentity(t *testing.T) {
	var events []ProgressEvent
	r := NewEventProgressRenderer(func(e ProgressEvent) error { events = append(events, e); return nil })
	ctx := WithProgress(context.Background(), r)
	a, b := StartProgress(ctx, "upload", 1024), StartProgress(ctx, "upload", -1)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 256; j++ {
				a.Add(1)
			}
		}()
	}
	wg.Wait()
	b.Add(7)
	r.draw()
	a.Finish()
	b.Finish()
	r.Close()
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 || events[0].ID == events[1].ID {
		t.Fatal(events)
	}
	for _, event := range events {
		if event.ID == a.id && event.TotalBytes == nil {
			t.Fatal("lost total")
		}
		if event.ID == b.id && event.TotalBytes != nil {
			t.Fatal("invented total")
		}
		if event.State == "STOPPED" && event.ID == a.id && event.CompletedBytes != 1024 {
			t.Fatal(event)
		}
	}
}
func TestProgressSinkFailureIsRetained(t *testing.T) {
	sentinel := errors.New("closed writer")
	r := NewEventProgressRenderer(func(ProgressEvent) error { return sentinel })
	p := StartProgress(WithProgress(context.Background(), r), "upload", 1)
	p.Finish()
	r.Close()
	if !errors.Is(r.Err(), sentinel) {
		t.Fatal(r.Err())
	}
}
func TestPlainProgressNeverRewritesLines(t *testing.T) {
	var out bytes.Buffer
	r := NewPlainProgressRenderer(&out)
	p := StartProgress(WithProgress(context.Background(), r), "upload", 10)
	p.Add(5)
	r.draw()
	p.Finish()
	r.Close()
	if out.String() != "[+] upload\n" || strings.ContainsAny(out.String(), "\r\x1b") {
		t.Fatal(out.String())
	}
}
