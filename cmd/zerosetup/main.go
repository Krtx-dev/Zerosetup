// Command zerosetup inspects, runs and reproduces supported repositories.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/zerosetup/zerosetup/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
