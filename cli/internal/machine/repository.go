package machine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go"
)

// Repository reads a repository as the command line names one: its
// remote, or its remote, a # and the branch or tag to stand at.
//
// A remote is a URL, and in a URL a # ends the address. So the first #
// is where the remote ends, and all that follows is the branch or tag,
// taken as it is written: a branch may hold a # or an @ of its own. A #
// that is part of a remote is written %23, as a URL writes one.
func Repository(written string) (frontseat.Repository, error) {
	remote, ref, at := strings.Cut(written, "#")
	switch {
	case remote == "":
		return frontseat.Repository{}, fmt.Errorf("%q names no repository: a repository is URL or URL#REF", written)
	case at && ref == "":
		return frontseat.Repository{}, fmt.Errorf("%q names no branch or tag after its #", written)
	}
	return frontseat.Repository{Remote: remote, Ref: ref}, nil
}

// Stored is a repository named by its remote as a remote workspace holds
// it: from the control plane's snapshot store, by the name nameOf gives
// the remote, which is how its version control system names a clone of
// it. It is held on the workspace's task, and so at no branch or tag:
// one named with a ref is refused. Nothing of the remote is repeated.
func Stored(named frontseat.Repository, nameOf func(remote string) string) (frontseat.StoredRepository, error) {
	if named.Ref != "" {
		return frontseat.StoredRepository{}, errors.New("a repository is named with a branch or tag, and a remote workspace holds a repository on its task: name it without its #REF")
	}
	name := nameOf(named.Remote)
	if name == "" {
		return frontseat.StoredRepository{}, errors.New("a repository's remote gives it no name, and a remote workspace holds a repository by its name in the store")
	}
	return frontseat.StoredRepository{Repository: name}, nil
}
