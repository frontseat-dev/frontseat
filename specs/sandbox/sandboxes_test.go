package sandbox_test

import (
	. "github.com/onsi/gomega"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A workspace lives in a sandbox: a machine of its own, with its
// supervisor in it, and the build daemon a command there starts. You never start one by hand. You ask for a workspace, and the
// server you ask makes the sandbox, on your own machine with a container
// runtime, or on the platform.
//
// Everything you then do reaches the sandbox through that same server:
// building, looking at what it holds, opening a shell. Nothing of the
// sandbox is exposed on its own.
//
// A workspace's source is in its sandbox: the supervisor clones it there, and
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

// Workspaces in sandboxes / A sandbox holds one supervisor, which stands in its workspace and knows its name, and a build there starts its own daemon.
func TestASandboxHoldsOneSupervisorWhichStandsInItsWorkspaceAndKnowsItsName(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(strings.Fields(workspace.prints(supervisors))).To(HaveLen(1))
	spec.Expect(workspace.prints("for p in $(" + supervisors + "); do readlink $p/cwd; done")).To(Equal("/work"))
	// It knows its name, as the sandbox's lock says it, which is how the
	// command line there knows it runs inside a workspace's sandbox.
	spec.Expect(workspace.prints("head -1 /var/lib/frontseat/box.lock")).To(Equal(workspace.Workspace))

	// A second is refused, whatever it is told: of the same workspace, or
	// of another.
	for _, told := range []string{workspace.Workspace, "another"} {
		printed, exit := workspace.run("/opt/frontseat/bin/frontseat-box --source 127.0.0.1:7699 --workspace /tmp --name " + told + " 2>&1")
		spec.Expect(exit).NotTo(Equal(0))
		spec.Expect(printed).To(And(ContainSubstring("one supervisor"), ContainSubstring(workspace.Workspace)))
	}
	spec.Expect(strings.Fields(workspace.prints(supervisors))).To(HaveLen(1))
	// The supervisor builds nothing. A command that builds starts the
	// build daemon of the workspace's directory, as on any machine, told
	// what the supervisor wrote of the grid, and the next finds that one:
	// there is one, and it is the command line's own program.
	spec.Expect(workspace.exits("frontseat build list")).To(Equal(0))
	spec.Expect(workspace.exits("frontseat build list")).To(Equal(0))
	daemons := strings.Fields(workspace.prints(buildDaemons))
	spec.Expect(daemons).To(HaveLen(1))
	spec.Expect(workspace.prints("readlink " + daemons[0] + "/cwd")).To(Equal("/work"))
	spec.Expect(workspace.exits("test -s /var/lib/frontseat/build.yaml")).To(Equal(0))
}

// Workspaces in sandboxes / A workspace's build is read from outside its sandbox by its owner, through a port carried there.
//
// A workspace is built from inside it, and no control plane serves or
// forwards the build API. What its owner reads of a build from outside
// is asked of the build daemon in the sandbox, over a port the control
// plane carries as bytes (testbuild).
func TestAWorkspacesBuildIsReadFromOutsideByItsOwnerThroughAPortCarriedThere(t *testing.T) {
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
//
// Its owner is carried to its shell by the control plane, by the
// workspace's name; no address of the sandbox's own is said, and none of
// a build API, which the control plane neither serves nor forwards.
func TestTheSandboxIsReachedThroughTheServerThatMadeIt(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(workspace.prints("echo reached")).To(Equal("reached"))
	for _, call := range []string{"/workspaces/" + workspace.Workspace + "/build/frontseat.build.v1alpha1.BuildService/ListBuilds", "/frontseat.build.v1alpha1.BuildService/ListBuilds"} {
		status, said := spec.postAs(workspace.Server+call, ownerToken(workspace.Server))
		spec.Expect(status).To(Equal(http.StatusNotFound), call+": "+said)
	}
}

// Workspaces in sandboxes / A workspace's sandbox is made by OpenShell.
func TestAWorkspaceSSandboxIsMadeByOpenShell(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	spec.Expect(workspace.madeBy()).To(Equal("openshell"))
}

// Workspaces in sandboxes / What builds reaches the grid, and another program a shell starts does not.
//
// The way to the grid is for one program, the command line, which is
// the build daemon it starts too: the gateway lets a connection through
// for that program or any that started it. So a build in the sandbox
// reaches the grid, and neither a program run in the workspace's shell
// that is not the command line, which the box's supervisor serves, nor
// one the server runs beside it does.
//
// It proves no more than that. The command line is let through whoever
// starts it and whatever it is told: it runs a program of the
// workspace's (frontseat run) and is the build daemon of any settings
// (frontseat daemon --settings), so whatever runs in the box reaches
// the grid by one command of it. The rule keeps a shell's other
// programs from the grid by accident, and is no boundary against what
// runs in the box (AGENTS.md, "The command line asks one server", Not
// held).
func TestWhatBuildsReachesTheGridAndAnotherProgramAShellStartsDoesNot(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()
	// The grid is the one the box's supervisor was told by its flags,
	// and wrote where a build reads its settings.
	grid := workspace.prints(`for p in $(` + supervisors + `); do tr '\0' '\n' < $p/cmdline | sed -n 's/^--grid=//p'; done`)
	spec.Expect(grid).To(MatchRegexp(`^[a-z.-]+:[0-9]+$`))
	spec.Expect(workspace.prints("cat /var/lib/frontseat/build.yaml")).To(ContainSubstring(grid))
	// It opens a connection to that grid, and says so, or says why not.
	connecting := `import socket, sys
host, port = sys.argv[1].rsplit(":", 1)
try:
    socket.create_connection((host, int(port)), 5).close()
    print("connected")
except OSError as refused:
    print("refused:", refused)`
	// What builds reaches it: a build's actions run there.
	spec.Expect(workspace.build().outcome()).To(Equal("SUCCESS"))
	// The same connection, opened by a program of the workspace's shell,
	// which the supervisor started and no command that builds, is denied,
	// which is how the sandbox's policy says no, and not unable to find
	// the grid or left waiting.
	spec.Expect(workspace.prints("python3 -c '" + connecting + "' " + grid)).To(HavePrefix("refused: [Errno 13]"))
	// And so is one the server runs in the sandbox beside them.
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
