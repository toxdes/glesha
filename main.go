package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"glesha/cmd"
	L "glesha/logger"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	err := cmd.Execute(ctx)
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
