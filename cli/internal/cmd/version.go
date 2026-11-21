package cmd

import (
	"github.com/spf13/cobra"
)

// version and commit are what this program was released at: the release
// that builds it writes them in where it is linked (the go plugin's
// version option, in frontseat.yaml). Any other build leaves the
// version every build that is no release is given, and no commit.
var version, commit = "0.0.0-dev", ""

// versionCmd represents the version command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Say which release of frontseat this is",
	Long: `Say the version this frontseat was released at, and the commit that
release was made of. One that no release built says 0.0.0-dev. It asks
no server.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if commit == "" {
			said("frontseat %s", version)
			return nil
		}
		said("frontseat %s (%s)", version, commit)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
