package cmd

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// buildFrom is the event a build is followed from.
var buildFrom int64

// buildListCmd lists the workspace's builds.
var buildListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the workspace's builds",
	Long: `List the workspace's builds, newest first: each by its id, which the
other build commands take, and where it is.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printedEach(func(w *frontseat.Workspace) iter.Seq2[*buildv1.Build, error] { return w.Builds(cmd.Context()) }, func(b *buildv1.Build) {
			said("%s  %s", b.GetId(), state(b))
		})
	},
}

// buildDescribeCmd says where a build is.
var buildDescribeCmd = &cobra.Command{
	Use:   "describe BUILD",
	Short: "Say where a build is, and how it ended",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		w, err := asked()
		if err != nil {
			return err
		}
		b, err := w.BuildByID(args[0]).Describe(cmd.Context())
		if err != nil {
			return err
		}
		said("%s  %s", b.GetId(), state(b))
		if why := b.GetFailure(); why != "" {
			said("  %s", plain.Shown(why))
		}
		return nil
	},
}

// buildEventsCmd follows a build from one of its events on.
var buildEventsCmd = &cobra.Command{
	Use:   "events BUILD",
	Short: "Follow a build's events to its end",
	Long: `Follow a build's events, from the first or from the one --from
numbers, to its end, and exit with the build's exit code.`,
	Example: `  frontseat build events build_3f2a9c1d07e4
  frontseat build events build_3f2a9c1d07e4 --from 12`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if buildFrom < 0 {
			return fmt.Errorf("--from is an event's number, 0 or more, and %d is not", buildFrom)
		}
		w, err := asked()
		if err != nil {
			return err
		}
		exitCode, err = follow(cmd, w.BuildByID(args[0]), buildFrom)
		return err
	},
}

// buildActionsCmd says what each of a build's actions did.
var buildActionsCmd = &cobra.Command{
	Use:   "actions BUILD",
	Short: "Say what each of a build's actions did",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return printedEach(func(w *frontseat.Workspace) iter.Seq2[*buildv1.ActionResult, error] {
			return w.BuildByID(args[0]).Actions(cmd.Context())
		}, plain.Printer{Out: out}.Action)
	},
}

// buildLogsCmd prints what one action of a build printed.
var buildLogsCmd = &cobra.Command{
	Use:   "logs BUILD LABEL",
	Short: "Print what one action of a build printed",
	Long: `Print what one action of a build printed, every line, by the action's
label as the build printed it.`,
	Example: `  frontseat build logs build_3f2a9c1d07e4 //services/greeter:go.compile`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return printedEach(func(w *frontseat.Workspace) iter.Seq2[*typev1.LogLine, error] {
			return w.BuildByID(args[0]).Logs(cmd.Context(), args[1])
		}, plain.Printer{Out: out}.Line)
	},
}

// buildCancelCmd stops a build.
var buildCancelCmd = &cobra.Command{
	Use:   "cancel BUILD",
	Short: "Stop a build",
	Long: `Stop a build, by its id, as build list lists it. Cancelling one that
has ended changes nothing.`,
	Example: `  frontseat build cancel build_3f2a9c1d07e4`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return inWorkspace(func(w *frontseat.Workspace) error {
			_, err := w.BuildByID(args[0]).Cancel(cmd.Context())
			return err
		})
	},
}

func init() {
	for _, sub := range []*cobra.Command{buildListCmd, buildDescribeCmd, buildEventsCmd, buildActionsCmd, buildLogsCmd, buildCancelCmd} {
		buildCmd.AddCommand(sub)
		asksAWorkspace(sub)
	}
	buildEventsCmd.Flags().Int64Var(&buildFrom, "from", 0, "the number of the event to follow the build from; 0, the first, without it")
}

// printedEach prints, one at a time as they come, what the workspace a
// command asks yields: a list, a log.
func printedEach[T any](items func(*frontseat.Workspace) iter.Seq2[T, error], print func(T)) error {
	return inWorkspace(func(w *frontseat.Workspace) error {
		for item, err := range items(w) {
			if err != nil {
				return err
			}
			print(item)
		}
		return nil
	})
}

// inWorkspace does what a command does of the workspace it asks.
func inWorkspace(do func(*frontseat.Workspace) error) error {
	w, err := asked()
	if err != nil {
		return err
	}
	return do(w)
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
	return 0, fmt.Errorf("%w: the events of %s ended before it did: frontseat build events %s --from %d goes on from where they were", errUnfinished, build.ID(), build.ID(), next)
}

// errUnfinished is a stream the server ended before what it follows
// did: the server no longer answers of it.
var errUnfinished = errors.New("the server stopped answering")

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
