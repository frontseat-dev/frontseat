package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/sdk/go"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

var (
	buildOnly    []string
	buildProfile profile
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

The command follows the build to its end and exits with its exit code.

The builds of the workspace are read and stopped with the commands
beneath: build list, describe, events, actions, logs and cancel. An
entity named list, describe, events, actions, logs or cancel is written
with its kind, component:list.`,
	Example: `  frontseat build
  frontseat build gateway --only lint
  frontseat build --fix //cli:go.format`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return built(cmd, func(w *frontseat.Workspace) (*frontseat.Build, error) {
			return w.Build(cmd.Context(), frontseat.BuildRequest{Targets: args, Only: buildOnly, Profile: buildProfile.Profile, Fix: buildFix})
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
	onlyFlag(buildCmd, &buildOnly)
	profileFlag(buildCmd, &buildProfile)
	buildCmd.Flags().BoolVar(&buildFix, "fix", false, "write what is out of step as it should be, where a build would fail and say so")
}

// onlyFlag gives a command that builds the flag that keeps the actions
// of a name or kind.
func onlyFlag(cmd *cobra.Command, only *[]string) {
	cmd.Flags().StringArrayVar(only, "only", nil, "keep only the actions of this name or kind, such as lint or generate; give it once for each")
}

// profileFlag gives a command that builds the flag that names the
// profile it builds with.
func profileFlag(cmd *cobra.Command, profile *profile) {
	cmd.Flags().Var(profile, "profile", "the profile to build with: development, or production, as a release builds")
}

// profile is a profile as the flag names it, and as the API says it;
// unset, the API's default, development.
type profile struct{ typev1.Profile }

// profiles are the profiles there are, by the name the flag takes.
var profiles = map[string]typev1.Profile{
	"development": typev1.Profile_PROFILE_DEVELOPMENT,
	"production":  typev1.Profile_PROFILE_PRODUCTION,
}

// Set takes a profile by name; the empty name is none, as unset.
func (p *profile) Set(name string) error {
	if name == "" {
		p.Profile = typev1.Profile_PROFILE_UNSPECIFIED
		return nil
	}
	said, there := profiles[name]
	if !there {
		return errors.New("the profiles are development and production")
	}
	p.Profile = said
	return nil
}

func (p *profile) String() string {
	for name, said := range profiles {
		if said == p.Profile {
			return name
		}
	}
	return ""
}

func (*profile) Type() string { return "profile" }
