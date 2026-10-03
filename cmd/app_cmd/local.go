package app_cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"

	"glesha/archive"
	"glesha/database/model"
	"glesha/database/repository"
	"glesha/file_io"
	L "glesha/logger"
)

func localArchive(r *runtime, args []string) error {
	roots, err := archive.Roots(args)
	if err != nil {
		return err
	}
	output := r.env.Output
	if output == "" {
		output = r.config.Archive.Prefix + "-" + time.Now().UTC().Format("20060102T150405.000000000Z") + archive.Extension(r.config.Archive.Compression) + ".gpg"
	}
	output, err = file_io.Expand(output)
	if err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(output); !os.IsNotExist(err) {
		return fmt.Errorf("cli: archive output exists or cannot be inspected")
	}
	temp, err := file_io.Temp(filepath.Dir(output))
	if err != nil {
		return err
	}
	temp.Close()
	defer os.Remove(temp.Name())
	c, err := repository.NewCatalogRepository(r.ctx, temp.Name())
	if err != nil {
		return err
	}
	defer c.Close()
	id := time.Now().UTC().Format("20060102T150405.000000000Z")
	exclude := []string{output, temp.Name(), r.root}
	if err = archive.Inventory(r.ctx, c, id, roots, exclude, r.budget.HashWorkers); err != nil {
		return err
	}
	pw, err := password(r.env.PasswordFile, "Archive passphrase", true)
	if err != nil {
		return err
	}
	defer wipe(pw)
	_, _, err = archive.Create(r.ctx, archive.Options{Output: output, Compression: r.config.Archive.Compression, Level: r.config.Archive.Level, Mode: r.config.Archive.Mode, Budget: r.budget, Password: pw, Manifest: model.Manifest{Version: 1, ID: id, Full: true, Roots: roots, Compression: r.config.Archive.Compression}, Catalog: c, Exclusions: exclude})
	if err == nil {
		fmt.Fprintln(r.out, L.HumanText(output, term.IsTerminal(int(os.Stdout.Fd()))))
	}
	return err
}
func decrypt(r *runtime, args []string) error {
	if err := count(args, 1); err != nil {
		return err
	}
	pw, err := password(r.env.PasswordFile, "Archive passphrase", false)
	if err != nil {
		return err
	}
	defer wipe(pw)
	input, err := file_io.Expand(args[0])
	if err != nil {
		return err
	}
	format, err := archive.DecryptFile(r.ctx, input, r.env.Output, pw)
	if err == nil {
		output := r.env.Output
		if output == "" {
			output = filepath.Join(filepath.Dir(input), archive.Stem(input)+archive.Extension(format))
		}
		fmt.Fprintln(r.out, L.HumanText(output, term.IsTerminal(int(os.Stdout.Fd()))))
	}
	return err
}
func extract(r *runtime, args []string) error {
	if err := count(args, 1); err != nil {
		return err
	}
	if r.env.Into == "" {
		return fmt.Errorf("cli: extract requires --into")
	}
	input, err := file_io.Expand(args[0])
	if err != nil {
		return err
	}
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	var signature [3]byte
	_, err = io.ReadFull(f, signature[:])
	f.Close()
	if err != nil {
		return err
	}
	var pw []byte
	if !archive.IsCompressed(signature[:]) {
		pw, err = password(r.env.PasswordFile, "Archive passphrase", false)
		if err != nil {
			return err
		}
	}

	defer wipe(pw)
	_, _, err = archive.Extract(r.ctx, archive.ExtractOptions{Input: input, Output: r.env.Into, Password: pw})
	return err
}
