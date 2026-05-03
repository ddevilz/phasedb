// cmd/phasedb/main.go
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ddevilz/phasedb/internal/cli"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := cli.NewRootCmd(version).ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
