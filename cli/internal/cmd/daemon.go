package cmd

import (
	"flag"
	"time"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
)

var (
	daemonOptions life.Options
	daemonIdle    time.Duration
)

// daemonCmd is the build daemon, which the command line starts for
// itself (machine.BuildDaemon) and no person runs: this program again,
// serving the build API over one directory until it has had nothing to
// do for long enough.
var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Serve the build API over a directory: started by the command line, for itself",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// What the grid's client library logs (glog) goes to standard
		// error, never to a file of its own. The library registers its
		// flags with the process's, which the command line never parses.
		if err := flag.Set("logtostderr", "true"); err != nil {
			return err
		}
		daemonOptions.Idle = daemonIdle
		return life.Serve(cmd.Context(), daemonOptions)
	},
}

func init() {
	daemonCmd.Flags().StringVar(&daemonOptions.Directory, "directory", "", "the directory it builds")
	daemonCmd.Flags().StringVar(&daemonOptions.State, "state", "", "its state directory, which is that directory's alone")
	daemonCmd.Flags().StringVar(&daemonOptions.Settings, "settings", "", "the file it is told its plugins and its grid in")
	daemonCmd.Flags().StringVar(&daemonOptions.Compiled, "compiled", "", "the directory it keeps compiled plugins in, its user's alone, which every daemon of that user shares")
	daemonCmd.Flags().StringVar(&daemonOptions.Version, "version", "", "the program it is, as the command that starts it says")
	daemonCmd.Flags().DurationVar(&daemonIdle, "idle", 0, "how long it has nothing to do before it ends itself (default: three hours)")
	rootCmd.AddCommand(daemonCmd)
}
