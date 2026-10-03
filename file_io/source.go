package file_io

import (
	"context"
	"io"
	"io/fs"
	"os"
)

type ReadableFile interface {
	io.ReadCloser
	Stat() (fs.FileInfo, error)
}

type SourceReader interface {
	Open(context.Context, string) (ReadableFile, error)
	Lstat(context.Context, string) (fs.FileInfo, error)
	Readlink(context.Context, string) (string, error)
	Walk(context.Context, string, func(string, fs.FileInfo) error) error
}

type sourceReader struct{}

func NewSourceReader() SourceReader { return sourceReader{} }

func (sourceReader) Open(ctx context.Context, p string) (ReadableFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (sourceReader) Lstat(ctx context.Context, p string) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.Lstat(p)
}

func (sourceReader) Readlink(ctx context.Context, p string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return os.Readlink(p)
}

func (sourceReader) Walk(ctx context.Context, p string, visit func(string, fs.FileInfo) error) error {
	return Walk(ctx, p, visit)
}
