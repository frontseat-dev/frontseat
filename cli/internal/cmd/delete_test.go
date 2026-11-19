package cmd

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
)

// endingPlane is a control plane that ends its one workspace, "named", as
// the real one does: asked to, it answers at once; whoever waits is told
// it is ending, and that there is none when the test lets it go.
type endingPlane struct {
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
	asked, gone chan struct{}
}

func (e endingPlane) there() error {
	select {
	case <-e.gone:
		return connect.NewError(connect.CodeNotFound, "there is no workspace \"named\"")
	default:
		return nil
	}
}

func (e endingPlane) DeleteWorkspace(context.Context, *workspacev1.DeleteWorkspaceRequest) (*workspacev1.DeleteWorkspaceResponse, error) {
	if err := e.there(); err != nil {
		return nil, err
	}
	close(e.asked)
	return &workspacev1.DeleteWorkspaceResponse{}, nil
}

func (e endingPlane) WaitWorkspace(ctx context.Context, _ *workspacev1.WaitWorkspaceRequest) (*workspacev1.WaitWorkspaceResponse, error) {
	if err := e.there(); err != nil {
		return nil, err
	}
	select {
	case <-e.gone:
		return nil, e.there()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// `frontseat workspace delete` returns when the workspace is gone, and
// forgets it only then: while the control plane is still ending it, the
// command has not returned and the workspace is still the one the other
// commands ask. A command that returned on the control plane's word that
// it would end it left a sandbox behind whoever stopped the control plane
// next.
func TestDeletingAWorkspaceReturnsWhenItIsGone(t *testing.T) {
	plane := endingPlane{asked: make(chan struct{}), gone: make(chan struct{})}
	at := serving(t, func(api *connect.Server) {
		workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, plane)
	})
	serverAt(t, at)
	lastOpened(t, at, "named")
	deleted := make(chan error, 1)
	go func() {
		_, err := Execute(context.Background(), []string{"workspace", "delete"})
		deleted <- err
	}()
	select {
	case <-plane.asked:
	case <-time.After(10 * time.Second):
		t.Fatal("the control plane was never asked to end the workspace")
	}
	select {
	case err := <-deleted:
		t.Fatalf("the command returned while the workspace was still ending: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if held, found, err := machine.ReadContext(); err != nil || !found || held.Workspace != "named" {
		t.Fatalf("a workspace that is still ending was forgotten: %+v, %v, %v", held, found, err)
	}
	close(plane.gone)
	select {
	case err := <-deleted:
		if err != nil {
			t.Fatalf("a workspace that is gone was not deleted: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the workspace is gone and the command has not returned")
	}
	if held, found, err := machine.ReadContext(); err != nil || found {
		t.Fatalf("a workspace that is gone is still the one the other commands ask: %+v, %v", held, err)
	}
}
