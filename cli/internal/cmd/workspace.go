package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/enum"
)

// workspaceCmd is the group of commands that are asked of the control plane about workspaces.
var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Open, fill, list and delete workspaces, and reach their shells and ports, from outside them",
	Args:  cobra.ArbitraryArgs,
	RunE:  named("a workspace command"),
	// Inside a workspace's sandbox its source is fixed: every workspace
	// command is refused there, before anything is asked.
	PersistentPreRunE: func(*cobra.Command, []string) error {
		if inside() {
			return fixed
		}
		return nil
	},
}

// describeCmd says what a workspace holds.
var describeCmd = &cobra.Command{
	Use:   "describe [NAME]",
	Short: "Say what a workspace holds",
	Long:  `Say what the workspace named, or the one last opened, holds, and where it is.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		w, err := subject(args)
		if err != nil {
			return err
		}
		return described(cmd, w)
	},
}

func init() {
	rootCmd.AddCommand(workspaceCmd)
	workspaceCmd.AddCommand(describeCmd)
}

// subject is the workspace a workspace command is of: the one named,
// or, with none, the one selected, the one last opened of the control
// plane asked now (selected). Where there is none to take, or the one
// last opened is of another control plane, it says so, and that the
// command takes a name too.
func subject(args []string) (*frontseat.Workspace, error) {
	if len(args) == 0 {
		chosen, err := selected()
		switch {
		case err != nil:
			return nil, err
		case chosen.name == "":
			return nil, fmt.Errorf("%w, or name one, as this command takes a workspace's NAME", errNoWorkspace)
		case !chosen.here:
			return nil, fmt.Errorf("the workspace last opened, %s, is of the control plane at %s, and the command line asks the one at %s (frontseat --help): open one there, or name one, as this command takes a workspace's NAME", chosen.name, chosen.plane, chosen.asked)
		}
		fs, err := server()
		if err != nil {
			return nil, err
		}
		return fs.Workspace(chosen.name), nil
	}
	if !frontseat.IsWorkspaceName(args[0]) {
		return nil, fmt.Errorf("%q is no workspace's name: one is lower-case letters, digits and dashes, at most 48", args[0])
	}
	fs, err := server()
	if err != nil {
		return nil, err
	}
	return fs.Workspace(args[0]), nil
}

// described prints a workspace: its name, where it is, and what it
// holds.
func described(cmd *cobra.Command, w *frontseat.Workspace) error {
	held, err := w.Describe(cmd.Context())
	if err != nil {
		return err
	}
	printWorkspace(out, held)
	return nil
}

// printWorkspace writes a workspace as described says it.
func printWorkspace(to io.Writer, held *workspacev1.Workspace) {
	line := func(format string, a ...any) { fmt.Fprintf(to, format+"\n", a...) }
	state := enum.Word(held.GetStatus().GetState())
	if held.GetName() != "" {
		line("%s  %s", held.GetName(), state)
	} else {
		line("%s", state)
	}
	// Why it failed, or what keeps it from going on.
	if why := held.GetStatus().GetFailure(); why != "" {
		line("  %s", plain.Shown(why))
	}
	for _, r := range held.GetStatus().GetRepositories() {
		switch {
		case r.GetName() == "":
			line("  a directory the supervisor was given")
		case len(r.GetRoots()) == 0:
			line("  %s  %.12s  whole", r.GetName(), r.GetCommit())
		default:
			line("  %s  %.12s  %s", r.GetName(), r.GetCommit(), strings.Join(r.GetRoots(), " "))
		}
	}
}
