package frontseat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A workspace given no name is no workspace, and not the one Here is:
// whatever is asked of it fails before a server is asked, and says a
// name was not given, not that its source is fixed. The controls are a
// workspace that is named, for which the control plane is asked, and
// the one Here is, refused as itself.
func TestAWorkspaceGivenNoNameIsAskedNothing(t *testing.T) {
	var asked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	ctx := context.Background()
	unnamed := New(server.URL).Workspace("")
	_, described := unnamed.Describe(ctx)
	_, built := unnamed.Build(ctx, BuildRequest{})
	_, shell := unnamed.Shell(ctx)
	for what, err := range map[string]error{"describing": described, "building": built, "a shell": shell, "adding": unnamed.Add(ctx, WorkspaceSpec{}), "ending": unnamed.Delete(ctx)} {
		if !errors.Is(err, ErrWorkspaceUnnamed) || errors.Is(err, ErrHere) {
			t.Errorf("%s in a workspace given no name: %v, want it said that none was given", what, err)
		}
	}
	if got := asked.Load(); got != 0 {
		t.Errorf("a workspace given no name asked a server %d times", got)
	}
	if _, err := New(server.URL).Workspace("named").Describe(ctx); err == nil || errors.Is(err, ErrWorkspaceUnnamed) || asked.Load() != 1 {
		t.Errorf("the control: a workspace that is named: %v, asking %d times, want the control plane asked once", err, asked.Load())
	}
	if _, err := New(server.URL).Here().Describe(ctx); !errors.Is(err, ErrHere) {
		t.Errorf("the control: the workspace Here is, described: %v, want ErrHere", err)
	}
}
