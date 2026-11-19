// Command rvavctl is rvav's command line for users: the catalog, requests,
// deployments and their day-2 actions, and approvals (docs/guides/cli.md).
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/damhau/rvav/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Main(ctx, os.Args[1:], cli.OSEnv())
	stop()
	os.Exit(code)
}
