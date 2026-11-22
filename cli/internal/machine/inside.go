package machine

import (
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/frontseat-dev/frontseat/local/ports"
)

// Daemon is where the command line asks the daemon of the workspace's
// sandbox it runs inside: the daemon's build API, on the loopback.
var Daemon = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Daemon))

// Inside says whether the command line runs inside a workspace's
// sandbox: where the sandbox's lock (ports.Lock) names the workspace its
// daemon holds, as only a daemon a control plane started for a
// workspace writes it. Nothing it is told decides it, and no variable: a
// box is where its daemon is. A lock that is not there, cannot be read
// by this user, or names no workspace is outside.
func Inside() bool {
	return named(ports.Lock)
}

// named is whether a lock names a workspace.
func named(lock string) bool {
	held, err := os.ReadFile(lock)
	return err == nil && strings.TrimSpace(string(held)) != ""
}
