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
// daemon, and the command says there is no workspace and how this
// machine is told. A command about a workspace starts none either way.
// The control is the same command with a workspace selected, which is
// run in that workspace's sandbox and asks no daemon of its directory
// (TestWithAWorkspaceSelectedWhatBuildsIsRunInItsSandbox).
func TestWithNoWorkspaceSelectedABuildAsksTheDaemonOfItsDirectory(t *testing.T) {
	serverAt(t, "http://127.0.0.1:1")
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
	builder = func(_ context.Context, directory string) (machine.Builder, error) {
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

	// A command about a workspace starts none.
	if _, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err == nil || !strings.Contains(err.Error(), "no workspace to talk to") || len(asked) != 1 {
		t.Errorf("a workspace command with none selected: %v, after %d daemons asked for; want it refused, and none started", err, len(asked))
	}

	// A machine told nothing of its grid.
	builder = func(context.Context, string) (machine.Builder, error) {
		return machine.Builder{}, machine.ErrNoSettings
	}
	_, _, err = runAt(t, context.Background(), "", "ship", "plan", "greet")
	if err == nil || !strings.Contains(err.Error(), "no workspace to talk to") || !strings.Contains(err.Error(), "build.yaml") {
		t.Errorf("on a machine told nothing of its grid: %v; want no workspace, and how this machine is told", err)
	}

	// A directory that is no repository's.
	t.Chdir(t.TempDir())
	builder = func(context.Context, string) (machine.Builder, error) {
		return machine.Builder{}, errors.New("a daemon was asked for")
	}
	if _, _, err := runAt(t, context.Background(), "", "ship", "plan", "greet"); err == nil || !strings.Contains(err.Error(), "frontseat.yaml") {
		t.Errorf("in a directory of no repository: %v; want it said that none is here", err)
	}
}
