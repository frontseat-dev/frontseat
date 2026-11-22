// Package vcs is what the client asks of a version control system about
// a checkout of this machine: which repository it is of and where it
// stands, so that a workspace of the same repository can be asked for.
// Nothing of the checkout is given to a sandbox; it is only read. Git is
// one such system (cli/internal/vcs/git), and the command
// line chooses it.
package vcs

import (
	"context"
	"errors"
)

// Checkout reads a checkout of this machine.
type Checkout interface {
	// Standing says where the checkout a directory is in stands: the
	// repository it is of, as a sandbox can clone it, and its branch.
	// It reads what the checkout holds and asks no remote.
	Standing(ctx context.Context, directory string) (Standing, error)
	// Same is whether two remotes are of one repository, whatever their
	// spelling.
	Same(one, other string) bool
}

// Standing is where a checkout stands.
type Standing struct {
	// Remote is where the repository is cloned from, over http or https:
	// what a sandbox is let fetch. It names no user and holds no password
	// or token.
	Remote string
	// Converted is whether the checkout writes its remote otherwise than
	// it is cloned: for another transport, so that Remote is the same
	// host and path over https, or with a user before the host, who is
	// left out.
	Converted bool
	// Branch is the branch the checkout is on, which the remote holds
	// under the same name as far as the checkout knows.
	Branch string
	// Unpushed counts the commits of the branch that the remote did not
	// hold when the checkout last heard from it.
	Unpushed int
	// Uncommitted counts the files changed and not committed.
	Uncommitted int
	// Author is who commits here, as the checkout says.
	Author Author
}

// Author is who a commit is by.
type Author struct {
	Name, Email string
}

// Why a checkout gives no workspace. Each says what to do.
var (
	// ErrNoCheckout is a directory that is in no checkout. A checkout that
	// is there and cannot be read is another error, which says why.
	ErrNoCheckout = errors.New("this directory is in no checkout of a repository: run it in one, or make a workspace of the repository: `frontseat workspace create --repository URL`")
	// ErrNoBranch is a checkout that is on no branch.
	ErrNoBranch = errors.New("this checkout is on no branch: switch to one, since a workspace is of a branch")
	// ErrNoRemote is a checkout whose branch has no remote a sandbox can
	// clone from.
	ErrNoRemote = errors.New("this checkout names no remote a sandbox can clone from, over http or https")
	// ErrNotOnRemote is a branch the remote does not hold, as far as the
	// checkout knows.
	ErrNotOnRemote = errors.New("the remote does not hold this branch, as far as this checkout knows: push it first, since a workspace clones what the remote holds")
)
