package frontseat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// ending is a control plane that ends a workspace as the real one does:
// asked to, it answers at once and the workspace is ending; whoever waits
// for it is told so, and is answered that there is none when it is gone,
// which is when the test says.
type ending struct {
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
	mu    sync.Mutex
	asked []string
	// waiting is how many waits for the workspace have begun.
	waiting int
	gone    chan struct{}
	// why is what keeps the workspace from going, as its status says.
	why string
	// again makes a wait end on a workspace that is ready: another's, made
	// by the name of the one that was ended.
	again bool
}

func (e *ending) DescribeWorkspace(_ context.Context, asked *workspacev1.DescribeWorkspaceRequest) (*workspacev1.DescribeWorkspaceResponse, error) {
	if err := e.known(asked.GetName()); err != nil {
		return nil, err
	}
	return &workspacev1.DescribeWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: asked.GetName(), Status: &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_ENDING, Failure: e.why}}}, nil
}

func (e *ending) known(name string) error {
	if name != "there" {
		return connect.NewError(connect.CodeNotFound, "there is no workspace "+name)
	}
	select {
	case <-e.gone:
		return connect.NewError(connect.CodeNotFound, "there is no workspace "+name)
	default:
		return nil
	}
}

func (e *ending) DeleteWorkspace(_ context.Context, asked *workspacev1.DeleteWorkspaceRequest) (*workspacev1.DeleteWorkspaceResponse, error) {
	if err := e.known(asked.GetName()); err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.asked = append(e.asked, asked.GetName())
	e.mu.Unlock()
	return &workspacev1.DeleteWorkspaceResponse{}, nil
}

func (e *ending) WaitWorkspace(ctx context.Context, asked *workspacev1.WaitWorkspaceRequest) (*workspacev1.WaitWorkspaceResponse, error) {
	if err := e.known(asked.GetName()); err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.waiting++
	e.mu.Unlock()
	if e.again {
		return &workspacev1.WaitWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: asked.GetName(), Status: &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_READY}}}, nil
	}
	select {
	case <-e.gone:
		return nil, e.known(asked.GetName())
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func endingPlane(t *testing.T) (*ending, string) {
	t.Helper()
	e := &ending{gone: make(chan struct{})}
	api := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, e)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return e, srv.URL
}

// Deleting a workspace returns when the workspace is gone, and its sandbox
// with it, not when it was asked to go: whoever stops the control plane
// next leaves nothing behind. A workspace there is none of is said to be
// none, which is the control.
func TestDeletingAWorkspaceReturnsWhenItIsGone(t *testing.T) {
	plane, server := endingPlane(t)
	if err := New(server).Workspace("another").Delete(t.Context()); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("a workspace there is none of was ended: %v", err)
	}
	ended := make(chan error, 1)
	go func() { ended <- New(server).Workspace("there").Delete(t.Context()) }()
	select {
	case err := <-ended:
		t.Fatalf("the workspace was said to be ended while it was still ending: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	plane.mu.Lock()
	asked := len(plane.asked)
	plane.mu.Unlock()
	if asked != 1 {
		t.Fatalf("the workspace was asked to end %d times", asked)
	}
	close(plane.gone)
	select {
	case err := <-ended:
		if err != nil {
			t.Fatalf("a workspace that is gone was not said to be ended: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the workspace is gone and ending it has not returned")
	}
}

// Whoever gives up waiting is told so, and the workspace goes on ending:
// it was asked to, and that is not taken back.
func TestGivingUpWaitingDoesNotTakeTheEndingBack(t *testing.T) {
	plane, server := endingPlane(t)
	plane.why = "the gateway does not answer"
	ctx, cancel := context.WithCancel(t.Context())
	ended := make(chan error, 1)
	go func() { ended <- New(server).Workspace("there").Delete(ctx) }()
	// It gives up once it is waiting: after the workspace was asked to
	// end, and the asking answered.
	deadline := time.After(10 * time.Second)
	for waiting := 0; waiting == 0; {
		select {
		case <-deadline:
			t.Fatal("the workspace was never waited for")
		case <-time.After(10 * time.Millisecond):
		}
		plane.mu.Lock()
		waiting = plane.waiting
		plane.mu.Unlock()
	}
	cancel()
	select {
	case err := <-ended:
		if !errors.Is(err, context.Canceled) && connect.CodeOf(err) != connect.CodeCanceled {
			t.Fatalf("giving up was answered %v", err)
		}
		// With what keeps it, as the control plane says it.
		if !strings.Contains(err.Error(), "still ending: the gateway does not answer") {
			t.Fatalf("giving up was not told what keeps the workspace: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("giving up did not end the wait")
	}
}

// A wait that ends on a workspace that is there is of another's, made by
// the name of the one that was ended: it is said so, and not that the
// ending failed.
func TestAWorkspaceMadeAgainByTheNameIsSaidToBeAnother(t *testing.T) {
	plane, server := endingPlane(t)
	plane.again = true
	err := New(server).Workspace("there").Delete(t.Context())
	if err == nil || !strings.Contains(err.Error(), "there again, ready") {
		t.Fatalf("a workspace made again by the name was answered %v", err)
	}
}
