package cmd

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

var (
	opened    machine.Open
	openWhole bool
)

// openCmd represents the open command
var openCmd = &cobra.Command{
	Use:   "open [WORKSPACE | REPOSITORY... | ENTITY...]",
	Short: "Open a workspace: this checkout's, a repository's, entities', or one there is",
	Long: `Open a workspace, the one there is, as it was left, or a new one, and make
it the workspace the other commands ask.

With nothing named, it is the workspace of the repository this checkout is
of, at the branch it is on, cloned from the checkout's remote. Named a
repository (URL or URL#REF) or entities (kind:name), it is a workspace of
those: the control plane resolves entities through the Backstage catalog
its operator gives it, and refuses them where it has none. Named a workspace there is, by its name, it is that
one.

A workspace is given a name by what it was first asked for, or the one
--name gives. Asked for again, it is the same workspace. It is the
control plane's every command asks (frontseat --help).

At a terminal it goes on into a shell there, by this machine's SSH
client; leaving the shell leaves the workspace as it is. Run by a script
or an agent, it opens the workspace and returns.

Nothing of this machine is given to it. A workspace holds what the remote
holds: commits that are not pushed and files that are not committed are
not in it, and are counted so that you know.

What is done in a workspace leaves it by a push from it, which only a
workspace made with --push may do: to its own repositories and no other,
as far as the credential the control plane keeps lets it.`,
	Example: `  frontseat workspace open
  frontseat workspace open https://example.com/acme/services.git#main
  frontseat workspace open component:payments
  frontseat workspace open services-main`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		fs, err := server()
		if err != nil {
			return err
		}
		if len(args) > 0 {
			return openNamed(ctx, fs, args, cmd.Flags())
		}
		open := opened
		if open.Directory == "" {
			here, err := os.Getwd()
			if err != nil {
				return err
			}
			open.Directory = here
		}
		got, err := machine.OpenCheckout(ctx, fs, open, machine.Checkout())
		if err := kept(fs, got.Opened, err); err != nil {
			return err
		}
		standing := got.Standing
		said("%s  %s", standing.Remote, standing.Branch)
		if standing.Converted {
			said("  this checkout writes its remote otherwise: the same host and path are cloned as above, and no user it names is sent on")
		}
		if missing := notHeld(standing.Unpushed, standing.Uncommitted); missing != "" {
			said("  %s: the workspace does not hold them", missing)
		}
		said("%s  %s", got.Workspace.Name(), how(got.How))
		if got.Held.GetSpec().GetPush() {
			said("  it may push to this repository, with whatever credential the control plane keeps for its host")
		}
		return entered(ctx, got.Repository)
	},
}

// kept makes a workspace that was opened the one the other commands
// ask; of one that could not be opened, it says that it is still there.
func kept(fs *frontseat.Client, got frontseat.Opened, err error) error {
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

// workspaceName is one word that can be a workspace's name, and so is
// read as one: an entity is written with its kind.
var workspaceName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,46}[a-z0-9])?$`)

// openNamed opens what was named: a workspace there is, by its name, or
// a workspace of the repositories or the entities named.
func openNamed(ctx context.Context, fs *frontseat.Client, args []string, flags *pflag.FlagSet) error {
	key, err := machine.AuthorizedKey()
	if err != nil {
		return err
	}
	spec := frontseat.Spec{AuthorizedKey: key}
	if len(args) == 1 && workspaceName.MatchString(args[0]) {
		// What a workspace is was said when it was made, and is not asked
		// again of one that is there.
		for _, said := range []string{"name", "push", "image", "whole"} {
			if flags.Changed(said) {
				return fmt.Errorf("--%s is said of a workspace when it is made, and %s is one there is: open it by its name alone", said, args[0])
			}
		}
		spec.Name = args[0]
	} else {
		spec = frontseat.Spec{Name: opened.Name, Image: opened.Image, Push: opened.Push, Whole: openWhole, AuthorizedKey: key}
		for _, named := range args {
			switch {
			case strings.Contains(named, "://"):
				repository, err := machine.Repository(named)
				if err != nil {
					return err
				}
				spec.Repositories = append(spec.Repositories, repository)
			case strings.Contains(named, "@") && strings.Contains(named, ":"):
				return fmt.Errorf("a workspace clones a repository over http or https, and is given its address so: %q is not one", named)
			default:
				if err := typed([]string{named}); err != nil {
					return fmt.Errorf("%q is no workspace's name, no repository's address and no entity: %w", named, err)
				}
				spec.Entities = append(spec.Entities, named)
			}
		}
	}
	got, err := fs.Open(ctx, spec)
	if err := kept(fs, got, err); err != nil {
		return err
	}
	came := "as it was left"
	if got.How == frontseat.Made {
		came = "made"
	}
	said("%s  %s", got.Workspace.Name(), came)
	// A workspace of one repository is entered in it, and one of several
	// above them.
	in := ""
	if holds := got.Held.GetStatus().GetRepositories(); len(holds) == 1 {
		in = holds[0].GetName()
	}
	return entered(ctx, in)
}

// entered goes on, at a terminal, into a shell in a workspace that was
// opened or made: a person who opens a workspace means to be in it. What
// is not at a terminal, a script or an agent, has no one to type, and is
// given what was opened and nothing more. The workspace was opened
// whatever is then done in the shell: how the shell's last command ended
// is not how this command ends, and only an SSH client that could not
// get in (255, its own) is.
func entered(ctx context.Context, repository string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return nil
	}
	err := shell(ctx, []string{"-t", "frontseat-workspace", entering(repository)})
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

// how says how a workspace came to be the one opened.
func how(came frontseat.How) string {
	switch came {
	case frontseat.Made:
		return "made"
	case frontseat.Filled:
		return "filled: it was there and held nothing yet"
	default:
		return "as it was left"
	}
}

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

func init() {
	workspaceCmd.AddCommand(openCmd)
	openCmd.Flags().StringVar(&opened.Name, "name", "", "the workspace's name; without it, the one the control plane gives what is asked for")
	openCmd.Flags().BoolVar(&opened.Push, "push", false, "make the workspace one that may push to its repositories, with whatever credential the control plane keeps for their hosts; said when a workspace is made, and refused of one made without it")
	openCmd.Flags().BoolVar(&openWhole, "whole", false, "clone the entities' repositories whole, not only the directories the entities govern")
	openCmd.Flags().StringVar(&opened.Image, "image", "", "the box image a new workspace's sandbox runs; without it, the control plane's own")
	openCmd.Flags().StringVar(&opened.Directory, "directory", "", "a directory of the checkout; without it, the one this is run in")
}
