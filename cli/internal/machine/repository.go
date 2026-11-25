package machine

import (
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
