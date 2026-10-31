package file_io

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

func ReadPassword(ctx context.Context, input *os.File) (secret []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle := windows.Handle(input.Fd())
	var original uint32
	if err = windows.GetConsoleMode(handle, &original); err != nil {
		return nil, err
	}
	mode := (original &^ windows.ENABLE_ECHO_INPUT) | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT
	if err = windows.SetConsoleMode(handle, mode); err != nil {
		return nil, err
	}
	defer func() {
		if restoreErr := windows.SetConsoleMode(handle, original); restoreErr != nil {
			clear(secret)
			secret = nil
			err = errors.Join(err, fmt.Errorf("file_io: restore console: %w", restoreErr))
		}
	}()
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-stop:
			return
		case <-ctx.Done():
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			windows.CancelIoEx(handle, nil)
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { close(stop); <-stopped }()
	var units []uint16
	var buffer [256]uint16
	defer func() {
		clear(units)
		clear(buffer[:])
		if err != nil {
			clear(secret)
			secret = nil
		}
	}()
	complete := false
	for !complete {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		var count uint32
		err = windows.ReadConsole(handle, &buffer[0], uint32(len(buffer)), &count, nil)
		if cancelErr := ctx.Err(); cancelErr != nil {
			return nil, cancelErr
		}
		if err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, io.EOF
		}
		for _, unit := range buffer[:count] {
			if unit == '\r' || unit == '\n' {
				complete = true
				break
			}
			if unit == 3 {
				return nil, context.Canceled
			}
			if len(units) >= 1<<20 {
				return nil, fmt.Errorf("file_io: passphrase exceeds 1MiB")
			}
			units = append(units, unit)
		}
		clear(buffer[:])
	}
	runes := utf16.Decode(units)
	defer clear(runes)
	for _, r := range runes {
		secret = utf8.AppendRune(secret, r)
	}
	if len(secret) >= 1<<20 {
		return secret, fmt.Errorf("file_io: passphrase exceeds 1MiB")
	}
	return secret, nil
}
