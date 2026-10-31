package file_io

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func passwordPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "pty")
	t.Cleanup(func() { master.Close() })
	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

func waitPasswordHidden(t *testing.T, fd int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		settings, err := unix.IoctlGetTermios(fd, unix.TCGETS)
		if err != nil {
			t.Fatal(err)
		}
		if settings.Lflag&unix.ECHO == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("terminal echo was not disabled")
}

func TestPasswordTerminalCancellationAndEditing(t *testing.T) {
	for _, scenario := range []string{"cancel", "unicode and editing"} {
		t.Run(scenario, func(t *testing.T) {
			master, slave := passwordPTY(t)
			fd := int(slave.Fd())
			original, err := unix.IoctlGetTermios(fd, unix.TCGETS)
			if err != nil {
				t.Fatal(err)
			}
			flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				secret []byte
				err    error
			}
			done := make(chan result, 1)
			go func() { secret, err := ReadPassword(ctx, slave); done <- result{secret, err} }()
			waitPasswordHidden(t, fd)
			if scenario == "cancel" {
				cancel()
			} else {
				if _, err = master.Write([]byte("caf\xc3\xa9x\x7f\n")); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case got := <-done:
				defer clear(got.secret)
				if scenario == "cancel" {
					if !errors.Is(got.err, context.Canceled) || len(got.secret) != 0 {
						t.Fatalf("cancellation failed: %v", got.err)
					}
				} else if got.err != nil || !bytes.Equal(got.secret, []byte("café")) {
					t.Fatalf("unicode/editing failed: %v", got.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("password read did not return")
			}
			after, err := unix.IoctlGetTermios(fd, unix.TCGETS)
			if err != nil || !reflect.DeepEqual(after, original) {
				t.Fatal("terminal settings were not restored", err)
			}
			afterFlags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
			if err != nil || afterFlags != flags {
				t.Fatal("terminal file flags were not restored", err)
			}
		})
	}
}

func TestPasswordControlCHelper(t *testing.T) {
	if os.Getenv("GLESHA_PASSWORD_PTY_HELPER") != "1" {
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	secret, err := ReadPassword(ctx, os.Stdin)
	clear(secret)
	if errors.Is(err, context.Canceled) {
		os.Exit(130)
	}
	os.Exit(1)
}

func TestPasswordControlCExitsWithoutEnter(t *testing.T) {
	master, slave := passwordPTY(t)
	fd := int(slave.Fd())
	original, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPasswordControlCHelper$")
	command.Env = append(os.Environ(), "GLESHA_PASSWORD_PTY_HELPER=1")
	command.Stdin, command.Stdout, command.Stderr = slave, slave, slave
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	waitPasswordHidden(t, fd)
	if _, err := master.Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	err = command.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 130 || ctx.Err() != nil {
		t.Fatalf("Ctrl+C needed Enter or did not cancel: %v", err)
	}
	after, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil || !reflect.DeepEqual(after, original) {
		t.Fatal("Ctrl+C did not restore echo", err)
	}
}
