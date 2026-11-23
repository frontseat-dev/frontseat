// Command frontseat-box is a box's supervisor: the one program of a
// workspace's sandbox that works for the control plane. It holds the
// sandbox's lock, fills the workspace's directory as it is told, and
// serves the shell. It is a main over package supervisor.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/frontseat-dev/frontseat/workspace/box/supervisor"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := supervisor.Run(ctx, os.Args[1:])
	stop()
	if errors.Is(err, supervisor.ErrUsage) {
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "frontseat-box:", err)
		os.Exit(1)
	}
}
