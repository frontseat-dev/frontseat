package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

// deleteYes deletes with no one asked.
var deleteYes bool

// answers is where a person's answer to a question is read: standard
// input, where a test does not say otherwise.
var answers io.Reader = os.Stdin

// deleteCmd ends a workspace.
var deleteCmd = &cobra.Command{
	Use:   "delete NAME",
	Short: "End a workspace, with what it holds",
	Long: `End the workspace named, and its sandbox with it. It returns when they
are gone, and prints deleted NAME.

What was changed in a workspace and not pushed goes with it. At a
terminal, what the workspace holds is shown and you are asked first;
anything else, a script or an agent, gives --yes, and is refused
without it.

Where it was the workspace selected on this machine, the selection is
forgotten too, also where the control plane answers that there is no
such workspace: it is gone already. frontseat workspace close forgets a
selection and ends nothing.`,
	Example: `  frontseat workspace delete services-main
  frontseat workspace delete services-main --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		w, err := subject(args)
		if err != nil {
			return err
		}
		ended := ending(cmd, w, name)
		if ended != nil && !absent(ended) {
			return ended
		}
		// Gone, now or before: where it was the workspace selected here,
		// of the control plane that was asked, it is selected no longer.
		// One of that name another control plane keeps is another, and
		// stays selected.
		chosen, err := selected()
		if err == nil && chosen.here && chosen.name == name {
			err = machine.ForgetContext()
		}
		switch {
		case ended != nil && err == nil && chosen.here && chosen.name == name:
			complained("%s was the workspace selected on this machine, and is forgotten here: build, test, run, query and ship work on this machine's directory again", name)
			return ended
		case ended != nil:
			return ended
		case err != nil:
			return fmt.Errorf("%s was deleted, and what this machine says of the workspace selected here could not be put right: %w", name, err)
		}
		said("deleted %s", name)
		return nil
	},
}

// ending ends a workspace once someone was asked, or --yes says no one
// is, and returns when it is gone.
func ending(cmd *cobra.Command, w *frontseat.Workspace, name string) error {
	if !deleteYes {
		if !atTerminal() {
			return fmt.Errorf("deleting %s ends what it holds, and what was not pushed with it: where no one is asked, give --yes", name)
		}
		held, err := w.Describe(cmd.Context())
		if err != nil {
			return err
		}
		printWorkspace(errOut, held)
		fmt.Fprintf(errOut, "Delete %s, and what was not pushed of it? [y/N] ", name)
		answer, _ := bufio.NewReader(answers).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			return fmt.Errorf("%s was not deleted", name)
		}
	}
	// Until it is gone: what is done next, stopping the control plane
	// among it, leaves no sandbox behind.
	return w.Delete(cmd.Context())
}

// absent is whether a control plane answered that there is no such
// workspace: by the reason it refuses with, or, where it gave none, by
// its code, as the SDK reads a workspace that is gone (Workspace.Delete).
func absent(err error) bool {
	return errors.Is(err, frontseat.ErrWorkspaceNotFound) || connect.CodeOf(err) == connect.CodeNotFound
}

func init() {
	workspaceCmd.AddCommand(deleteCmd)
	deleteCmd.Flags().BoolVar(&deleteYes, "yes", false, "delete with no one asked, as a script or an agent does")
}
