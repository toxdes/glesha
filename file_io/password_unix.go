//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package file_io

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// ReadPassword keeps terminal line editing while checking cancellation between reads.
func ReadPassword(ctx context.Context, input *os.File) (secret []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd := int(input.Fd())
	original, err := unix.IoctlGetTermios(fd, passwordReadTermios)
	if err != nil {
		return nil, err
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return nil, err
	}
	settings := *original
	settings.Lflag &^= unix.ECHO
	settings.Lflag |= unix.ICANON | unix.ISIG
	settings.Iflag |= unix.ICRNL
	if err = unix.IoctlSetTermios(fd, passwordWriteTermios, &settings); err != nil {
		return nil, err
	}
	defer func() {
		restoreErr := unix.IoctlSetTermios(fd, passwordWriteTermios, original)
		_, flagsErr := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags)
		if restoreErr != nil || flagsErr != nil {
			clear(secret)
			secret = nil
			err = errors.Join(err, fmt.Errorf("file_io: restore terminal: %w", errors.Join(restoreErr, flagsErr)))
		}
	}()
	if _, err = unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags|unix.O_NONBLOCK); err != nil {
		return nil, err
	}
	reader := passwordReader{ctx: ctx, fd: fd}
	var character [1]byte
	defer func() {
		clear(character[:])
		if err != nil {
			clear(secret)
			secret = nil
		}
	}()
	for {
		n, readErr := reader.Read(character[:])
		if n > 0 {
			switch character[0] {
			case '\n':
				return secret, nil
			case '\r':
				continue
			case 3:
				return secret, context.Canceled
			}
			if len(secret) >= 1<<20 {
				return secret, fmt.Errorf("file_io: passphrase exceeds 1MiB")
			}
			secret = append(secret, character[0])
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) && len(secret) > 0 {
				return secret, nil
			}
			return secret, readErr
		}
	}
}

type passwordReader struct {
	ctx context.Context
	fd  int
}

func (r passwordReader) Read(buffer []byte) (int, error) {
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		fds := []unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 50)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if n == 0 {
			continue
		}
		n, err = unix.Read(r.fd, buffer)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if n == 0 && err == nil {
			return 0, io.EOF
		}
		return n, err
	}
}
