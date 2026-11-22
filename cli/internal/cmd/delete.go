package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
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
without it.`,
	Example: `  frontseat workspace delete services-main
  frontseat workspace delete services-main --yes`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		w, err := subject(args)
		if err != nil {
			return err
		}
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
		if err := w.Delete(cmd.Context()); err != nil {
			return err
		}
		// The workspace the other commands ask is forgotten where it was
		// this one.
		if held, found, err := machine.ReadContext(); err != nil {
			return err
		} else if fs, _ := server(); found && fs != nil && held.Workspace == name && held.Server == fs.Server() {
			if err := machine.ForgetContext(); err != nil {
				return err
			}
		}
		said("deleted %s", name)
		return nil
	},
}

func init() {
	workspaceCmd.AddCommand(deleteCmd)
	deleteCmd.Flags().BoolVar(&deleteYes, "yes", false, "delete with no one asked, as a script or an agent does")
}
