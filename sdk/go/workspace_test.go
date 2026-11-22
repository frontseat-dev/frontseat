package frontseat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// A workspace given no name is no workspace, and not the one Here is:
// whatever is asked of it fails before a server is asked, and says a
// name was not given, not that its source is fixed. The controls are a
// workspace that is named, for which the control plane is asked, and
// the one Here is, refused as itself.
func TestAWorkspaceGivenNoNameIsAskedNothing(t *testing.T) {
	server, asked := counting(t)
	ctx := context.Background()
	unnamed := client(t, server.URL).Workspace("")
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
	if _, err := client(t, server.URL).Workspace("named").Describe(ctx); err == nil || errors.Is(err, ErrWorkspaceUnnamed) || asked.Load() != 1 {
		t.Errorf("the control: a workspace that is named: %v, asking %d times, want the control plane asked once", err, asked.Load())
	}
	if _, err := client(t, server.URL).Here().Describe(ctx); !errors.Is(err, ErrHere) {
		t.Errorf("the control: the workspace Here is, described: %v, want ErrHere", err)
	}
}

// A pattern that names no entity is refused before a control plane is
// asked; the control is one that does, which is asked.
func TestAPatternThatNamesNoEntityIsRefusedBeforeAnythingIsAsked(t *testing.T) {
	server, asked := counting(t)
	fs := client(t, server.URL)
	if _, err := fs.Open(context.Background(), WorkspaceSpec{Entities: []entity.Pattern{{Kind: "component", Name: "a b"}}}); err == nil || asked.Load() != 0 {
		t.Errorf("a pattern that is none: %v, asking %d times", err, asked.Load())
	}
	if _, err := fs.Open(context.Background(), WorkspaceSpec{Entities: []entity.Pattern{{Kind: "component", Name: "gateway"}}}); err == nil || asked.Load() != 1 {
		t.Errorf("the control: a pattern: %v, asking %d times", err, asked.Load())
	}
}

// counting is a server that answers nothing, and counts what it is
// asked.
func counting(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	asked := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return server, asked
}
