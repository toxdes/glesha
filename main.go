package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"glesha/internal/cli"
)

var VERSION = "dev"
var GIT_SHA = "unknown"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if e := cli.Execute(ctx, os.Args[1:], VERSION, GIT_SHA); e != nil {
		cli.PrintError(os.Args[1:], e)
		os.Exit(cli.ExitCode(e))
	}
}
