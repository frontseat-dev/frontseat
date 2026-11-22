package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// cancelCmd represents the cancel command
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
		// Until build cancel and run cancel are commands of their own,
		// the id says which it is.
		switch id := args[0]; {
		case strings.HasPrefix(id, "build_"):
			_, err = w.BuildByID(id).Cancel(cmd.Context())
		case strings.HasPrefix(id, "run_"):
			_, err = w.RunByID(id).Cancel(cmd.Context())
		default:
			err = fmt.Errorf("%q is no build's id and no run's: a build's begins build_, and a run's run_", id)
		}
		return err
	},
}

func init() {
	rootCmd.AddCommand(cancelCmd)
	asksAWorkspace(cancelCmd)
}
