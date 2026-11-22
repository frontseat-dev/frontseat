package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
)

// deleteCmd ends a workspace.
var deleteCmd = &cobra.Command{
	Use:   "delete [WORKSPACE]",
	Short: "End a workspace, with what it holds",
	Long: `End the workspace the other commands ask, or the one named, and its
sandbox with it. It returns when they are gone.

What was changed in a workspace and not pushed goes with it.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		held, found, err := machine.ReadContext()
		if err != nil {
			return err
		}
		fs, err := server()
		if err != nil {
			return err
		}
		name := ""
		switch {
		case len(args) == 1:
			name = args[0]
		case found && held.Server == fs.Server():
			// The one the other commands ask is ended where it is kept.
			name = held.Workspace
		default:
			return errors.New("there is no workspace to delete: name one, as `frontseat workspace list` lists them")
		}
		// Until it is gone: what is done next, stopping the control plane
		// among it, leaves no sandbox behind.
		if err := fs.Workspace(name).Delete(cmd.Context()); err != nil {
			return err
		}
		// The workspace the other commands ask is forgotten where it was
		// this one.
		if found && held.Workspace == name && held.Server == fs.Server() {
			return machine.ForgetContext()
		}
		return nil
	},
}

func init() {
	workspaceCmd.AddCommand(deleteCmd)
}
