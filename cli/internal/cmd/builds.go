package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// buildsCmd represents the builds command
var buildsCmd = &cobra.Command{
	Use:   "builds",
	Short: "List the workspace's builds",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		w, err := asked()
		if err != nil {
			return err
		}
		listed, err := w.Builds(cmd.Context())
		if err != nil {
			return err
		}
		for _, b := range listed {
			said("%s  %s", b.GetId(), state(b))
		}
		return nil
	},
}

// buildsEventsCmd represents the builds events command
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

// follow prints a build's events from one on, to the build's end, and
// returns the exit code the build ended with.
func follow(cmd *cobra.Command, build *frontseat.Build, from int64) (int, error) {
	printer := plain.Printer{Out: out}
	for e, err := range build.Events(cmd.Context(), from) {
		if err != nil {
			return 0, err
		}
		printer.Event(e)
		if ended := e.GetFinished().GetBuild(); ended != nil {
			return int(ended.GetExitCode().GetCode()), nil
		}
	}
	// A build is followed to the event that says how it ended: a stream
	// that ends before it is no build that passed.
	return 0, fmt.Errorf("the events of %s ended before it did: frontseat builds events %s goes on from where they were", build.ID, build.ID)
}

func state(b *buildv1.Build) string {
	if b.GetState() == buildv1.Build_STATE_RUNNING {
		return "running"
	}
	return b.GetExitCode().GetName()
}
