package app_cmd

import (
	"bytes"
	"crypto/subtle"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"glesha/file_io"
	L "glesha/logger"
)

func password(file, label string, confirm bool) ([]byte, error) {
	label = L.ASCII(label)
	if file != "" {
		var r io.Reader
		if file == "-" {
			r = os.Stdin
		} else {
			p, e := file_io.Expand(file)
			if e != nil {
				return nil, e
			}
			f, e := os.Open(p)
			if e != nil {
				return nil, e
			}
			defer f.Close()
			r = f
		}
		b, e := io.ReadAll(io.LimitReader(r, (1<<20)+1))
		if e != nil {
			wipe(b)
			return nil, e
		}
		if len(b) > 1<<20 {
			wipe(b)
			return nil, fmt.Errorf("cli: invalid passphrase length")
		}
		b = bytes.TrimSuffix(bytes.TrimSuffix(b, []byte("\n")), []byte("\r"))
		if len(b) == 0 || len(b) >= 1<<20 {
			wipe(b)
			return nil, fmt.Errorf("cli: invalid passphrase length")
		}
		return b, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("cli: hidden prompt needs a terminal; provide --passphrase-file")
	}
	fmt.Fprint(os.Stderr, label+": ")
	b, e := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if e != nil {
		wipe(b)
		return nil, e
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("cli: empty passphrase")
	}
	if confirm {
		fmt.Fprint(os.Stderr, "Confirm "+label+": ")
		c, e := term.ReadPassword(fd)
		defer wipe(c)
		fmt.Fprintln(os.Stderr)
		if e != nil {
			wipe(b)
			return nil, e
		}
		if subtle.ConstantTimeCompare(b, c) != 1 {
			wipe(b)
			return nil, fmt.Errorf("cli: passphrases do not match")
		}
	}
	return b, nil
}

func wipe(p []byte) {
	for i := range p {
		p[i] = 0
	}
}
