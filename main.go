package main

import (
	"context"
	"glesha/cmd"
	L "glesha/logger"
	"os"
	"os/signal"
	"syscall"
)

func init() {
	// will be overriden by subcommands
	L.SetLevel("error")
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	err := cmd.Execute(ctx, os.Args[:])

	select {
	case <-ctx.Done():
		L.Debug("Command execution was aborted.")
	default:
		L.Debug("Command execution complete.")
	}
	if err != nil {
		L.Panic(err)
	}
	os.Exit(0)
}
