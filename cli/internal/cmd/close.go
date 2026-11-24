package cmd

import (
	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
)

// closeCmd forgets, on this machine, which workspace is selected.
var closeCmd = &cobra.Command{
	Use:   "close",
	Short: "Forget, on this machine, which workspace is selected: it goes on",
	Long: `Forget which workspace is selected on this machine, the one last
opened: build, test, run, query and ship then work on the directory
they are run in again, and a workspace command is told a workspace's
name.

Nothing but this machine is touched, and no control plane is asked: the
workspace goes on as it is, with what it holds. frontseat workspace open
NAME selects it again, and frontseat workspace delete NAME ends it.

With no workspace selected it says so, and that is no mistake. A
selection this machine cannot read, written by another version of the
command line, is removed.`,
	Example: `  frontseat workspace close`,
	Args:    cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		held, found, unread := machine.ReadContext()
		if unread == nil && !found {
			said("no workspace is selected on this machine")
			return nil
		}
		// The file is this machine's own, and the command line's alone
		// (machine.ForgetContext): one it cannot read is removed as one
		// it can.
		if err := machine.ForgetContext(); err != nil {
			return err
		}
		if unread != nil {
			said("forgot a selection that could not be read: no workspace is selected on this machine")
			return nil
		}
		said("closed %s on this machine: the workspace goes on (frontseat workspace open %s selects it again, frontseat workspace delete %s ends it)", held.Workspace, held.Workspace, held.Workspace)
		return nil
	},
}

func init() {
	workspaceCmd.AddCommand(closeCmd)
}
