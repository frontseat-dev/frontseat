package cmd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
)

var (
	opened machine.Open
	// openShell and openNoShell say whether a workspace opened or made
	// is gone on into, in a shell; without either, it is at a terminal.
	openShell, openNoShell bool
)

// openCmd opens a workspace, and makes it the one the other commands ask.
var openCmd = &cobra.Command{
	Use:   "open [NAME]",
	Short: "Open a workspace: one there is, by its name, or this checkout's",
	Long: `Open a workspace and make it the workspace the other commands ask.

Named, it is the workspace there is of that name, as it was left. With
no name, it is the workspace of the repository this checkout is of, at
the branch it is on, cloned from the checkout's remote: the one there
is, as it was left, or a new one, named by its repository and branch or
by --name. A workspace of repositories or entities is made with
workspace create.

It is the control plane's every command asks (frontseat --help).

At a terminal it goes on into a shell there, by this machine's SSH
client; leaving the shell leaves the workspace as it is. Run by a script
or an agent, it opens the workspace and returns. --shell and --no-shell
say which, whatever runs it.

Nothing of this machine is given to it. A workspace holds what the remote
holds: commits that are not pushed and files that are not committed are
not in it, and are counted so that you know.

What is done in a workspace leaves it by a push from it, which only a
workspace made with --push may do: to its own repositories and no other,
as far as the credential the control plane keeps lets it.`,
	Example: `  frontseat workspace open
  frontseat workspace open --no-shell
  frontseat workspace open services-main`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if len(args) == 1 {
			return openByName(cmd, args[0])
		}
		fs, err := server()
		if err != nil {
			return err
		}
		open := opened
		if open.Directory == "" {
			directory, err := os.Getwd()
			if err != nil {
				return err
			}
			open.Directory = directory
		}
		got, err := machine.OpenCheckout(ctx, fs, open, machine.Checkout())
		if err := kept(fs, got.OpenResult, err); err != nil {
			return err
		}
		if asJSON {
			if err := emitted(got.Status); err != nil {
				return err
			}
			return entered(ctx, got.Repository)
		}
		standing := got.Standing
		said("%s  %s", standing.Remote, standing.Branch)
		if standing.Converted {
			said("  this checkout writes its remote otherwise: the same host and path are cloned as above, and no user it names is sent on")
		}
		if missing := notHeld(standing.Unpushed, standing.Uncommitted); missing != "" {
			said("  %s: the workspace does not hold them", missing)
		}
		said("%s  %s", got.Workspace.Name(), got.Outcome)
		if got.Status.GetSpec().GetPush() {
			said("  it may push to this repository, with whatever credential the control plane keeps for its host")
		}
		return entered(ctx, got.Repository)
	},
}

// kept makes a workspace that was opened the one the other commands
// ask; of one that could not be opened, it says that it is still there.
func kept(fs *frontseat.Client, got frontseat.OpenResult, err error) error {
	if err != nil {
		return left(err, got.Workspace)
	}
	return machine.WriteContext(machine.Context{Server: fs.Server(), Workspace: got.Workspace.Name()})
}

// left says, of a workspace that could not be opened, that it is still
// there: nothing is ended here, since it may hold work.
func left(err error, workspace *frontseat.Workspace) error {
	if workspace == nil || workspace.Name() == "" {
		return err
	}
	return fmt.Errorf("%w\nthe workspace is %s: `frontseat workspace open` again goes on with it, and `frontseat workspace delete %s` ends it, with what it holds", err, workspace.Name(), workspace.Name())
}

// openByName opens the workspace there is of a name, as it was left.
func openByName(cmd *cobra.Command, name string) error {
	if !frontseat.IsWorkspaceName(name) {
		return fmt.Errorf("%q is no workspace's name: one is lower-case letters, digits and dashes, at most 48; a workspace of repositories or entities is made with `frontseat workspace create`", name)
	}
	// What a workspace is was said when it was made, and is not asked
	// again of one that is there; a directory names a checkout.
	for _, flag := range []string{"name", "push", "image", "directory"} {
		if cmd.Flags().Changed(flag) {
			return fmt.Errorf("--%s is said of the workspace of a checkout, and %s is one there is: open it by its name alone", flag, name)
		}
	}
	fs, err := server()
	if err != nil {
		return err
	}
	key, err := machine.AuthorizedKey()
	if err != nil {
		return err
	}
	return opening(cmd.Context(), fs, frontseat.WorkspaceSpec{Name: name, AuthorizedKey: key})
}

