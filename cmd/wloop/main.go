// The command closes the loop between GitHub pull requests and AI
// coding agents running in local worktrees.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/wloophq/wloop/internal/cli"
)

var version = "dev"

func main() {
	cli.Version = version

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Run(ctx, cli.NewEnv(), os.Args[1:]))
}
