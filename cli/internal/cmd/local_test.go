package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
)

// With no workspace selected, a command that builds asks the build
// daemon of the directory it is run in, which the command line starts
// for itself, presenting the daemon's token: no control plane is asked.
// The directory is the repository's, from wherever beneath it the
// command is run. A machine told nothing of its grid has no such
// daemon, and the command says so and how this machine is told. A
// command about a workspace starts none either way. That no control
// plane is asked is counted by one that answers, which the control, a
// workspace command, does ask.
// The control is the same command outside a workspace that is open,
// which is refused and asks for no daemon of its directory
// (TestAWorkspaceThatIsOpenIsBuiltFromInsideIt).
func TestWithNoWorkspaceSelectedABuildAsksTheDaemonOfItsDirectory(t *testing.T) {
	// A control plane that answers, and counts whatever it is asked.
	plane, planeAsked := counted(t)
	serverAt(t, plane)
	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, "app", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "frontseat.yaml"), []byte("plugins: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(repository, "app", "deep"))

	d := &daemonOf{}
	api := connect.NewServer()
	buildv1alpha1connect.RegisterReleaseServiceHandler(api, d)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	presented := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented = r.Header.Get("Authorization")
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	var asked []string
	before := builder
	t.Cleanup(func() { builder = before })
	builder = func(_ context.Context, directory string, _ bool) (machine.Builder, error) {
		asked = append(asked, directory)
		return machine.Builder{Address: strings.TrimPrefix(srv.URL, "http://"), Token: "the daemon's token"}, nil
	}

	if _, code, err := runAt(t, context.Background(), "", "ship", "plan", "greet"); err != nil || code != 0 || len(d.planned) != 1 {
		t.Fatalf("with no workspace selected, a plan ended %d (%v), and the daemon was asked %d times", code, err, len(d.planned))
	}
	real, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != real || presented != "Bearer the daemon's token" {
		t.Errorf("the daemon asked for was of %q, presented %q; want the repository's, %s, and its token", asked, presented, real)
	}
	if planeAsked.Load() != 0 {
		t.Errorf("for a plan of this machine's directory, the control plane was asked %d times; want it asked nothing", planeAsked.Load())
	}

	// A command about a workspace starts none, and the control: one that
	// names a workspace asks the control plane, which counts it.
	if _, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err == nil || !strings.Contains(err.Error(), "no workspace to talk to") || len(asked) != 1 {
		t.Errorf("a workspace command with none selected: %v, after %d daemons asked for; want it refused, and none started", err, len(asked))
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "describe", "named"); err != nil || planeAsked.Load() != 1 || len(asked) != 1 {
		t.Errorf("the control: workspace describe named asked the control plane %d times, after %d daemons asked for (%v)", planeAsked.Load(), len(asked), err)
	}

	// A machine told nothing of its grid.
	builder = func(context.Context, string, bool) (machine.Builder, error) {
		return machine.Builder{}, machine.ErrNoSettings
	}
	_, _, err = runAt(t, context.Background(), "", "ship", "plan", "greet")
	if err == nil || !strings.Contains(err.Error(), "no workspace to talk to, and this machine was told no grid to build "+real+" on") || !strings.Contains(err.Error(), filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "frontseat", "build.yaml")) {
		t.Errorf("on a machine told nothing of its grid: %v; want it said of the repository, and how this machine is told", err)
	}

	// A directory that is no repository's.
	t.Chdir(t.TempDir())
	builder = func(context.Context, string, bool) (machine.Builder, error) {
		return machine.Builder{}, errors.New("a daemon was asked for")
	}
	if _, _, err := runAt(t, context.Background(), "", "ship", "plan", "greet"); err == nil || !strings.Contains(err.Error(), "frontseat.yaml") {
		t.Errorf("in a directory of no repository: %v; want it said that none is here", err)
	}
	if planeAsked.Load() != 1 {
		t.Errorf("the control plane was asked %d times in all; want once, by workspace describe named", planeAsked.Load())
	}
}