// opening opens what a spec asks for, says how it came to be, and goes
// on into it where a shell is wanted: in its repository where it holds
// one, and above them where it holds several.
func opening(ctx context.Context, fs *frontseat.Client, spec frontseat.WorkspaceSpec) error {
	got, err := fs.Open(ctx, spec)
	if err := kept(fs, got, err); err != nil {
		return err
	}
	if err := shown(got.Status, func(*workspacev1.Workspace) { said("%s  %s", got.Workspace.Name(), got.Outcome) }); err != nil {
		return err
	}
	in := ""
	if holds := got.Status.GetStatus().GetRepositories(); len(holds) == 1 {
		in = holds[0].GetName()
	}
	return entered(ctx, in)
}

// entered goes on, at a terminal, into a shell in a workspace that was
// opened or made: a person who opens a workspace means to be in it. What
// is not at a terminal, a script or an agent, has no one to type, and is
// given what was opened and nothing more, and so is a program that asked
// for --json; --shell and --no-shell say which, whatever runs it. The workspace was opened whatever is then
// done in the shell: how the shell's last command ended is not how this
// command ends, and only an SSH client that could not get in (255, its
// own) is.
func entered(ctx context.Context, repository string) error {
	if wanted := openShell || (!openNoShell && !asJSON && atTerminal()); !wanted {
		return nil
	}
	err := shell(ctx, "", true, []string{entering(repository)})
	if exitCode != 255 {
		exitCode = 0
	}
	return err
}

// entering is what a shell opened on a workspace runs first: it goes to
// the repository's directory, where the workspace's name for it is one
// directory's name written plainly, and becomes a login shell there.
// The name is the daemon's word: one that is anything else is gone to by
// no one, and the shell begins where a shell there begins.
func entering(repository string) string {
	const login = `if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi`
	if repository == "." || repository == ".." || !plainName.MatchString(repository) {
		return login
	}
	// Written from here, so that a name that is a dash is a directory's
	// name and not the directory before.
	return "cd -- ./" + repository + " && " + login
}

// plainName is one directory's name of letters, digits and . _ - +,
// which a shell reads as it is written.
var plainName = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,255}$`)

// notHeld says what of a checkout its remote does not hold, in a
// person's words; empty where it holds it all.
func notHeld(unpushed, uncommitted int) string {
	var parts []string
	if unpushed == 1 {
		parts = append(parts, "1 commit here is not pushed")
	} else if unpushed > 1 {
		parts = append(parts, fmt.Sprintf("%d commits here are not pushed", unpushed))
	}
	if uncommitted == 1 {
		parts = append(parts, "1 file is not committed")
	} else if uncommitted > 1 {
		parts = append(parts, fmt.Sprintf("%d files are not committed", uncommitted))
	}
	return strings.Join(parts, ", and ")
}

// atTerminal is whether a person is at a terminal, to be shown and
// asked: standard input and output are both one. A test stands in for
// it.
var atTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func init() {
	workspaceCmd.AddCommand(openCmd)
	openCmd.Flags().StringVar(&opened.Name, "name", "", "the name of this checkout's workspace; without it, the one the control plane gives its repository and branch")
	openCmd.Flags().BoolVar(&opened.Push, "push", false, "make this checkout's workspace one that may push to its repository, with whatever credential the control plane keeps for its host; said when a workspace is made, and refused of one made without it")
	openCmd.Flags().StringVar(&opened.Image, "image", "", "the box image a new workspace's sandbox runs; without it, the control plane's own")
	openCmd.Flags().StringVar(&opened.Directory, "directory", "", "a directory of the checkout; without it, the one this is run in")
	shellFlags(openCmd)
}

// shellFlags gives a command that opens a workspace the flags that say
// whether it goes on into a shell there.
func shellFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&openShell, "shell", false, "go on into a shell in the workspace, whatever runs this; without it or --no-shell, at a terminal")
	cmd.Flags().BoolVar(&openNoShell, "no-shell", false, "open the workspace and return, at a terminal too")
	cmd.MarkFlagsMutuallyExclusive("shell", "no-shell")
}
