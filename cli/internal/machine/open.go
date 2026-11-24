package machine

import (
	"context"
	"errors"

	"github.com/frontseat-dev/frontseat/cli/internal/vcs"
	"github.com/frontseat-dev/frontseat/cli/internal/vcs/git"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

// Checkout is the version control system a checkout of this machine is
// read with: git, the default.
func Checkout() vcs.Checkout { return git.System{} }

// OpenOptions is the workspace of a checkout of this machine to open: the one
// of the repository and branch it is of.
type OpenOptions struct {
	// Name names the workspace; empty for the name the control plane
	// gives a repository and a branch.
	Name string
	// Image is the box image a sandbox made for it runs; empty for the
	// one the control plane says is its own.
	Image string
	// Directory is a directory of the checkout.
	Directory string
	// Push asks for a workspace whose sandbox may push to the
	// repository.
	Push bool
	// Remote asks for a remote workspace: the repository is held from
	// the control plane's snapshot store and not cloned, and what a test
	// that passes there tested lands on the workspace's task. Without
	// it the workspace is local, and the repository is cloned in it.
	Remote bool
	// Task names the task a remote workspace works on; empty for one
	// named for the workspace.
	Task string
}

// CheckoutOpened is the workspace of a checkout that was opened, or as
// much of one as there is (frontseat.OpenResult), where the checkout
// stands, which says what the workspace is of and what of the checkout
// it does not hold, and the workspace's name for the checkout's
// repository, which is the directory it is in.
type CheckoutOpened struct {
	frontseat.OpenResult
	Standing   vcs.Standing
	Repository string
}

// OpenCheckout gives the workspace of the repository and branch a
// checkout is of, asked for by who commits in the checkout and with this
// machine's key. Nothing of the checkout is given to it: what the remote
// does not hold, the workspace does not either. What the workspace holds
// is read, and not taken on its name's word: a workspace of the name
// that holds other source is not this one, and is refused, named.
//
// The workspace is local unless it is asked for remote: then the
// repository is held from the control plane's snapshot store, by the
// name its version control system gives it, on a task, and what passes
// its tests lands there. A local workspace that holds the repository as
// a clone is not the remote one, nor the other way round.
func OpenCheckout(ctx context.Context, fs *frontseat.Client, o OpenOptions, checkout vcs.Checkout) (CheckoutOpened, error) {
	standing, err := checkout.Standing(ctx, o.Directory)
	if err != nil {
		return CheckoutOpened{}, err
	}
	key, err := AuthorizedKey()
	if err != nil {
		return CheckoutOpened{Standing: standing}, err
	}
	spec := frontseat.WorkspaceSpec{
		Name:          o.Name,
		Image:         o.Image,
		Repositories:  []frontseat.Repository{{Remote: standing.Remote, Ref: standing.Branch}},
		Author:        frontseat.Author(standing.Author),
		Push:          o.Push,
		AuthorizedKey: key,
	}
	var stored frontseat.StoredRepository
	if o.Remote {
		// The repository as the store names it; on the task, and so at no
		// branch: what the checkout's branch holds is not asked for.
		if stored, err = Stored(frontseat.Repository{Remote: standing.Remote}, checkout.NameOf); err != nil {
			return CheckoutOpened{Standing: standing}, err
		}
		spec.Repositories, spec.Stored, spec.Task, spec.Land = nil, []frontseat.StoredRepository{stored}, o.Task, true
	}
	opened, err := fs.Open(ctx, spec)
	out := CheckoutOpened{OpenResult: opened, Standing: standing}
	if err != nil {
		return out, err
	}
	for _, held := range opened.Status.GetStatus().GetRepositories() {
		if (o.Remote && held.GetStored() == stored.Repository) || (!o.Remote && held.GetStored() == "" && checkout.Same(held.GetRemote(), standing.Remote)) {
			out.Repository = held.GetName()
			return out, nil
		}
	}
	return out, errors.New("the workspace of this name holds other source than the repository asked for")
}
