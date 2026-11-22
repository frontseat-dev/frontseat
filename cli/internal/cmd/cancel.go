package cmd

import (
	"github.com/spf13/cobra"
)

// cancelCmd cancels a build or a run.
var cancelCmd = &cobra.Command{
	Use:   "cancel ID",
	Short: "Stop a build, or a run of a program",
	Long: `Stop a build, or a run of a program, by its id, as frontseat builds
and frontseat runs list them: a build is stopped, and a run's build
stopped or its program ended. Cancelling one that has ended changes
nothing.`,
	Example: `  frontseat cancel build_3f2a9c1d07e4
  frontseat cancel run_8b0e5d2c41aa`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		w, err := asked()
		if err != nil {
			return err
		}
		return w.Cancel(cmd.Context(), args[0])
	},
}

func init() {
	rootCmd.AddCommand(cancelCmd)
	asksAWorkspace(cancelCmd)
}
