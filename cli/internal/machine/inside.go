package machine

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/frontseat-dev/frontseat/core/ports"
)

// Inside says whether the command line runs inside a workspace's
// sandbox: where the sandbox's lock (ports.Lock) names a workspace and
// is held now, as the supervisor a control plane started for a
// workspace holds it for as long as it runs. Nothing it is told decides
// it, and no variable: a box is where its supervisor is. A lock that is
// not there, cannot be read by this user, names no workspace, or is
// held by no one, a file an earlier program left behind, is outside.
//
// Not held: whoever can write the lock's directory can hold a lock
// there too, and be taken for a supervisor; what is read inside a box
// is as trusted as that directory is.
func Inside() bool {
	return supervised(ports.Lock)
}

// supervised is whether a lock names a workspace and is held by
// another. It is asked by trying to share it, without waiting: that is
// refused while a supervisor holds it alone, and where it is not, what
// was taken is let go of at once, with the file. Commands that ask at
// once share it, and none takes another for a supervisor.
func supervised(lock string) bool {
	file, err := os.Open(lock)
	if err != nil {
		return false
	}
	defer file.Close()
	said, err := io.ReadAll(io.LimitReader(file, 256))
	if err != nil || strings.TrimSpace(string(said)) == "" {
		return false
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			return errors.Is(err, syscall.EWOULDBLOCK)
		}
	}
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
