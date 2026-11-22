package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

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
// adding to it, describing it, listing, opening, ending, a shell, a
// credential, is refused by name before anything is asked, and the
// control plane is told nothing. The control is the same `workspace
// add` run outside, which the control plane is told; and, inside, a
// release planned, which the daemon there is asked.
func TestInsideAWorkspaceItsSourceIsFixed(t *testing.T) {
	plane := &told{keeping: keeping{daemon: func() string { return "" }}}
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
		{"workspace", "credential", "forge.example"},
	} {
		if _, _, err := run(t, d, args...); err == nil || !strings.Contains(err.Error(), "its source is fixed") {
			t.Errorf("inside, %v: %v; want it refused by name", args, err)
		}
	}
	if asked != 0 || plane.changes() != 0 {
		t.Fatalf("inside, the control plane was asked %d times, and told %d changes", asked, plane.changes())
	}
	// Inside, the daemon there is asked what it builds and releases.
	if _, code, err := run(t, d, "ship", "--dry-run", "greet"); err != nil || code != 0 || len(d.planned) != 1 {
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
