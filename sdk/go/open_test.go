package frontseat

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
	"google.golang.org/protobuf/proto"
)

// plane is a control plane that says what a test has it say, and keeps
// what it was asked: enough to show what opening asks of one, and what
// it never asks. Its one workspace's daemon says what the test has it
// hold.
type plane struct {
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
	// answers is how a request for a workspace is answered; nil for a
	// refusal, which refused then says.
	answers *workspacev1.CreateWorkspaceResponse
	refused error
	// holds are the remotes of what the workspace's daemon holds.
	holds []string
	// kept is what the workspace there is was made with; nil for none.
	kept *workspacev1.WorkspaceSpec

	url     string
	mu      sync.Mutex
	created []*workspacev1.CreateWorkspaceRequest
	deleted []string
}

func (p *plane) CreateWorkspace(_ context.Context, asked *workspacev1.CreateWorkspaceRequest) (*workspacev1.CreateWorkspaceResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.created = append(p.created, asked)
	if p.answers == nil {
		return nil, p.refused
	}
	if p.answers.Workspace.Status == nil {
		p.answers.Workspace.Status = &workspacev1.WorkspaceStatus{}
	}
	p.answers.Workspace.Status.BuildUri = p.url + "/workspaces/" + p.answers.GetWorkspace().GetName() + "/build"
	return p.answers, nil
}

func (p *plane) WaitWorkspace(_ context.Context, _ *workspacev1.WaitWorkspaceRequest) (*workspacev1.WaitWorkspaceResponse, error) {
	status := &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_READY, BuildUri: p.answers.GetWorkspace().GetStatus().GetBuildUri()}
	for _, remote := range p.holds {
		status.Repositories = append(status.Repositories, &workspacev1.Repository{Name: "code", Remote: remote})
	}
	return &workspacev1.WaitWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: p.answers.GetWorkspace().GetName(), Spec: p.answers.GetWorkspace().GetSpec(), Status: status}}, nil
}

func (p *plane) DeleteWorkspace(_ context.Context, asked *workspacev1.DeleteWorkspaceRequest) (*workspacev1.DeleteWorkspaceResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleted = append(p.deleted, asked.GetName())
	return &workspacev1.DeleteWorkspaceResponse{}, nil
}

func served(t *testing.T, p *plane) string {
	t.Helper()
	api := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, p)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p.url = srv.URL
	return srv.URL
}

// DescribeWorkspace says the workspace there is was made as kept says.
func (p *plane) DescribeWorkspace(_ context.Context, asked *workspacev1.DescribeWorkspaceRequest) (*workspacev1.DescribeWorkspaceResponse, error) {
	if p.kept == nil || asked.GetName() != "a-workspace" {
		return nil, connect.NewError(connect.CodeNotFound, "there is no workspace "+asked.GetName())
	}
	return &workspacev1.DescribeWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: "a-workspace", Spec: p.kept}}, nil
}

