package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/sdk/go"
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

// endingAt serves an ending plane for as long as the test runs, and
// returns it with where it is asked.
func endingAt(t *testing.T) (endingPlane, string) {
	t.Helper()
	plane := endingPlane{asked: make(chan struct{}), gone: make(chan struct{})}
	return plane, serving(t, func(api *connect.Server) {
		workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, plane)
	})
}

// `frontseat workspace delete` returns when the workspace is gone, and
// forgets it only then: while the control plane is still ending it, the
// command has not returned and the workspace is still the one selected
// on this machine. A command that returned on the control plane's word that
// it would end it left a sandbox behind whoever stopped the control plane
// next.
func TestDeletingAWorkspaceReturnsWhenItIsGone(t *testing.T) {
	plane, at := endingAt(t)
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
		t.Fatalf("a workspace that is gone is still the one selected here: %+v, %v", held, err)
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

// noneKept is a control plane that keeps no workspace, and says so of
// whichever is asked for, by the reason where it is told to give one.
type noneKept struct {
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
	t      *testing.T
	reason string
}

func (n noneKept) DescribeWorkspace(_ context.Context, asked *workspacev1.DescribeWorkspaceRequest) (*workspacev1.DescribeWorkspaceResponse, error) {
	return nil, refusal(n.t, connect.CodeNotFound, n.reason, "there is no workspace "+asked.GetName())
}

func (n noneKept) DeleteWorkspace(_ context.Context, asked *workspacev1.DeleteWorkspaceRequest) (*workspacev1.DeleteWorkspaceResponse, error) {
	return nil, refusal(n.t, connect.CodeNotFound, n.reason, "there is no workspace "+asked.GetName())
}

// A workspace selected on this machine that its control plane keeps no
// longer is forgotten here by deleting it: the control plane answers
// that there is none, by its reason or by its code alone, with --yes or
// asked at a terminal; the command says that, exit 1 as of any workspace
// there is none of, and that the selection is forgotten. The controls
// are a workspace of another name selected, and one of the same name
// that another control plane keeps: each stays selected, and nothing is
// said of it; and a control plane that answers something else, which
// forgets nothing.
func TestDeletingASelectedWorkspaceThatIsGoneForgetsItHere(t *testing.T) {
	wasAt := atTerminal
	t.Cleanup(func() { atTerminal = wasAt })
	const forgotten = "named was the workspace selected on this machine, and is forgotten here: build, test, run, query and ship work on this machine's directory again\n"
	for name, c := range map[string]struct {
		reason   string
		terminal bool
		args     []string
	}{
		"by its reason, with --yes":    {frontseat.ReasonWorkspaceNotFound, false, []string{"named", "--yes"}},
		"by its code, with --yes":      {"", false, []string{"named", "--yes"}},
		"by its reason, at a terminal": {frontseat.ReasonWorkspaceNotFound, true, []string{"named"}},
	} {
		at := serving(t, func(api *connect.Server) {
			workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, noneKept{t: t, reason: c.reason})
		})
		serverAt(t, at)
		atTerminal = func() bool { return c.terminal }
		for selected, forgets := range map[machine.Context]bool{
			{Server: at, Workspace: "named"}:                   true,
			{Server: at, Workspace: "another"}:                 false,
			{Server: "http://127.0.0.1:1", Workspace: "named"}: false,
		} {
			lastOpened(t, selected.Server, selected.Workspace)
			printed, code, err := runAt(t, context.Background(), "", append([]string{"workspace", "delete"}, c.args...)...)
			if err == nil || code != ExitRefused || printed != "" || !strings.Contains(err.Error(), "there is no workspace named") {
				t.Errorf("%s, %+v selected: printed %q and ended %d (%v); want it said that there is none, exit %d", name, selected, printed, code, err, ExitRefused)
			}
			held, found, err := machine.ReadContext()
			if err != nil || found == forgets || (found && held != selected) {
				t.Errorf("%s, %+v selected: after it, %+v is selected: %t (%v); want it forgotten: %t", name, selected, held, found, err, forgets)
			}
			if said := complaints.String(); (said == forgotten) != forgets || (!forgets && said != "") {
				t.Errorf("%s, %+v selected: it said %q; want the selection said forgotten: %t", name, selected, said, forgets)
			}
		}
	}
	// A control plane that answers something else: nothing is known of
	// the workspace, and nothing is forgotten.
	at := serving(t, func(api *connect.Server) {
		workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler{})
	})
	serverAt(t, at)
	lastOpened(t, at, "named")
	if _, _, err := runAt(t, context.Background(), "", "workspace", "delete", "named", "--yes"); err == nil {
		t.Fatal("a control plane that deletes nothing was taken to have")
	}
	if _, found, err := machine.ReadContext(); err != nil || !found || complaints.Len() != 0 {
		t.Errorf("the control: a control plane that answered otherwise, and the selection is there: %t (%v), %q said", found, err, complaints.String())
	}
}

// A workspace that was deleted is selected no longer where it was
// selected of the control plane that deleted it, and stays selected
// where the one selected is of that name on another control plane, or of
// another name: that one is another workspace. A context that cannot be
// read then is said, the workspace being deleted all the same.
func TestDeletingAWorkspaceForgetsOnlyTheOneSelectedOfItsControlPlane(t *testing.T) {
	ended := func() string {
		plane, at := endingAt(t)
		go func() { <-plane.asked; close(plane.gone) }()
		return at
	}
	for _, c := range []struct {
		elsewhere, name string
		forgets         bool
	}{
		{"", "named", true},
		{"", "another", false},
		{"http://127.0.0.1:1", "named", false},
	} {
		at := ended()
		serverAt(t, at)
		selected := machine.Context{Server: at, Workspace: c.name}
		if c.elsewhere != "" {
			selected.Server = c.elsewhere
		}
		lastOpened(t, selected.Server, selected.Workspace)
		if printed, _, err := runAt(t, context.Background(), "", "workspace", "delete", "named", "--yes"); err != nil || printed != "deleted named\n" {
			t.Fatalf("with %+v selected: printed %q (%v), want deleted named", selected, printed, err)
		}
		if held, found, err := machine.ReadContext(); err != nil || found == c.forgets || (found && held != selected) {
			t.Errorf("with %+v selected, after named was deleted, %+v is selected: %t (%v); want it forgotten: %t", selected, held, found, err, c.forgets)
		}
	}
	at := ended()
	serverAt(t, at)
	file := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "frontseat", "context.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("in no format"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, code, err := runAt(t, context.Background(), "", "workspace", "delete", "named", "--yes"); err == nil || code != ExitUsage || !strings.Contains(err.Error(), "named was deleted") || !strings.Contains(err.Error(), "`frontseat workspace close` removes it") {
		t.Errorf("with a context that cannot be read: ended %d (%v); want it said that named was deleted, and what removes the context", code, err)
	}
}
