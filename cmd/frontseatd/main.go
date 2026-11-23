// Command frontseatd is the daemon. It always runs in a sandbox: it
// populates a workspace, evaluates it with plugins, runs actions on a
// grid and serves the build API. It is a main over package daemon,
// which is the daemon.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/frontseat-dev/frontseat/build/daemon/daemon"
)

// version and commit are what this program was released at: the release
// that builds it writes them in where it is linked (the go plugin's
// version option, in frontseat.yaml). Any other build leaves the
// version every build that is no release is given, and no commit.
var version, commit = "0.0.0-dev", ""

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := daemon.Run(ctx, os.Args[1:], daemon.Release{Version: version, Commit: commit})
	stop()
	if errors.Is(err, daemon.ErrUsage) {
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "frontseatd:", err)
		os.Exit(1)
	}
}
