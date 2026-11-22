package cmd

import (
	"context"
	"strings"
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
		_, err := Execute(context.Background(), []string{"workspace", "delete", "named", "--yes"})
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

// A workspace is deleted by its name, once someone is asked, or once
// --yes says no one is: at a terminal what it holds is shown and an
// answer other than yes deletes nothing; where no one is at one, it is
// refused without --yes. The controls are yes at a terminal, and --yes
// where no one is, each of which deletes it, and says so.
func TestAWorkspaceIsDeletedOnceSomeoneIsAskedOrTheyAreNotThere(t *testing.T) {
	wasAt, wasAnswers := atTerminal, answers
	t.Cleanup(func() { atTerminal, answers = wasAt, wasAnswers })
	for name, c := range map[string]struct {
		terminal bool
		answer   string
		args     []string
		deleted  bool
	}{
		"no one there, no --yes":   {false, "", []string{"named"}, false},
		"at a terminal, no":        {true, "n\n", []string{"named"}, false},
		"at a terminal, nothing":   {true, "\n", []string{"named"}, false},
		"at a terminal, yes":       {true, "yes\n", []string{"named"}, true},
		"no one there, with --yes": {false, "", []string{"named", "--yes"}, true},
	} {
		plane := endingPlane{asked: make(chan struct{}), gone: make(chan struct{})}
		close(plane.gone)
		plane.gone = make(chan struct{})
		at := serving(t, func(api *connect.Server) {
			workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, describedEnding{endingPlane: plane})
		})
		serverAt(t, at)
		atTerminal = func() bool { return c.terminal }
		answers = strings.NewReader(c.answer)
		go func() { <-plane.asked; close(plane.gone) }()
		printed, _, err := runAt(t, context.Background(), "", append([]string{"workspace", "delete"}, c.args...)...)
		select {
		case <-plane.asked:
			if !c.deleted {
				t.Errorf("%s: the workspace was deleted", name)
			}
			if err != nil || printed != "deleted named\n" {
				t.Errorf("%s: printed %q, %v; want deleted named", name, printed, err)
			}
		default:
			if c.deleted {
				t.Errorf("%s: the workspace was not deleted: %v", name, err)
			} else if err == nil {
				t.Errorf("%s: nothing was deleted and nothing said so", name)
			}
		}
		if c.terminal && !strings.Contains(complaints.String(), "named  ready") {
			t.Errorf("%s: what the workspace holds was not shown before asking: %q", name, complaints.String())
		}
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "delete"); err == nil {
		t.Error("delete with no name was taken")
	}
}

// describedEnding is an ending plane that describes its workspace.
type describedEnding struct{ endingPlane }

func (describedEnding) DescribeWorkspace(context.Context, *workspacev1.DescribeWorkspaceRequest) (*workspacev1.DescribeWorkspaceResponse, error) {
	return &workspacev1.DescribeWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: "named", Status: &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_READY}}}, nil
}
