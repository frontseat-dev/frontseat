// Command frontseat is the client: the command line. It talks to a
// daemon over the build API and holds no build state. It runs on every
// operating system, and nothing of the daemon is linked into it. Its
// commands are in cmd, one to a file.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.opentelemetry.io/otel/log"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/frontseat-dev/frontseat/cli/internal/cmd"
	"github.com/frontseat-dev/frontseat/server/telemetry"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code, err := observedCommand(ctx, os.Args[1:])
	if err != nil {
		var said string
		code, said = cmd.Failed(ctx, err)
		if said != "" {
			fmt.Fprintln(os.Stderr, "frontseat:", said)
		}
	}
	os.Exit(code)
}

// observedCommand owns the command's telemetry until it has flushed. main may
// then call os.Exit without losing its last logs, measurements or root span.
func observedCommand(ctx context.Context, args []string) (code int, err error) {
	signals, stop, err := telemetry.Setup(ctx, "frontseat")
	defer func() {
		if flush := telemetry.Ended(stop); flush != nil {
			fmt.Fprintln(os.Stderr, "frontseat: telemetry:", flush)
		}
	}()
	if err != nil {
		return 0, err
	}
	ctx = signals.Context(ctx)
	// The span is named by the command alone, never by what it was given.
	ctx, span := telemetry.Start(ctx, "frontseat "+cmd.Name(args))
	defer func() {
		exit := code
		if err != nil {
			exit, _ = cmd.Failed(ctx, err)
		}
		failed := telemetry.Outcome(span, exit, err)
		severity, message := log.SeverityInfo, "command finished"
		if failed != nil {
			severity, message = log.SeverityError, failed.Error()
		}
		telemetry.Message(ctx, severity, message, semconv.ProcessExitCode(exit))
		telemetry.End(span, failed)
	}()
	return cmd.Execute(ctx, args)
}
