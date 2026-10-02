package cloud

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"

	L "glesha/logger"
)

type uploadProgressKey struct{}

type progressHTTPClient struct {
	client aws.HTTPClient
}

func (c progressHTTPClient) Do(request *http.Request) (*http.Response, error) {
	if reader, ok := request.Context().Value(uploadProgressKey{}).(*uploadReader); ok {
		reader.sending.Store(true)
		defer reader.sending.Store(false)
	}
	return c.client.Do(request)
}

type uploadReader struct {
	mu        sync.Mutex
	body      io.ReadSeeker
	progress  *L.Progress
	sending   atomic.Bool
	position  int64
	highWater int64
}

func trackUpload(ctx context.Context, body io.ReadSeeker, progress *L.Progress) (context.Context, io.ReadSeeker) {
	if progress == nil {
		return ctx, body
	}
	r := &uploadReader{body: body, progress: progress}
	return context.WithValue(ctx, uploadProgressKey{}, r), r
}

func (r *uploadReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.body.Read(p)
	r.position += int64(n)
	// signing/checksum reads happen before Do; retries count each byte once
	if r.sending.Load() && r.position > r.highWater {
		r.progress.Add(r.position - r.highWater)
		r.highWater = r.position
	}
	return n, err
}

func (r *uploadReader) Seek(offset int64, whence int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	position, err := r.body.Seek(offset, whence)
	if err == nil {
		r.position = position
	}
	return position, err
}
