package cmd

import (
	"iter"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// runListCmd lists the programs the workspace has run.
var runListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the programs the workspace has run",
	Long: `List the programs the workspace has run, newest first: each that has
not ended, and the newest of those that have, by its id, which the
other run commands take.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printedEach(func(w *frontseat.Workspace) iter.Seq2[*buildv1.Run, error] { return w.Runs(cmd.Context()) }, plain.Printer{Out: out}.Run)
	},
}

// runDescribeCmd says where a run is.
var runDescribeCmd = &cobra.Command{
	Use:   "describe RUN",
	Short: "Say where a run of a program is, and how it ended",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return inWorkspace(func(w *frontseat.Workspace) error {
			described, err := w.RunByID(args[0]).Describe(cmd.Context())
			if err != nil {
				return err
			}
			plain.Printer{Out: out}.Run(described)
			return nil
		})
	},
}

// runLogsCmd prints what a run's program printed.
var runLogsCmd = &cobra.Command{
	Use:   "logs RUN",
	Short: "Print what a run's program printed, to its end",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return printedEach(func(w *frontseat.Workspace) iter.Seq2[*typev1.LogLine, error] {
			return w.RunByID(args[0]).Logs(cmd.Context())
		}, plain.Printer{Out: out}.Line)
	},
}

// runCancelCmd stops a run.
var runCancelCmd = &cobra.Command{
	Use:   "cancel RUN",
	Short: "Stop a run of a program",
	Long: `Stop a run of a program, by its id, as run list lists it: its build is
stopped, or its program ended. Cancelling one that has ended changes
nothing.`,
	Example: `  frontseat run cancel run_8b0e5d2c41aa`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return inWorkspace(func(w *frontseat.Workspace) error {
			_, err := w.RunByID(args[0]).Cancel(cmd.Context())
			return err
		})
	},
}

func init() {
	for _, sub := range []*cobra.Command{runListCmd, runDescribeCmd, runLogsCmd, runCancelCmd} {
		runCmd.AddCommand(sub)
		worksWhereItIsRun(sub)
	}
}
