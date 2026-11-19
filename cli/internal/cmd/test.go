package cmd

import (
	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/sdk/go"
)

var testProfile string

// testCmd represents the test command
var testCmd = &cobra.Command{
	Use:   "test [TARGET...]",
	Short: "Test the workspace, or what you point at",
	Long: `Test the workspace, or what you point at: what the tests need is
built, and every test action runs. Nothing else is built or checked.

` + targets + `

Tests are the actions their plugins say run tests, whatever they are
named, each piece of them where they are cut in pieces. Where what is
pointed at holds none, the build ends with NO_TESTS_FOUND and the
command exits 4, as bazel test does. Otherwise the command follows the
build to its end and exits with its exit code: 3 where a test failed.`,
	Example: `  frontseat test
  frontseat test gateway
  frontseat test //platform/gateway:go.test`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return built(cmd, func(w *frontseat.Workspace) (*frontseat.Build, error) {
			return w.Test(cmd.Context(), frontseat.BuildRequest{Targets: args, Profile: testProfile})
		})
	},
}

func init() {
	rootCmd.AddCommand(testCmd)
	asksAWorkspace(testCmd)
	profileFlag(testCmd, &testProfile)
}
