package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"glesha/internal/backup"
	"glesha/internal/logger"
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
	resume := logger.PauseProgress(ctx)
	defer resume()
	if _, err := fmt.Fprintf(out, "%s [y/N] ", logger.ASCII(message)); err != nil {
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

// readTypedConfirmation asks the user to type name, for permanent deletion.
// One reader serves every prompt, so a line typed ahead is not lost.
func readTypedConfirmation(ctx context.Context, in *bufio.Reader, out io.Writer, terminal bool, label, name string, yes bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if yes {
		return nil
	}
	if !terminal {
		return fmt.Errorf("%w: type %s %q", backup.ErrConfirmationRequired, label, name)
	}
	resume := logger.PauseProgress(ctx)
	defer resume()
	if _, err := fmt.Fprintf(out, "Type %s %q to confirm deletion: ", label, logger.ASCII(name)); err != nil {
		return err
	}
	answer, err := in.ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(answer) != name {
		return fmt.Errorf("%w: confirmation does not match; nothing was deleted", backup.ErrConfirmationRequired)
	}
	return nil
}
