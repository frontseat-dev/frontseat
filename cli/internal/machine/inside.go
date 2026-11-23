package machine

import (
	"os"
	"strings"

	"github.com/frontseat-dev/frontseat/core/ports"
)

// Inside says whether the command line runs inside a workspace's
// sandbox: where the sandbox's lock (ports.Lock) names the workspace its
// supervisor holds, as only a supervisor a control plane started for a
// workspace writes it. Nothing it is told decides it, and no variable: a
// box is where its supervisor is. A lock that is not there, cannot be
// read by this user, or names no workspace is outside.
func Inside() bool {
	return named(ports.Lock)
}

// named is whether a lock names a workspace.
func named(lock string) bool {
	held, err := os.ReadFile(lock)
	return err == nil && strings.TrimSpace(string(held)) != ""
}

// kept is where the build daemons of where the command line runs are
// told and keep what they keep: the file of their settings, and the
// directory beneath which each has a state directory of its own.
type kept struct {
	settings, state string
}

// boxed is where they are inside a workspace's sandbox: the settings the
// sandbox's supervisor wrote, and a directory of the sandbox's own that
// its user writes. A test stands in for it.
var boxed = kept{settings: ports.Settings, state: ports.Builds}

// keeps is where the build daemons are told and keep what they keep,
// inside a workspace's sandbox or outside one: there what the sandbox
// says, and here beneath the command line's one root, build.yaml and
// build/.
func keeps(inside bool) (kept, error) {
	if inside {
		return boxed, nil
	}
	settings, err := beneath("build.yaml")
	if err != nil {
		return kept{}, err
	}
	state, err := beneath("build")
	if err != nil {
		return kept{}, err
	}
	return kept{settings: settings, state: state}, nil
}
