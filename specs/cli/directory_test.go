package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
)

// With no workspace selected, the command line builds the directory it
// is run in, on the grid this machine was told (build.yaml), through a
// build daemon it starts for itself: no control plane is there to ask,
// and none is asked. The daemon serves the next command too, found by
// what it said of itself. The control is the same directory on a
// machine told nothing of its grid, where the command says there is no
// workspace and how this machine is told.
func TestWithNoWorkspaceTheCommandLineBuildsItsDirectoryThroughADaemonItStarts(t *testing.T) {
	grid, assets := testgrid.Address(t), testgrid.Assets(t)
	root, command := builtCommand(t)

	checkout := filepath.Join(t.TempDir(), "code")
	testforge.Hello(t, checkout)
	if err := os.WriteFile(filepath.Join(checkout, "frontseat.yaml"), []byte("plugins: {go: {}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	machine := aMachine(t, command)
	kept := machine.kept
	asked := func(args ...string) (string, error) { return machine.asked(filepath.Join(checkout, "hello"), args...) }

	// The control: told nothing of its grid, nothing is started.
	if out, err := asked("build"); err == nil || !strings.Contains(out, "no workspace to talk to") || !strings.Contains(out, "build.yaml") {
		t.Fatalf("on a machine told nothing of its grid, a build: %v\n%s", err, out)
	}
	if left, _ := filepath.Glob(filepath.Join(kept, "build", "*")); len(left) != 0 {
		t.Fatalf("told nothing of its grid, the command line left %v", left)
	}

	machine.toldItsGrid(root, grid, assets, "")

	machine.does(filepath.Join(checkout, "hello"), "build")
	states, _ := filepath.Glob(filepath.Join(kept, "build", "*"))
	if len(states) != 1 {
		t.Fatalf("after one build of one directory the command line keeps %v; want one daemon's state", states)
	}
	first, err := life.Told(states[0])
	real, _ := filepath.EvalSymlinks(checkout)
	if err != nil || first.Directory != real {
		t.Fatalf("the daemon says %+v (%v); want it the daemon of the repository, %s, though the command was run beneath it", first.Directory, err, real)
	}

	// The next command asks the daemon that serves.
	listed, err := asked("build", "list")
	if err != nil || !strings.Contains(listed, "build_") {
		t.Fatalf("frontseat build list: %v\n%s", err, listed)
	}
	if again, err := life.Told(states[0]); err != nil || again != first {
		t.Errorf("after the next command the daemon says %+v (%v); want the one that served the first, %+v", again.Address, err, first.Address)
	}
}
