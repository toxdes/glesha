package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"glesha/cmd/app_cmd"
)

var VERSION = "dev"
var GIT_SHA = "unknown"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if e := app_cmd.Execute(ctx, os.Args[1:], VERSION, GIT_SHA); e != nil {
		app_cmd.PrintError(os.Args[1:], e)
		os.Exit(app_cmd.ExitCode(e))
	}
}
