package cmd

import (
	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
)

// runsCmd lists the programs the workspace has run.
var runsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List the programs the workspace has run",
	Long: `List the programs the workspace has run, newest first: each that has
not ended, and the newest of those that have.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		w, err := asked()
		if err != nil {
			return err
		}
		listed, err := w.Runs(cmd.Context())
		if err != nil {
			return err
		}
		printer := plain.Printer{Out: out}
		for _, each := range listed {
			printer.Run(each)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(runsCmd)
	asksAWorkspace(runsCmd)
}
