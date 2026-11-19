package cmd

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// failing is a control plane that keeps one workspace, "broken", which
// failed, and was asked for with an entity by its name alone.
type failing struct {
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
}

func (failing) workspace() *workspacev1.Workspace {
	return &workspacev1.Workspace{
		Name:   "broken",
		Spec:   &workspacev1.WorkspaceSpec{Entities: []*typev1.EntityRef{{Name: "gateway"}}},
		Status: &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_FAILED, Failure: "the forge did not answer"},
	}
}

func (f failing) DescribeWorkspace(context.Context, *workspacev1.DescribeWorkspaceRequest) (*workspacev1.DescribeWorkspaceResponse, error) {
	return &workspacev1.DescribeWorkspaceResponse{Workspace: f.workspace()}, nil
}

func (f failing) ListWorkspaces(context.Context, *workspacev1.ListWorkspacesRequest) (*workspacev1.ListWorkspacesResponse, error) {
	return &workspacev1.ListWorkspacesResponse{Workspaces: []*workspacev1.Workspace{f.workspace()}}, nil
}

// A workspace that failed is described and listed with why, and an
// entity it was asked for is said as it was asked for: no namespace is
// said that nobody wrote.
func TestAWorkspaceThatFailedIsDescribedAndListedWithWhy(t *testing.T) {
	plane := serving(t, func(api *connect.Server) {
		workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, failing{})
	})
	serverAt(t, plane)
	lastOpened(t, plane, "broken")
	for _, args := range [][]string{{"workspace", "describe"}, {"workspace", "list"}} {
		got, _, err := runAt(t, context.Background(), "", args...)
		if err != nil || !strings.Contains(got, "failed") || !strings.Contains(got, "the forge did not answer") {
			t.Errorf("%v printed %q (%v), want the workspace failed and why", args, got, err)
		}
	}
	got, _, err := runAt(t, context.Background(), "", "workspace", "list")
	if err != nil || !strings.Contains(got, "component:gateway") || strings.Contains(got, "default") {
		t.Errorf("listed as %q (%v), want component:gateway as it was asked for", got, err)
	}
}

// A shell is opened in the workspace named, and an editor told to open
// one is told it by its name: the workspace opened last is followed only
// where none is named. A name that is none is refused.
func TestASSHConnectionIsToTheWorkspaceNamed(t *testing.T) {
	options, err := sshOptions("/usr/local/bin/frontseat", "/home/a/.ssh/id", "payments")
	if err != nil {
		t.Fatal(err)
	}
	if proxy := optionOf(options, "ProxyCommand"); proxy != `"/usr/local/bin/frontseat" workspace ssh --stdio --workspace=payments` {
		t.Errorf("the SSH client goes by %s, want the workspace named", proxy)
	}
	options, err = sshOptions("/usr/local/bin/frontseat", "/home/a/.ssh/id", "")
	if err != nil {
		t.Fatal(err)
	}
	if proxy := optionOf(options, "ProxyCommand"); proxy != `"/usr/local/bin/frontseat" workspace ssh --stdio` {
		t.Errorf("with no workspace named, the SSH client goes by %s, want the one last opened", proxy)
	}
	if _, err := sshOptions("/usr/local/bin/frontseat", "/home/a/.ssh/id", "a b;c"); err == nil {
		t.Error("a workspace's name that is none was put to the SSH client")
	}
	if sub, _, err := rootCmd.Find([]string{"workspace", "ssh"}); err != nil || sub.Flags().Lookup("workspace") == nil {
		t.Error("workspace ssh takes no --workspace")
	}
}

func optionOf(options [][2]string, name string) string {
	for _, option := range options {
		if option[0] == name {
			return option[1]
		}
	}
	return ""
}

// The checkout a workspace is opened of is given with nothing else
// named: a directory given beside a name, a repository or an entity is
// refused, never dropped.
func TestADirectoryIsNotGivenBesideWhatIsNamed(t *testing.T) {
	serverAt(t, "http://127.0.0.1:1")
	for _, named := range []string{"services-main", "https://example.com/a.git", "component:payments"} {
		_, _, err := runAt(t, context.Background(), "", "workspace", "open", named, "--directory", t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "--directory") {
			t.Errorf("%s with --directory: %v, want it refused by the flag's name", named, err)
		}
	}
}
