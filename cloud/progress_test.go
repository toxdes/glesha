package cloud

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"glesha/config"
	L "glesha/logger"
)

type progressTestClient struct {
	do func(*http.Request) (*http.Response, error)
}

type progressOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (p *progressOutput) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buffer.Write(b)
}

func (p *progressOutput) contains(text string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Contains(p.buffer.String(), text)
}

func TestSDKUploadDisplaysBytesBeforeAcknowledgement(t *testing.T) {
	t.Setenv("B2_KEY_ID", "test")
	t.Setenv("B2_APPLICATION_KEY", "test")
	data := bytes.Repeat([]byte("x"), (128<<10)+511)
	file := filepath.Join(t.TempDir(), "archive.gpg")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	read := make(chan struct{}, 1)
	acknowledge := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(acknowledge) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(body, data) {
				t.Error("SDK body changed", err)
			}
			read <- struct{}{}
			<-acknowledge
			w.Header().Set("ETag", `"test-etag"`)
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("ETag", `"test-etag"`)
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()
	defer release()
	var output progressOutput
	renderer := L.NewProgressRenderer(&output, false)
	defer renderer.Close()
	ctx, cancel := context.WithTimeout(L.WithProgress(context.Background(), renderer), 5*time.Second)
	defer cancel()
	store, err := New(ctx, "b2", config.Provider{Bucket: "test", Region: "test", Endpoint: server.URL}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := store.Put(ctx, "archive", file, "STANDARD")
		result <- err
	}()
	select {
	case <-read:
	case <-ctx.Done():
		t.Fatal("upload did not reach server")
	}
	for !output.contains("128.50 KiB / 128.50 KiB") {
		select {
		case <-ctx.Done():
			t.Fatal("progress waited for upload acknowledgement")
		case <-time.After(10 * time.Millisecond):
		}
	}
	release()
	if err = <-result; err != nil {
		t.Fatal(err)
	}
}

func (c progressTestClient) Do(r *http.Request) (*http.Response, error) { return c.do(r) }

func TestUploadProgressExcludesSigningAndRetryBytes(t *testing.T) {
	ctx, body := trackUpload(context.Background(), strings.NewReader("1234567890"), &L.Progress{})
	r := body.(*uploadReader)
	io.Copy(io.Discard, body)
	if r.highWater != 0 {
		t.Fatal("signing read counted")
	}
	body.Seek(0, io.SeekStart)
	client := progressHTTPClient{client: progressTestClient{do: func(request *http.Request) (*http.Response, error) {
		_, err := io.CopyN(io.Discard, request.Body, 3)
		return nil, err
	}}}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPut, "http://test.invalid", nil)
	request.Body = io.NopCloser(body)
	client.Do(request)
	if r.highWater != 3 || r.sending.Load() {
		t.Fatal("in-flight bytes not tracked", r.highWater)
	}
	body.Seek(0, io.SeekStart)
	client.Do(request)
	if r.highWater != 3 {
		t.Fatal("retry inflated progress")
	}
	client.client = progressTestClient{do: func(request *http.Request) (*http.Response, error) {
		_, err := io.Copy(io.Discard, request.Body)
		return nil, err
	}}
	client.Do(request)
	if r.highWater != 10 {
		t.Fatal("remaining bytes not counted")
	}
	_, untouched := trackUpload(context.Background(), strings.NewReader("x"), nil)
	if _, wrapped := untouched.(*uploadReader); wrapped {
		t.Fatal("disabled progress wraps body")
	}
}
