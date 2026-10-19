package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"glesha/cmd"
)

var VERSION = "dev"
var GIT_SHA = "unknown"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if e := cmd.Execute(ctx, os.Args[1:], VERSION, GIT_SHA); e != nil {
		cmd.PrintError(os.Args[1:], e)
		os.Exit(cmd.ExitCode(e))
	}
}
