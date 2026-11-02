package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alinateghi/GoAnvilInstaller/internal/cli"
)

// Set with go build -ldflags "-X main.version=v0.1.0" for a release.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.Run(ctx, os.Args[1:], os.Stdout, version); err != nil {
		fmt.Fprintln(os.Stderr, "anvil:", err)
		os.Exit(1)
	}
}
