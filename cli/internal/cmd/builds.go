package cmd

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// buildsCmd lists the workspace's builds.
var buildsCmd = &cobra.Command{
	Use:   "builds",
	Short: "List the workspace's builds",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		w, err := asked()
		if err != nil {
			return err
		}
		for b, err := range w.Builds(cmd.Context()) {
			if err != nil {
				return err
			}
			said("%s  %s", b.GetId(), state(b))
		}
		return nil
	},
}

// buildsEventsCmd follows a build from one of its events on.
var buildsEventsCmd = &cobra.Command{
	Use:   "events BUILD [N]",
	Short: "Follow a build from event N, to its end",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		from := int64(0)
		if len(args) > 1 {
			var err error
			if from, err = strconv.ParseInt(args[1], 10, 64); err != nil {
				return fmt.Errorf("%q is not an event's number", args[1])
			}
		}
		w, err := asked()
		if err != nil {
			return err
		}
		exitCode, err = follow(cmd, w.BuildByID(args[0]), from)
		return err
	},
}

func init() {
	rootCmd.AddCommand(buildsCmd)
	asksAWorkspace(buildsCmd)
	buildsCmd.AddCommand(buildsEventsCmd)
	asksAWorkspace(buildsEventsCmd)
}

// reads is how long what a failed action printed is read for, before
// the command says it could not be and goes on.
const reads = time.Minute

// follow prints a build's events from one on, to the build's end, and
// returns the exit code the build ended with.
func follow(cmd *cobra.Command, build *frontseat.Build, from int64) (int, error) {
	printer := plain.Printer{Out: out}
	next := from
	for e, err := range build.Events(cmd.Context(), from) {
		if err != nil {
			return 0, err
		}
		printer.Event(e)
		// An action that failed is followed by what it printed, all of
		// it: its exit code says nothing of why. One that passed stays
		// its one line. What cannot be read is said, and the build is
		// followed on to its end.
		if failed := e.GetActionFinished().GetResult(); failed.GetOutcome() == buildv1.Outcome_OUTCOME_FAILED {
			// Not waited on for longer than reading takes: a daemon
			// that does not answer for what was printed is not what a
			// build's end is kept from being said by.
			reading, cancel := context.WithTimeout(cmd.Context(), reads)
			lines, err := frontseat.Collect(build.Logs(reading, failed.GetLabel()))
			cancel()
			printer.Printed(lines)
			if err != nil {
				printer.Unread(err)
			}
		}
		next = e.GetSequence() + 1
		if ended := e.GetFinished().GetBuild(); ended != nil {
			return exitOf(ended.GetExitCode()), nil
		}
	}
	// A build is followed to the event that says how it ended: a stream
	// that ends before it is no build that passed.
	return 0, fmt.Errorf("the events of %s ended before it did: frontseat builds events %s %d goes on from where they were", build.ID(), build.ID(), next)
}

func state(b *buildv1.Build) string {
	if b.GetState() == buildv1.Build_STATE_RUNNING {
		return "running"
	}
	return plain.Ended(b.GetExitCode())
}

// exits are the numbers the command line exits with for how a build
// ended: Bazel's, so that a script reads `frontseat build` as it reads
// `bazel build`.
var exits = map[buildv1.ExitCode]int{
	buildv1.ExitCode_EXIT_CODE_SUCCESS:        0,
	buildv1.ExitCode_EXIT_CODE_BUILD_FAILURE:  1,
	buildv1.ExitCode_EXIT_CODE_TESTS_FAILED:   3,
	buildv1.ExitCode_EXIT_CODE_NO_TESTS_FOUND: 4,
	buildv1.ExitCode_EXIT_CODE_INTERRUPTED:    8,
	buildv1.ExitCode_EXIT_CODE_INTERNAL_ERROR: 37,
}

// exitOf is the number the command line exits with for how a build
// ended; INTERNAL_ERROR's for one it does not know.
func exitOf(code buildv1.ExitCode) int {
	if n, ok := exits[code]; ok {
		return n
	}
	return exits[buildv1.ExitCode_EXIT_CODE_INTERNAL_ERROR]
}
