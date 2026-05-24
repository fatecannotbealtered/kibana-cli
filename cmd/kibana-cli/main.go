package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/fatecannotbealtered/kibana-cli/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.ExecuteContext(ctx); err != nil {
		if errors.Is(err, cmd.ErrSilent) {
			os.Exit(cmd.LastExitCode())
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(cmd.ExitBadArgs)
	}
}
