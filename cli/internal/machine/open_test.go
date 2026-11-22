package machine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/frontseat-dev/frontseat/cli/internal/vcs"
	"github.com/frontseat-dev/frontseat/sdk/go"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// plane is a control plane that keeps what it is asked. Where it holds
// a remote it makes a workspace of it, ready; otherwise it refuses.
type plane struct {
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
	asked chan *workspacev1.CreateWorkspaceRequest
	holds string
}

func (p *plane) CreateWorkspace(_ context.Context, asked *workspacev1.CreateWorkspaceRequest) (*workspacev1.CreateWorkspaceResponse, error) {
	p.asked <- asked
	if p.holds == "" {
		return nil, connect.NewError(connect.CodeInternal, "the test makes no workspace")
	}
	return &workspacev1.CreateWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: "code-main"}, Created: true}, nil
}

func (p *plane) WaitWorkspace(_ context.Context, _ *workspacev1.WaitWorkspaceRequest) (*workspacev1.WaitWorkspaceResponse, error) {
	return &workspacev1.WaitWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: "code-main", Status: &workspacev1.WorkspaceStatus{
		State: workspacev1.WorkspaceStatus_STATE_READY, Repositories: []*workspacev1.Repository{{Name: "code", Remote: p.holds}},
	}}}, nil
}

// serve serves a control plane for a test, with the test's own context
// file and key beside it.
func serve(t *testing.T, p *plane) *frontseat.Client {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	api := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, p)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	fs, err := frontseat.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// standing is a checkout that stands where a test has it stand, and
// says two remotes are one as its version control system would: here,
// whatever .git they end in. It keeps what it was asked.
type standing struct {
	at       vcs.Standing
	compared *[][2]string
}

func (s standing) Standing(context.Context, string) (vcs.Standing, error) { return s.at, nil }
func (s standing) Same(one, other string) bool {
	if s.compared != nil {
		*s.compared = append(*s.compared, [2]string{one, other})
	}
	return strings.TrimSuffix(one, ".git") == strings.TrimSuffix(other, ".git")
}

// The workspace of a checkout is asked for by what the checkout is of,
// by who commits there, and with this machine's key, on one line: the
// one it logs in to the workspace's shell with.
func TestACheckoutsWorkspaceIsAskedForWithThisMachinesKey(t *testing.T) {
	plane := &plane{asked: make(chan *workspacev1.CreateWorkspaceRequest, 1)}
	here := standing{at: vcs.Standing{Remote: "https://forge.example/acme/code.git", Branch: "main", Author: vcs.Author{Name: "Someone", Email: "someone@forge.example"}}}
	opened, err := OpenCheckout(context.Background(), serve(t, plane), Open{}, here)
	if err == nil || !strings.Contains(err.Error(), "the test makes no workspace") {
		t.Fatalf("opening: %v, want the control plane's own answer", err)
	}
	if opened.Standing.Remote != here.at.Remote {
		t.Errorf("opened stands at %q, want the checkout's standing", opened.Standing.Remote)
	}
	spec := (<-plane.asked).GetSpec()
	if repositories := spec.GetRepositories(); len(repositories) != 1 || repositories[0].GetRemote() != here.at.Remote || repositories[0].GetRef() != "main" {
		t.Errorf("asked for %v, want the repository at its branch", repositories)
	}
	if spec.GetAuthor().GetName() != "Someone" || spec.GetAuthor().GetEmail() != "someone@forge.example" {
		t.Errorf("asked for by %v", spec.GetAuthor())
	}
	key, err := AuthorizedKey()
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.GetAuthorizedKey(); got != key || !strings.HasPrefix(got, "ssh-ed25519 ") || strings.ContainsAny(got, "\r\n") {
		t.Errorf("asked for with the key %q, want this machine's %q, on one line", got, key)
	}
}

// Whether the workspace holds the checkout's repository is the
// checkout's version control system's word: it is asked, and a
// repository it says is the same, spelled otherwise, is the one held.
func TestTheCheckoutSaysWhetherTheWorkspaceHoldsItsRepository(t *testing.T) {
	var compared [][2]string
	held := "https://forge.example/acme/code"
	here := standing{at: vcs.Standing{Remote: held + ".git", Branch: "main"}, compared: &compared}
	opened, err := OpenCheckout(context.Background(), serve(t, &plane{asked: make(chan *workspacev1.CreateWorkspaceRequest, 1), holds: held}), Open{}, here)
	if err != nil || opened.Repository != "code" {
		t.Fatalf("opened %q: %v; want the repository the checkout says is its own", opened.Repository, err)
	}
	if len(compared) != 1 || compared[0] != [2]string{held, held + ".git"} {
		t.Errorf("the checkout was asked %v, want the held remote and its own", compared)
	}
}

// A workspace of the name that holds other source than the checkout's
// is not this one: it is refused, and named, so that a person can end
// it. Opening ends nothing. The control is the workspace that holds the
// checkout's, above.
func TestAWorkspaceThatHoldsOtherSourceIsRefusedAndNamed(t *testing.T) {
	here := standing{at: vcs.Standing{Remote: "https://forge.example/acme/code.git", Branch: "main"}}
	other := &plane{asked: make(chan *workspacev1.CreateWorkspaceRequest, 1), holds: "https://forge.example/acme/another.git"}
	opened, err := OpenCheckout(context.Background(), serve(t, other), Open{}, here)
	if err == nil || !strings.Contains(err.Error(), "other source") || opened.Workspace.Name() != "code-main" {
		t.Errorf("a workspace that holds other source: %v, want it refused and named", err)
	}
}
