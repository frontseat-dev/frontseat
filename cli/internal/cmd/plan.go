package cmd

import (
	"github.com/spf13/cobra"
)

// planCmd says what a release would do.
var planCmd = &cobra.Command{
	Use:   "plan ENTITY...",
	Short: "Say what releasing entities would do, and change nothing",
	Long: `Say what releasing entities would do now: the units it would release,
the version each would get and why, the tag it would be given, or why a
unit holds. Nothing is built, tagged or changed.

The last line is the plan's etag. ship --plan releases that plan.`,
	Example: `  frontseat ship plan greet
  frontseat ship plan greet system:kiosk`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entities, w, err := releasing(args)
		if err != nil {
			return err
		}
		_, err = plan(cmd, w, entities)
		return err
	},
}

func init() {
	shipCmd.AddCommand(planCmd)
	asksAWorkspace(planCmd)
}
