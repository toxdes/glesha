package cloud

import (
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

	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"glesha/config"
)

func TestConditionalUploadAndVerifiedBytes(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	var data []byte
	var hash string
	var mu sync.Mutex
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodHead:
			if data == nil {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.Header().Set("ETag", `"etag"`)
			w.Header().Set("x-amz-version-id", "v1")
			w.Header().Set("x-amz-meta-sha256", hash)
		case http.MethodPut:
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("new AWS object not conditional")
			}
			puts++
			data, _ = io.ReadAll(r.Body)
			hash = r.Header.Get("x-amz-meta-sha256")
			w.Header().Set("ETag", `"etag"`)
			w.Header().Set("x-amz-version-id", "v1")
		default:
			t.Errorf("unexpected %s", r.Method)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "archive")
	os.WriteFile(file, []byte("complete archive"), 0600)
	store, err := New(context.Background(), "aws", config.Provider{Bucket: "test", Region: "test", Endpoint: server.URL}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Create(context.Background(), store, "archive", file, "STANDARD")
	if err != nil {
		t.Fatal(err)
	}
	if h.Size != 16 || h.Hash == "" {
		t.Fatal("upload not verified", h)
	}
	if _, err = Create(context.Background(), store, "archive", file, "STANDARD"); err != nil {
		t.Fatal("recorded bytes not idempotent", err)
	}
	if puts != 1 {
		t.Fatal("retry uploaded bytes again")
	}
	os.WriteFile(file, []byte("different bytes"), 0600)
	if _, err = Create(context.Background(), store, "archive", file, "STANDARD"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatal("existing key overwritten", err)
	}
}

func TestFailedMultipartSessionIsAborted(t *testing.T) {
	t.Setenv("B2_KEY_ID", "test")
	t.Setenv("B2_APPLICATION_KEY", "test")
	aborted := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Has("uploads") {
			fmt.Fprint(w, `<InitiateMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>test</Bucket><Key>archive</Key><UploadId>session</UploadId></InitiateMultipartUploadResult>`)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Query().Get("uploadId") == "session" {
			aborted++
			w.WriteHeader(204)
			return
		}
		t.Errorf("unexpected multipart request %s %s", r.Method, r.URL)
		w.WriteHeader(400)
	}))
	defer server.Close()
	store, err := New(context.Background(), "b2", config.Provider{Bucket: "test", Region: "test", Endpoint: server.URL}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.(*s3Store).multipart(context.Background(), "archive", 12<<20, "STANDARD", func(context.Context, string, int32, int64, int64) (types.CompletedPart, error) {
		return types.CompletedPart{}, fmt.Errorf("test: interrupted part")
	}, nil, nil, "")
	if err == nil || aborted != 1 {
		t.Fatal("failed multipart not aborted", err, aborted)
	}
}