// here is the workspace of a repository at a branch, asked for with a
// key.
var here = WorkspaceSpec{Repositories: []Repository{{Remote: "https://forge.example/acme/code.git", Ref: "main"}}, Author: Author{Name: "Someone", Email: "someone@forge.example"},
	AuthorizedKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl test"}

// pushing is here, pushing or not.
func pushing(push bool) WorkspaceSpec {
	s := here
	s.Push = push
	return s
}

func there(state workspacev1.WorkspaceStatus_State, push bool) *workspacev1.Workspace {
	return &workspacev1.Workspace{Name: "a-workspace", Status: &workspacev1.WorkspaceStatus{State: state}, Spec: &workspacev1.WorkspaceSpec{Push: push}}
}

// A workspace is asked for by what it is to hold, and by no name where
// the spec gives none: the control plane names it, so that two who ask
// alike are given one. It is told who commits and whose key gets into
// its shell, and pushes only where that was asked.
func TestAWorkspaceIsAskedForByWhatItIsToHold(t *testing.T) {
	for _, push := range []bool{false, true} {
		p := &plane{refused: connect.NewError(connect.CodeInternal, "the test makes no workspace")}
		_, err := New(served(t, p)).Open(context.Background(), pushing(push))
		if err == nil || !strings.Contains(err.Error(), "the test makes no workspace") {
			t.Fatalf("opening: %v, want the control plane's own answer", err)
		}
		if len(p.created) != 1 {
			t.Fatalf("%d workspaces were asked for, want one", len(p.created))
		}
		asked := p.created[0]
		spec := asked.GetSpec()
		if asked.GetName() != "" || len(spec.GetRepositories()) != 1 || spec.GetRepositories()[0].GetRemote() != here.Repositories[0].Remote || spec.GetRepositories()[0].GetRef() != "main" {
			t.Errorf("asked for as %q of %v, want no name and the repository at its branch", asked.GetName(), spec.GetRepositories())
		}
		if spec.GetPush() != push || spec.GetAuthor().GetName() != "Someone" || spec.GetAuthor().GetEmail() != "someone@forge.example" {
			t.Errorf("asked for with push %v by %v", spec.GetPush(), spec.GetAuthor())
		}
		if key := spec.GetAuthorizedKey(); key != here.AuthorizedKey {
			t.Errorf("asked for with the key %q, want %q", key, here.AuthorizedKey)
		}
	}
}

// How a workspace came to be the one opened is what the control plane
// said of it, not what was asked: made now, there and as it was left, or
// there and filled only now. What it holds is its own record's word too.
func TestHowAWorkspaceCameToBeOpenedIsTheControlPlanesWord(t *testing.T) {
	for name, c := range map[string]struct {
		answers *workspacev1.CreateWorkspaceResponse
		how     OpenOutcome
		pushes  bool
	}{
		"made now":                         {&workspacev1.CreateWorkspaceResponse{Workspace: there(workspacev1.WorkspaceStatus_STATE_STARTING, false), Created: true}, OpenOutcomeCreated, false},
		"there, as it was left":            {&workspacev1.CreateWorkspaceResponse{Workspace: there(workspacev1.WorkspaceStatus_STATE_READY, false)}, OpenOutcomeExisting, false},
		"there and paused":                 {&workspacev1.CreateWorkspaceResponse{Workspace: there(workspacev1.WorkspaceStatus_STATE_PAUSED, false)}, OpenOutcomeExisting, false},
		"there, and holding nothing yet":   {&workspacev1.CreateWorkspaceResponse{Workspace: there(workspacev1.WorkspaceStatus_STATE_POPULATING, false)}, OpenOutcomeFilled, false},
		"there and pushing, asked plainly": {&workspacev1.CreateWorkspaceResponse{Workspace: there(workspacev1.WorkspaceStatus_STATE_READY, true)}, OpenOutcomeExisting, true},
	} {
		p := &plane{answers: c.answers, holds: []string{here.Repositories[0].Remote}}
		opened, err := New(served(t, p)).Open(context.Background(), pushing(false))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		held := opened.Status.GetStatus().GetRepositories()
		if opened.Outcome != c.how || opened.Status.GetSpec().GetPush() != c.pushes || opened.Workspace.Name() != "a-workspace" || len(held) != 1 || held[0].GetName() != "code" {
			t.Errorf("%s: opened %q as %v, pushing %v, holding %v", name, opened.Workspace.Name(), opened.Outcome, opened.Status.GetSpec().GetPush(), held)
		}
	}
}

// Opening ends nothing: a workspace that is there and was asked for
// otherwise is the control plane's to refuse, and its refusal is said as
// it was given.
func TestOpeningEndsNothing(t *testing.T) {
	refusing := &plane{refused: connect.NewError(connect.CodeAlreadyExists, "the workspace code-main is there, and it was made without push")}
	if _, err := New(served(t, refusing)).Open(context.Background(), pushing(true)); connect.CodeOf(err) != connect.CodeAlreadyExists || !strings.Contains(err.Error(), "made without push") {
		t.Errorf("a workspace that is there and asked for otherwise: %v, want the control plane's refusal", err)
	}
	if len(refusing.deleted) != 0 {
		t.Errorf("opening ended %v", refusing.deleted)
	}
}

// A spec of a name and keys alone opens the workspace there is by that
// name as it was made, whole: what it clones, whether it pushes and who
// commits there, with the keys given now. One there is none of is not
// made. The control is a spec that says more, which is asked for as it
// is.
func TestAWorkspaceOpenedByItsNameIsAskedForAsItWasMade(t *testing.T) {
	kept := &workspacev1.WorkspaceSpec{
		Repositories: []*workspacev1.RepositorySource{{Remote: "https://forge.example/acme/code.git", Ref: "main"}},
		Push:         true, Author: &workspacev1.Author{Name: "Someone", Email: "someone@forge.example"}, Image: "box:1",
	}
	p := &plane{answers: &workspacev1.CreateWorkspaceResponse{Workspace: there(workspacev1.WorkspaceStatus_STATE_READY, true)}, kept: kept}
	fs := New(served(t, p))
	if _, err := fs.Open(context.Background(), WorkspaceSpec{Name: "a-workspace", AuthorizedKey: "key"}); err != nil {
		t.Fatal(err)
	}
	want := proto.Clone(kept).(*workspacev1.WorkspaceSpec)
	want.AuthorizedKey = "key"
	if got := p.created[0]; got.GetName() != "a-workspace" || !proto.Equal(got.GetSpec(), want) {
		t.Errorf("opened by its name as %q %v, want %v", got.GetName(), got.GetSpec(), want)
	}
	if _, err := fs.Open(context.Background(), WorkspaceSpec{Name: "another"}); connect.CodeOf(err) != connect.CodeNotFound || len(p.created) != 1 {
		t.Errorf("a workspace there is none of, opened by its name: %v, asked for %d times", err, len(p.created))
	}
	if _, err := fs.Open(context.Background(), WorkspaceSpec{Name: "a-workspace", Whole: true}); err != nil {
		t.Fatal(err)
	}
	if got := p.created[1].GetSpec(); !proto.Equal(got, &workspacev1.WorkspaceSpec{Whole: true}) {
		t.Errorf("a spec that says more was asked for as %v", got)
	}
}

// An outcome says itself in a person's words, and the zero outcome is
// none of the three a workspace that was opened has.
func TestAnOpenOutcomeSaysItself(t *testing.T) {
	for outcome, want := range map[OpenOutcome]string{OpenOutcomeCreated: "made", OpenOutcomeFilled: "filled", OpenOutcomeExisting: "as it was left", OpenOutcomeUnspecified: "unspecified"} {
		if got := outcome.String(); got != want {
			t.Errorf("%d says %q, want %q", outcome, got, want)
		}
	}
}
