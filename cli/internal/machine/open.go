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

// Open is the workspace of a checkout of this machine to open: the one
// of the repository and branch it is of.
type Open struct {
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
}

// Opened is the workspace of a checkout that was opened, or as much of
// one as there is (frontseat.Opened), where the checkout stands, which
// says what the workspace is of and what of the checkout it does not
// hold, and the workspace's name for the checkout's repository, which
// is the directory it is in.
type Opened struct {
	frontseat.Opened
	Standing   vcs.Standing
	Repository string
}

// OpenCheckout gives the workspace of the repository and branch a
// checkout is of, asked for by who commits in the checkout and with this
// machine's key. Nothing of the checkout is given to it: what the remote
// does not hold, the workspace does not either. What the workspace holds
// is read, and not taken on its name's word: a workspace of the name
// that holds other source is not this one, and is refused, named.
func OpenCheckout(ctx context.Context, fs *frontseat.Client, o Open, checkout vcs.Checkout) (Opened, error) {
	standing, err := checkout.Standing(ctx, o.Directory)
	if err != nil {
		return Opened{}, err
	}
	keys, err := AuthorizedKeys()
	if err != nil {
		return Opened{Standing: standing}, err
	}
	opened, err := fs.Open(ctx, frontseat.Spec{
		Name:           o.Name,
		Image:          o.Image,
		Repositories:   []frontseat.Repository{{Remote: standing.Remote, Ref: standing.Branch}},
		Author:         frontseat.Author(standing.Author),
		Push:           o.Push,
		AuthorizedKeys: keys,
	})
	out := Opened{Opened: opened, Standing: standing}
	if err != nil {
		return out, err
	}
	for _, held := range opened.Held.GetStatus().GetRepositories() {
		if checkout.Same(held.GetRemote(), standing.Remote) {
			out.Repository = held.GetName()
			return out, nil
		}
	}
	return out, errors.New("the workspace of this name holds other source than the repository asked for")
}
