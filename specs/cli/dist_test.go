package cli_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/sdk/go"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgateway"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/server/testsupport/testowner"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
	"github.com/frontseat-dev/frontseat/server/testsupport/testsandbox"
)

// ran runs a program to its end and returns what it printed; one that
// fails, fails the test with what it printed.
func ran(t *testing.T, dir string, timeout time.Duration, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestTheLocalBuildMakesAWorkspaceAndBuildsIt is the journey a person
// takes with this checkout, as CONTRIBUTING.md's "Getting started as a
// contributor" writes it: `mise run
// build` makes dist/ and the box image (frontseat-local box), mise puts
// dist/bin on the PATH here, frontseat-local serves sandboxes, and the
// frontseat just built makes a workspace of a checkout and builds it.
func TestTheLocalBuildMakesAWorkspaceAndBuildsIt(t *testing.T) {
	testrequired.Stack(t)
	testbox.Runtime(t)
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	// What the gateway knows the server by, asked for before anything is
	// built, so that the lack of a gateway is said at once.
	testgateway.Required(t)
	ran(t, root, 20*time.Minute, "mise", "run", "build")
	// Every plugin, as the modules prepared for this test name them: the
	// Cargo workspace's, which both are built from.
	plugins, err := testmodule.Plugins()
	if err != nil {
		t.Fatal(err)
	}
	made := []string{"bin/frontseat", "bin/frontseatd", "bin/frontseat-local"}
	for name := range plugins {
		made = append(made, "plugins/"+name+".wasm")
	}
	if len(made) == 3 {
		t.Fatal("no plugin was prepared for this test")
	}
	for _, made := range made {
		if _, err := os.Stat(filepath.Join(root, "dist", made)); err != nil {
			t.Errorf("mise run build did not make dist/%s: %v", made, err)
		}
	}
	// The box of this checkout, by its tag of its content: what mise run
	// build tagged frontseat-box:local may since be another checkout's,
	// and a build that changes nothing is the same image, at once.
	printed := strings.Fields(ran(t, root, 10*time.Minute, filepath.Join(root, "dist", "bin", "frontseat-local"), "box", "--plugins", filepath.Join(root, "dist", "plugins")))
	if len(printed) == 0 {
		t.Fatal("frontseat-local box printed no tag")
	}
	image := printed[len(printed)-1]
	ran(t, root, time.Minute, "docker", "image", "inspect", image)
	// mise puts what was built on the PATH of this repository.
	which := strings.TrimSpace(ran(t, root, time.Minute, "mise", "exec", "--", "sh", "-c", "command -v frontseat"))
	if want := filepath.Join(root, "dist", "bin", "frontseat"); which != want {
		t.Fatalf("in this repository frontseat is %s, want %s", which, want)
	}
	command := filepath.Join(root, "dist", "bin", "frontseat")

	// frontseat-local, on a port of its own, until the test ends.
	at, state := localServer(t, filepath.Join(root, "dist", "bin", "frontseat-local"))
	address := strings.TrimPrefix(at, "http://")
	// What a person started answers its owner alone, by the token it
	// minted in its state, which the command line is told of as the
	// stack lays it out beside the server it names, which is the one the
	// command line asks; and it serves the sandbox API to no one.
	token := filepath.Join(state, "owner", "token")
	if info, err := os.Stat(token); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("frontseat-local minted no owner's token of its own in %s: %v", token, err)
	}
	t.Setenv("XDG_CONFIG_HOME", testowner.LayOut(t, token, "http://"+address))
	if _, err := testsandbox.Client("http://"+address).ListSandboxes(context.Background(), &sandboxv1.ListSandboxesRequest{PageSize: 1000}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("frontseat-local answered a call without its owner's token: %v", err)
	}

	// A repository holding a Go module, pinned as this repository pins
	// Go, on a forge the sandbox clones it from.
	checkout := filepath.Join(t.TempDir(), "code")
	testforge.Hello(t, checkout)

	// Whatever becomes of the creation, the workspace it may have made
	// is ended: the context names it from the moment it is recorded.
	// The command returns when the workspace is gone, so nothing is left
	// for the server, which is stopped next, to leave behind: no sandbox
	// of this server's is there after it.
	t.Cleanup(func() {
		// For so long and no longer: the command waits for as long as the
		// workspace is ending, and the server is stopped after it whatever
		// becomes of that.
		ending, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		cmd := exec.CommandContext(ending, command, "workspace", "delete")
		cmd.Dir = checkout
		if out, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(out), "there is no workspace") {
			t.Errorf("frontseat workspace delete: %v\n%s", err, out)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		raw, err := os.ReadFile(token)
		if err != nil {
			t.Fatal(err)
		}
		left, err := frontseat.New("http://"+address, frontseat.WithToken(strings.TrimSpace(string(raw)))).Workspaces(ctx)
		if err != nil || len(left) != 0 {
			t.Errorf("after its workspace was deleted the server holds %d workspaces, %v", len(left), err)
			for _, each := range left {
				t.Logf("%s is %s: %q", each.GetName(), each.GetStatus().GetState(), each.GetStatus().GetFailure())
			}
		}
	})
	ran(t, checkout, 5*time.Minute, command, "workspace", "open", "--image", image, testforge.Serve(t, filepath.Dir(checkout))+"/code")
	built := ran(t, checkout, 5*time.Minute, command, "build")
	if !strings.Contains(built, "build_") {
		t.Fatalf("frontseat build printed no build:\n%s", built)
	}
	fmt.Fprint(os.Stderr, built)
}
