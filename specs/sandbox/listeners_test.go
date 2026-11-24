package sandbox_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/frontseat-dev/frontseat/core/ports"
)

// listening is a program for a sandbox: it prints each TCP port that
// is listened on where a connection to the sandbox's loopback reaches
// it, at a loopback address or at every address, as the kernel says
// them (/proc/net/tcp, /proc/net/tcp6), on a line of its own: the
// port, the address it is bound to, and the program that holds it,
// where a process this user may read does ("?" where none does, as for
// one of another user's).
const listening = `import os
held = {}
for pid in filter(str.isdigit, os.listdir("/proc")):
    try:
        program = open("/proc/" + pid + "/cmdline", "rb").read().split(b"\0")[0].decode() or "?"
        for fd in os.listdir("/proc/" + pid + "/fd"):
            link = os.readlink("/proc/" + pid + "/fd/" + fd)
            if link.startswith("socket:["):
                held[link[8:-1]] = program.replace(" ", "_")
    except OSError:
        pass
anywhere = {"00000000": "0.0.0.0", "0" * 32: "::", "0" * 24 + "01000000": "::1", "0" * 16 + "FFFF0000" + "0100007F": "::ffff:127.0.0.1"}
for table in ("/proc/net/tcp", "/proc/net/tcp6"):
    try:
        lines = open(table).read().splitlines()[1:]
    except OSError:
        continue
    for line in lines:
        field = line.split()
        address, port = field[1].rsplit(":", 1)
        if field[3] != "0A":
            continue
        if len(address) == 8 and address.endswith("7F"):
            bound = "127." + ".".join(str(int(address[i:i + 2], 16)) for i in (4, 2, 0))
        elif address in anywhere:
            bound = anywhere[address]
        else:
            continue
        print(str(int(port, 16)) + "|" + bound + "|" + held.get(field[9], "?"))`

// expectedListeners is what listens in a box, reached at its loopback,
// that the specs expect: the one place they are said. A listener that
// is not here fails
// TestNothingListensOnABoxsLoopbackThatTheSpecsDoNotExpect.
//
// By its port: what the box's supervisor serves (stack/box.Dockerfile).
// The source API's is carried to no one by the control plane's port
// route (PORT_NOT_SERVED); the shell's is the route to the shell.
//
// And the sandbox's resolver, OpenShell's, at the port of DNS on an
// address of the loopback of its own (127.0.0.53), found by the first
// run of this spec on 2026-11-24. It answers names as the sandbox's
// policy lets them be reached and nothing else, and whoever owns the
// workspace asks it already from a shell in the box: reached through
// the route, it gives the owner nothing more. Nothing else of
// OpenShell's listens on a box's loopback: no proxy of its egress is
// reached this way.
var expectedListeners = map[int]string{
	ports.Source: "the supervisor's source API",
	ports.Shell:  "the supervisor's shell",
	53:           "the sandbox's resolver, OpenShell's",
}

// builder is the one program expected to listen at a port of its own
// choosing, on the loopback alone: the command line, as the build
// daemon of the workspace's directory (controlplane.Builder), once a
// command that builds has started one.
const builder = "/opt/frontseat/bin/frontseat"

// Workspaces in sandboxes / Nothing listens on a box's loopback that the specs do not expect.
//
// A probe of an open question, and no rule yet. The control plane's
// port route carries a workspace's owner to any port of the box's
// loopback but the source API's, one the sandbox never said it opens
// among them. If something of the sandbox's own listens there, as an
// agent or an egress proxy of what provides the sandbox might, the
// owner is carried to it too, and what it sees connect is the
// gateway's forwarder and no program its policy knows.
//
// A FAILURE HERE MEANS: the listener named is reachable by the
// workspace's owner, from outside, through
// /workspaces/{name}/ports/{port} of the control plane. Decide whether
// the route must refuse it (as it refuses the source API's port,
// PORT_NOT_SERVED), and then either refuse it there or add it to
// expectedListeners with why it may be reached. It does not mean the
// box is broken.
func TestNothingListensOnABoxsLoopbackThatTheSpecsDoNotExpect(t *testing.T) {
	spec := newScenario(t)
	workspace := spec.inASandboxOfItsOwn(spec.cloning(spec.aRepositoryOfHello()))
	// Its build daemon is started, so that it listens too: the most a
	// box listens on of Frontseat's own.
	workspace.daemon()

	listed := strings.Fields(workspace.prints("python3 -c '" + listening + "'"))

	// The control: the listing sees what is known to listen.
	seen := map[int]bool{}
	var unexpected []string
	for _, each := range listed {
		field := strings.Split(each, "|")
		spec.Expect(field).To(HaveLen(3), "a listener was listed as %q", each)
		port, err := strconv.Atoi(field[0])
		spec.Expect(err).NotTo(HaveOccurred(), "a listener was listed as %q", each)
		seen[port] = true
		_, expected := expectedListeners[port]
		daemon := field[2] == builder && strings.HasPrefix(field[1], "127.")
		if !expected && !daemon {
			unexpected = append(unexpected, fmt.Sprintf("port %s, bound to %s, held by %s", field[0], field[1], field[2]))
		}
	}
	for port, what := range expectedListeners {
		spec.Expect(seen[port]).To(BeTrue(), "the control: %s, at the port %d, was not listed among %v: the listing does not see what listens", what, port, listed)
	}
	if len(unexpected) != 0 {
		t.Errorf("in a fresh workspace's box these listen where a connection to its loopback reaches them, and the specs expect none of them:\n  %s\nEach is reachable by the workspace's owner, from outside, through the control plane's port route (/workspaces/{name}/ports/{port}): decide whether the route must refuse it, and refuse it there or add it to expectedListeners with why it may be reached.", strings.Join(unexpected, "\n  "))
	}
}
