// Package git is git, as the client asks a version control system about
// a checkout of this machine. It runs the git a person has, and reads
// only what the checkout holds: it asks no remote, so it needs no
// credential and waits on no network. It is run, and a remote is read,
// as frontseat's one package for git does it (common/git): hardened
// against what a checkout's configuration names, given none of the git
// variables of the environment, and offline.
package git

import (
	"context"
	"fmt"
	osexec "os/exec"
	"strconv"
	"strings"

	"github.com/frontseat-dev/frontseat/cli/internal/vcs"
	rungit "github.com/frontseat-dev/frontseat/common/git"
)

// System is git.
type System struct{}

var _ vcs.Checkout = System{}

// Standing implements vcs.Checkout.
func (System) Standing(ctx context.Context, directory string) (vcs.Standing, error) {
	if _, err := osexec.LookPath("git"); err != nil {
		return vcs.Standing{}, fmt.Errorf("git is not found on this machine, and a checkout is read with it: %w", err)
	}
	// What the checkout's own configuration would have git run is turned
	// off before anything else is asked of it.
	quiet := rungit.In(ctx, directory, rungit.Options{Offline: true})
	ask := func(args ...string) (string, error) {
		out, err := quiet.Run(ctx, args...)
		return strings.TrimSpace(out), err
	}
	if _, err := ask("rev-parse", "--show-toplevel"); err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return vcs.Standing{}, vcs.ErrNoCheckout
		}
		// A checkout git will not read, and why, as git says it.
		return vcs.Standing{}, fmt.Errorf("this checkout is not read: %w", err)
	}
	branch, err := ask("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch == "" {
		return vcs.Standing{}, vcs.ErrNoBranch
	}
	// The remote the branch follows, and else the one a clone names: not
	// the first of several, which may be another's repository.
	remote, err := ask("config", "--get", "branch."+branch+".remote")
	if err != nil || remote == "" || remote == "." {
		remote = "origin"
	}
	// What a branch follows may be an address, as git push -u with one
	// writes it, which may hold a token: a remote is a name git takes for
	// one, by its own rule, and anything else is not repeated.
	if _, err := ask("check-ref-format", "refs/remotes/"+remote+"/branch"); err != nil {
		return vcs.Standing{}, fmt.Errorf("%w: the branch %s follows an address and no remote of the checkout: give it a remote (git branch --set-upstream-to)", vcs.ErrNoRemote, branch)
	}
	// The remote as the checkout writes it, and not as a person's own
	// settings would rewrite it (url.*.insteadOf), which is where a token
	// is sometimes kept.
	written, err := ask("config", "--get-all", "remote."+remote+".url")
	if err != nil || written == "" {
		return vcs.Standing{}, fmt.Errorf("%w: the branch %s follows %q, which is no remote of it", vcs.ErrNoRemote, branch, remote)
	}
	// One remote of several addresses is fetched from one and read here
	// as another: which is the repository is then not this to say.
	if strings.Contains(written, "\n") {
		return vcs.Standing{}, fmt.Errorf("%w: %s is written with several addresses: give it one", vcs.ErrNoRemote, remote)
	}
	address, err := rungit.Over(written)
	if err != nil {
		// Said without the remote as it is written, which may hold a
		// password or a token.
		return vcs.Standing{}, fmt.Errorf("%w: %s is %w", vcs.ErrNoRemote, remote, err)
	}
	// The remote's branch of the same name, by the checkout's own record
	// of it and no question to the remote. A branch that follows another
	// name there (one made from the remote's main follows main) is not
	// that other branch: its workspace is its own, once it is pushed.
	held := "refs/remotes/" + remote + "/" + branch
	if _, err := ask("rev-parse", "--verify", "--quiet", held+"^{commit}"); err != nil {
		return vcs.Standing{}, fmt.Errorf("%w (%s, on %s)", vcs.ErrNotOnRemote, branch, remote)
	}
	standing := vcs.Standing{Remote: address.Cloned, Converted: address.Converted, Branch: branch}
	counted, err := ask("rev-list", "--count", held+"..HEAD")
	if err != nil {
		return vcs.Standing{}, fmt.Errorf("counting what is not pushed: %w", err)
	}
	if standing.Unpushed, err = strconv.Atoi(counted); err != nil {
		return vcs.Standing{}, fmt.Errorf("counting what is not pushed: %w", err)
	}
	// Every file, whatever a person's settings say of files git does not
	// follow, and nothing of a repository within this one, whose own
	// configuration is another's.
	changed, err := quiet.Run(ctx, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=all")
	if err != nil {
		return vcs.Standing{}, fmt.Errorf("counting what is not committed: %w", err)
	}
	standing.Uncommitted = strings.Count(changed, "\n")
	// Who commits here; a checkout that says no one names no one.
	standing.Author.Name, _ = ask("config", "--get", "user.name")
	standing.Author.Email, _ = ask("config", "--get", "user.email")
	return standing, nil
}

// NameOf implements vcs.Checkout: a repository is named as git names the
// directory it clones its remote into.
func (System) NameOf(remote string) string { return rungit.Name(remote) }

// Same implements vcs.Checkout: two remotes are one repository where
// they are what a sandbox clones as the same, whatever their spelling.
func (System) Same(one, other string) bool {
	first, err := rungit.Over(one)
	if err != nil {
		return one == other
	}
	second, err := rungit.Over(other)
	return err == nil && first.Repository == second.Repository
}
