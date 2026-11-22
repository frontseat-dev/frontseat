package sandbox_test

import (
	. "github.com/onsi/gomega"
	"strings"
	"testing"
	"time"
)

// A workspace lives in a sandbox: a machine of its own, with the daemon
// in it. You never start one by hand. You ask for a workspace, and the
// server you ask makes the sandbox, on your own machine with a container
// runtime, or on the platform.
//
// Everything you then do reaches the sandbox through that same server:
// building, looking at what it holds, opening a shell. Nothing of the
// sandbox is exposed on its own.
//
// A workspace's source is in its sandbox: the daemon clones it there, and
// no directory of your machine is given to a sandbox. The specs use a
// workspace that cloned a repository holding one Go module, hello.
// Workspaces in sandboxes / The repository you name is what the workspace holds.
func TestTheRepositoryYouNameIsWhatTheWorkspaceHolds(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(workspace.prints("ls " + code)).To(Equal("frontseat.yaml hello mise.lock mise.toml"))
}

// Workspaces in sandboxes / What runs in a sandbox is not root, and the sandbox mounts nothing of the grid.
func TestWhatRunsInASandboxIsNotRootAndTheSandboxMountsNothingOfTheGrid(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	// The box's own user, which the image names: not root.
	spec.Expect(workspace.prints("id -u")).To(Equal("65532"))
	// What it mounts was read, and nothing of it is a FUSE mount.
	mounts := workspace.prints("cat /proc/self/mountinfo")
	spec.Expect(mounts).To(ContainSubstring(" / "))
	spec.Expect(mounts).NotTo(ContainSubstring(" - fuse"))
	// It was given no device to mount with, and may not be given the
	// leave to: SYS_ADMIN, capability 21, is not among those it could
	// ever hold, whoever it ran as.
	spec.Expect(workspace.exits("test -e /dev/fuse")).To(Equal(1))
	spec.Expect(workspace.boundingCapabilities() & (1 << 21)).To(BeZero())
	// The control: it is still a workspace that builds. That the
	// capabilities were read is the reading's own to hold: a runtime
	// may leave a sandbox none at all.
	spec.Expect(workspace.build().outcome()).To(Equal("SUCCESS"))
}

// Workspaces in sandboxes / A sandbox holds one daemon, which stands in its workspace and knows its name.
func TestASandboxHoldsOneDaemonWhichStandsInItsWorkspaceAndKnowsItsName(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	daemons := "for p in /proc/[0-9]*; do [ \"$(readlink $p/exe 2>/dev/null)\" = /opt/frontseat/bin/frontseatd ] && echo $p; done"
	spec.Expect(strings.Fields(workspace.prints(daemons))).To(HaveLen(1))
	spec.Expect(workspace.prints("for p in $(" + daemons + "); do readlink $p/cwd; done")).To(Equal("/work"))
	// It knows its name, as the sandbox's lock says it, which is how the
	// command line there knows it runs inside a workspace's sandbox.
	spec.Expect(workspace.prints("head -1 /var/lib/frontseat/daemon.lock")).To(Equal(workspace.Workspace))

	printed, exit := workspace.run("/opt/frontseat/bin/frontseatd --listen 127.0.0.1:7699 --workspace /tmp 2>&1")
	spec.Expect(exit).NotTo(Equal(0))
	spec.Expect(printed).To(And(ContainSubstring("one daemon"), ContainSubstring(workspace.Workspace)))
	// Told a state of its own, it is refused all the same.
	printed, exit = workspace.run("/opt/frontseat/bin/frontseatd --listen 127.0.0.1:7699 --workspace /tmp --state /tmp/state 2>&1")
	spec.Expect(exit).NotTo(Equal(0))
	spec.Expect(printed).To(And(ContainSubstring("one daemon"), ContainSubstring(workspace.Workspace)))
	spec.Expect(strings.Fields(workspace.prints(daemons))).To(HaveLen(1))
	spec.Expect(workspace.exits("frontseat build list")).To(Equal(0))
}

// Workspaces in sandboxes / You build from where you are, outside the sandbox.
func TestYouBuildFromWhereYouAreOutsideTheSandbox(t *testing.T) {
	spec := newScenario(t)

	build := spec.theWorkspace().build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
}

// Workspaces in sandboxes / You build from inside the sandbox, with the same command.
func TestYouBuildFromInsideTheSandboxWithTheSameCommand(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(workspace.exits("frontseat build")).To(Equal(0))
}

// Workspaces in sandboxes / The sandbox is reached through the server that made it.
func TestTheSandboxIsReachedThroughTheServerThatMadeIt(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(workspace.Build).To(Equal(server + "/workspaces/" + workspace.Workspace + "/build"))
}

// Workspaces in sandboxes / A workspace's sandbox is made by OpenShell.
func TestAWorkspaceSSandboxIsMadeByOpenShell(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(workspace.madeBy()).To(Equal("openshell"))
}

// Workspaces in sandboxes / The grid is reached by the daemon and what it starts, and by nothing else in the sandbox.
func TestTheGridIsReachedByTheDaemonAndWhatItStartsAndByNothingElseInTheSandbox(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()
	// The grid is the one the daemon was told by its flags.
	grid := workspace.prints(`for p in /proc/[0-9]*; do [ "$(readlink $p/exe 2>/dev/null)" = /opt/frontseat/bin/frontseatd ] && tr '\0' '\n' < $p/cmdline | sed -n 's/^--grid=//p'; done`)
	spec.Expect(grid).To(MatchRegexp(`^[a-z.-]+:[0-9]+$`))
	// It opens a connection to that grid, and says so, or says why not.
	connecting := `import socket, sys
host, port = sys.argv[1].rsplit(":", 1)
try:
    socket.create_connection((host, int(port)), 5).close()
    print("connected")
except OSError as refused:
    print("refused:", refused)`
	// What the daemon starts reaches it, as the daemon does: a shell
	// in the workspace is the daemon's.
	spec.Expect(workspace.prints("python3 -c '" + connecting + "' " + grid)).To(Equal("connected"))
	// The same program, run in the sandbox by the server and not by
	// the daemon, does not: it is denied the connection, which is how
	// the sandbox's policy says no, and not unable to find the grid
	// or left waiting.
	spec.Expect(workspace.ranBeside("python3", "-c", connecting, grid)).To(HavePrefix("refused: [Errno 13]"))
}

// Workspaces in sandboxes / What cannot be given is refused, never quietly weakened.
func TestWhatCannotBeGivenIsRefusedNeverQuietlyWeakened(t *testing.T) {
	spec := newScenario(t)

	spec.Expect(askingForASandboxThatReachesAnything()).To(BeRefusedSaying("a sandbox that reaches anything"))
}

// Workspaces in sandboxes / A sandbox can be paused.
func TestASandboxCanBePaused(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	workspace.pause()

	spec.Expect(workspace.state()).To(Equal("paused"))
}

// Workspaces in sandboxes / A paused sandbox is picked up again.
func TestAPausedSandboxIsPickedUpAgain(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace().pause()

	workspace.resume()

	// Resumed, it starts, and runs.
	spec.Eventually(workspace.state, 2*time.Minute, time.Second).Should(Equal("running"))
}

// Workspaces in sandboxes / A workspace that is deleted is gone, sandbox and all.
func TestAWorkspaceThatIsDeletedIsGoneSandboxAndAll(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	workspace.delete()

	spec.Expect(workspace.askingAfterItsSandbox()).To(BeRefusedSaying("no sandbox has the id"))
}
