package cmd

import (
	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/sdk/go"
)

var (
	buildOnly    []string
	buildProfile string
	buildFix     bool
)

// targets is what build and test say of what they are pointed at.
const targets = `  a name is an entity         gateway
  a path is all beneath it    platform/
  an action, as printed       //platform/gateway:go.test`

// buildCmd builds the workspace, or what it is pointed at.
var buildCmd = &cobra.Command{
	Use:   "build [TARGET...]",
	Short: "Build the workspace, or what you point at",
	Long: `Build the workspace, or what you point at:

` + targets + `

The command follows the build to its end and exits with its exit code.`,
	Example: `  frontseat build
  frontseat build gateway --only lint
  frontseat build --fix //cli:go.format`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return built(cmd, func(w *frontseat.Workspace) (*frontseat.Build, error) {
			// Which profiles there are is the daemon's to say.
			return w.Build(cmd.Context(), frontseat.BuildRequest{Targets: args, Only: buildOnly, Profile: buildProfile, Fix: buildFix})
		})
	},
}

// built starts a build of the workspace a command asks, follows it to
// its end, and ends the command as the build ended.
func built(cmd *cobra.Command, start func(*frontseat.Workspace) (*frontseat.Build, error)) error {
	w, err := asked()
	if err != nil {
		return err
	}
	build, err := start(w)
	if err != nil {
		return err
	}
	exitCode, err = follow(cmd, build, 0)
	return err
}

func init() {
	rootCmd.AddCommand(buildCmd)
	asksAWorkspace(buildCmd)
	buildCmd.Flags().StringArrayVar(&buildOnly, "only", nil, "keep only the actions of this name, such as lint; give it once for each")
	profileFlag(buildCmd, &buildProfile)
	buildCmd.Flags().BoolVar(&buildFix, "fix", false, "write what is out of step as it should be, where a build would fail and say so")
}

// profileFlag gives a command that builds the flag that names the
// profile it builds with.
func profileFlag(cmd *cobra.Command, profile *string) {
	cmd.Flags().StringVar(profile, "profile", "", "the profile to build with, by name: development, or production, as a release builds")
}
