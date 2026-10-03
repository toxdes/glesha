package app_cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"glesha/backup"
	L "glesha/logger"
)

func confirm(ctx context.Context, message string, yes bool) error {
	return readConfirmation(ctx, os.Stdin, os.Stderr, term.IsTerminal(int(os.Stdin.Fd())), message, yes)
}
func readConfirmation(ctx context.Context, in io.Reader, out io.Writer, terminal bool, message string, yes bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if yes {
		return nil
	}
	if !terminal {
		return fmt.Errorf("%w: %s", backup.ErrConfirmationRequired, message)
	}
	if _, err := fmt.Fprintf(out, "%s [y/N] ", L.ASCII(message)); err != nil {
		return err
	}
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return fmt.Errorf("%w: declined", backup.ErrConfirmationRequired)
	}
	return nil
}
