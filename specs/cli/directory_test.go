package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
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
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	ran(t, root, 20*time.Minute, "mise", "run", "build")
	command := filepath.Join(root, "dist", "bin", "frontseat")

	checkout := filepath.Join(t.TempDir(), "code")
	testforge.Hello(t, checkout)
	if err := os.WriteFile(filepath.Join(checkout, "frontseat.yaml"), []byte("plugins: {go: {}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A machine of the test's own: its configuration names a control
	// plane nothing serves, and holds no token.
	config := t.TempDir()
	kept := filepath.Join(config, "frontseat")
	if err := os.MkdirAll(filepath.Join(kept, "local"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, "local", "server"), []byte("http://127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, command, args...)
		cmd.Dir = filepath.Join(checkout, "hello")
		cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+config)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// The control: told nothing of its grid, nothing is started.
	if out, err := asked("build"); err == nil || !strings.Contains(out, "no workspace to talk to") || !strings.Contains(out, "build.yaml") {
		t.Fatalf("on a machine told nothing of its grid, a build: %v\n%s", err, out)
	}
	if left, _ := filepath.Glob(filepath.Join(kept, "build", "*")); len(left) != 0 {
		t.Fatalf("told nothing of its grid, the command line left %v", left)
	}

	settings := fmt.Sprintf("version: 1\nplugins: %s\ngrid:\n  address: %s\n  assets: %s\nplatform:\n", filepath.Join(root, "dist", "plugins"), grid, assets)
	for name, value := range testgrid.Platform() {
		settings += fmt.Sprintf("  %s: %q\n", name, value)
	}
	if err := os.WriteFile(filepath.Join(kept, "build.yaml"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	// Whatever becomes of the build, the daemon it started is stopped,
	// by what it said of itself: it is this test's own.
	t.Cleanup(func() {
		states, _ := filepath.Glob(filepath.Join(kept, "build", "*"))
		for _, state := range states {
			said, err := life.Told(state)
			if err != nil {
				continue
			}
			request, err := http.NewRequest(http.MethodPost, "http://"+said.Address+life.StopPath, nil)
			if err != nil {
				t.Error(err)
				continue
			}
			request.Header.Set("Authorization", "Bearer "+said.Token)
			if answer, err := http.DefaultClient.Do(request); err == nil {
				_ = answer.Body.Close()
			}
			// It has ended once it has let go of its directory: until
			// then it still writes there, what it knows of the files it
			// built among it, and the directory is not the test's to
			// remove.
			for asked := time.Now(); life.Held(state); time.Sleep(20 * time.Millisecond) {
				if time.Since(asked) > 30*time.Second {
					t.Errorf("the build daemon asked to stop still holds %s half a minute on", state)
					break
				}
			}
		}
	})

	out, err := asked("build")
	if err != nil {
		states, _ := filepath.Glob(filepath.Join(kept, "build", "*", "daemon.log"))
		for _, each := range states {
			said, _ := os.ReadFile(each)
			t.Logf("%s:\n%s", each, said)
		}
		t.Fatalf("frontseat build, with no workspace: %v\n%s", err, out)
	}
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
