package cmd

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
)

// workspaceCmd represents the workspace command
var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Open, fill, list and delete workspaces, and reach their shells, from outside them",
	Args:  cobra.ArbitraryArgs,
	RunE:  named("a workspace command"),
	// Inside a workspace's sandbox its source is fixed: every workspace
	// command is refused there, before anything is asked.
	PersistentPreRunE: func(*cobra.Command, []string) error {
		in, err := inside()
		if err != nil {
			return err
		}
		return refusedInside(in)
	},
}

// describeCmd represents the workspace describe command
var describeCmd = &cobra.Command{
	Use:   "describe",
	Short: "Say what the workspace holds",
	Args:  cobra.NoArgs,
	RunE:  func(cmd *cobra.Command, args []string) error { return describe(cmd) },
}

func init() {
	rootCmd.AddCommand(workspaceCmd)
	workspaceCmd.AddCommand(describeCmd)
	asksAWorkspace(describeCmd)
}

// describe prints the workspace a command asks: its name, where it is,
// and what it holds.
func describe(cmd *cobra.Command) error {
	w, err := asked()
	if err != nil {
		return err
	}
	held, err := w.Describe(cmd.Context())
	if err != nil {
		return err
	}
	state := plain.Word(held.GetStatus().GetState())
	if held.GetName() != "" {
		said("%s  %s", held.GetName(), state)
	} else {
		said("%s", state)
	}
	// Why it failed, or what keeps it from going on.
	if why := held.GetStatus().GetFailure(); why != "" {
		said("  %s", plain.Shown(why))
	}
	for _, r := range held.GetStatus().GetRepositories() {
		switch {
		case r.GetName() == "":
			said("  a directory the daemon was given")
		case len(r.GetRoots()) == 0:
			said("  %s  %.12s  whole", r.GetName(), r.GetCommit())
		default:
			said("  %s  %.12s  %s", r.GetName(), r.GetCommit(), strings.Join(r.GetRoots(), " "))
		}
	}
	return nil
}
