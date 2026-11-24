package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// told is a control plane that keeps one workspace, "named", and counts
// what it is told to change of it.
type told struct {
	keeping
	mu      sync.Mutex
	updated []*workspacev1.WorkspaceSpec
}

func (k *told) UpdateWorkspace(_ context.Context, req *workspacev1.UpdateWorkspaceRequest) (*workspacev1.UpdateWorkspaceResponse, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.updated = append(k.updated, req.GetSpec())
	return &workspacev1.UpdateWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: "named", Spec: req.GetSpec()}}, nil
}

func (k *told) WaitWorkspace(context.Context, *workspacev1.WaitWorkspaceRequest) (*workspacev1.WaitWorkspaceResponse, error) {
	return &workspacev1.WaitWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: "named", Status: &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_READY}}}, nil
}

func (k *told) changes() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.updated)
}

// Inside a workspace's sandbox its source is fixed: a workspace command,
// adding to it, describing it, listing, opening, closing, ending, a
// shell, a port carried to it, is refused by name before anything is
// asked or listened on, and the control plane is told nothing; so is a
// credential command, as what it is: no sandbox holds a credential. The
// control is the same `workspace add` run outside, which the control
// plane is told; and, inside, a release planned, which the daemon there
// is asked.
func TestInsideAWorkspaceItsSourceIsFixed(t *testing.T) {
	plane := &told{keeping: keeping{}}
	mux := http.NewServeMux()
	api := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, plane)
	connecthttp.Mount(mux, api)
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	serverAt(t, srv.URL)
	lastOpened(t, srv.URL, "named")
	add := []string{"workspace", "add", "--repository", "https://forge.example/acme/more.git"}

	d := &daemonOf{}
	for _, args := range [][]string{
		add,
		{"workspace", "describe"},
		{"workspace", "list"},
		{"workspace", "open", "https://forge.example/acme/code.git"},
		{"workspace", "delete", "named"},
		{"workspace", "ssh", "true"},
		{"workspace", "forward", "8080", "--listen", "127.0.0.1:0"},
		{"workspace", "close"},
	} {
		if _, _, err := run(t, d, args...); err == nil || !strings.Contains(err.Error(), "its source is fixed") {
			t.Errorf("inside, %v: %v; want it refused by name", args, err)
		}
	}
	// A credential is no workspace's source: it is refused as what it
	// is, kept outside every sandbox, before a secret is read.
	secretFrom = strings.NewReader("a-made-up-secret\n")
	t.Cleanup(func() { secretFrom = nil })
	for _, args := range [][]string{
		{"credential", "set", "forge.example"},
		{"credential", "list"},
		{"credential", "delete", "forge.example"},
	} {
		if _, _, err := run(t, d, args...); err == nil || !strings.Contains(err.Error(), "inside a workspace's sandbox no credential is given, listed or let go of") || strings.Contains(err.Error(), "source is fixed") {
			t.Errorf("inside, %v: %v; want it refused as a credential, which no sandbox holds", args, err)
		}
	}
	if left, _ := io.ReadAll(secretFrom); string(left) != "a-made-up-secret\n" {
		t.Errorf("inside, a secret was read: %q is left of it", left)
	}
	if asked != 0 || plane.changes() != 0 {
		t.Fatalf("inside, the control plane was asked %d times, and told %d changes", asked, plane.changes())
	}
	// What is selected outside is no business of a sandbox's: closing is
	// refused there, and forgets nothing.
	if held, found, err := machine.ReadContext(); err != nil || !found || held.Workspace != "named" {
		t.Errorf("inside, workspace close forgot the selection: %+v, %v, %v", held, found, err)
	}
	// Inside, the build daemon there is asked what it builds and releases.
	if _, code, err := run(t, d, "ship", "plan", "greet"); err != nil || code != 0 || len(d.planned) != 1 {
		t.Errorf("inside, a plan ended %d (%v), and the daemon was asked %d times", code, err, len(d.planned))
	}
	// Outside, the same command tells the control plane.
	if _, _, err := runAt(t, context.Background(), "", add...); err != nil {
		t.Fatalf("outside, %v: %v", add, err)
	}
	if plane.changes() != 1 {
		t.Errorf("outside, the control plane was told %d changes, want the one added", plane.changes())
	}
}

// Inside a workspace's sandbox, a command that builds asks the build
// daemon of the workspace's directory, as it asks the one of its
// directory outside: one the command line starts there for itself, told
// that it is inside, and no daemon of the sandbox's at a port of its
// own. A workspace that was told nothing to hold has no directory to
// build, and a sandbox told no grid no settings: each is said as what it
// is, of the workspace, and never as a machine with no workspace open.
// The control is the same command where the workspace holds something
// and was told its grid, which asks the daemon
// (TestInsideAWorkspaceItsSourceIsFixed).
func TestInsideAWorkspaceABuildAsksTheBuildDaemonOfItsDirectory(t *testing.T) {
	d := &daemonOf{}
	if _, code, err := run(t, d, "ship", "plan", "greet"); err != nil || code != 0 || len(d.planned) != 1 {
		t.Fatalf("the control: inside, a plan ended %d (%v), and the build daemon was asked %d times", code, err, len(d.planned))
	}

	found, stood := builder, directory
	inside = func() bool { return true }
	t.Cleanup(func() { inside, builder, directory = outside, found, stood })
	started := 0
	builder = func(context.Context, string, bool) (machine.Builder, error) {
		started++
		return machine.Builder{}, machine.ErrNoSettings
	}
	// A workspace that holds nothing yet: the real reading of where the
	// command stands, in a directory that is no workspace's.
	t.Chdir(t.TempDir())
	_, err := Execute(context.Background(), []string{"build"})
	if err == nil || !strings.Contains(err.Error(), "nothing to build here") || !strings.Contains(err.Error(), "this workspace's") || strings.Contains(err.Error(), "frontseat.yaml") || started != 0 {
		t.Errorf("inside, in a directory that is no workspace's: %v, after %d daemons asked for; want it said that the workspace holds nothing here, and none started", err, started)
	}
	// A sandbox told no grid.
	directory = func(bool) (string, error) { return besideDirectory, nil }
	_, err = Execute(context.Background(), []string{"build"})
	if err == nil || !strings.Contains(err.Error(), "sandbox was told no grid") || errors.Is(err, errNoWorkspace) || strings.Contains(err.Error(), "workspace open") || started != 1 {
		t.Errorf("inside a sandbox told no grid: %v, after %d daemons asked for; want it said of the sandbox, and no workspace to open", err, started)
	}
}
