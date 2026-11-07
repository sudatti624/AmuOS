// Package process runs a server under the OS service manager or in a terminal.
package process

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func console(run func(context.Context) error) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return run(ctx)
}
