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

// runCLI is the entry hook used by run(); tests may replace it.
var runCLI = cmd.ExecuteContext

// exitFn is os.Exit in production; tests replace it to avoid exiting the process.
var exitFn = os.Exit

func main() {
	exitFn(run())
}

// run executes the CLI. main delegates to cmd.ExecuteContext; tests cover exit paths here.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runCLI(ctx); err != nil {
		if errors.Is(err, cmd.ErrSilent) {
			return cmd.LastExitCode()
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		return cmd.ExitBadArgs
	}
	return 0
}
