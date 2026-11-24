package cmd

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/cli/internal/vcs"
	"github.com/frontseat-dev/frontseat/sdk/go"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// standingAt is a checkout that stands where a test has it stand, and
// names a repository by the last element of its remote.
type standingAt vcs.Standing

func (s standingAt) Standing(context.Context, string) (vcs.Standing, error) {
	return vcs.Standing(s), nil
}
func (standingAt) Same(one, other string) bool { return one == other }
func (standingAt) NameOf(remote string) string {
	return strings.TrimSuffix(remote[strings.LastIndex(remote, "/")+1:], ".git")
}

// holding is a control plane that serves remote workspaces: it gives the
// workspace it is asked for, holding what was asked of the store on the
// task named or one named for it, and keeps what it was asked. Where it
// serves none, it refuses one as an open control plane does, by name.
type holding struct {
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
	t       *testing.T
	serves  bool
	created []*workspacev1.CreateWorkspaceRequest
}

func (h *holding) CreateWorkspace(_ context.Context, req *workspacev1.CreateWorkspaceRequest) (*workspacev1.CreateWorkspaceResponse, error) {
	h.created = append(h.created, req)
	spec := req.GetSpec()
	if !h.serves && (len(spec.GetStored()) > 0 || spec.GetTask() != "" || spec.GetLand()) {
		return nil, refusal(h.t, connect.CodeUnimplemented, frontseat.ReasonStoreNotServed, "this server serves no remote workspace")
	}
	return &workspacev1.CreateWorkspaceResponse{Created: true, Workspace: h.workspace()}, nil
}

func (h *holding) WaitWorkspace(context.Context, *workspacev1.WaitWorkspaceRequest) (*workspacev1.WaitWorkspaceResponse, error) {
	return &workspacev1.WaitWorkspaceResponse{Workspace: h.workspace()}, nil
}

// workspace is the workspace last asked for, ready, holding what it was
// asked for.
func (h *holding) workspace() *workspacev1.Workspace {
	spec := h.created[len(h.created)-1].GetSpec()
	status := &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_READY}
	for _, r := range spec.GetStored() {
		status.Repositories = append(status.Repositories, &workspacev1.Repository{Name: r.GetRepository(), Stored: r.GetRepository(), Commit: "changeset-1"})
	}
	for _, r := range spec.GetRepositories() {
		status.Repositories = append(status.Repositories, &workspacev1.Repository{Name: "cloned", Remote: r.GetRemote()})
	}
	kept := &workspacev1.WorkspaceSpec{Stored: spec.GetStored(), Task: spec.GetTask(), Land: spec.GetLand()}
	if kept.GetLand() && kept.GetTask() == "" {
		kept.Task = "services"
	}
	return &workspacev1.Workspace{Name: "services", Spec: kept, Status: status}
}

// remotelyServed serves a control plane to the test's commands, which
// read a checkout of a repository at a branch, with something not
// pushed.
func remotelyServed(t *testing.T, serves bool) *holding {
	t.Helper()
	plane := &holding{t: t, serves: serves}
	serverAt(t, serving(t, func(api *connect.Server) { workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, plane) }))
	was := checkout
	t.Cleanup(func() { checkout = was })
	checkout = func() vcs.Checkout {
		return standingAt{Remote: "https://forge.example/acme/services.git", Branch: "feature", Unpushed: 2}
	}
	return plane
}

// workspace open --remote asks for this checkout's workspace remote: its
// repository held from the store, by its name, on the task --task names
// or, with none, one the control plane names for the workspace, landing,
// with nothing to clone and the checkout's branch not asked for; and
// says so. The control is the same command without --remote, which asks
// for a local workspace: the repository cloned, at its branch, and
// nothing of the store.
func TestOpenRemoteAsksForTheCheckoutsRepositoryFromTheStoreOnATask(t *testing.T) {
	plane := remotelyServed(t, true)
	printed, _, err := runAt(t, context.Background(), "", "workspace", "open", "--remote", "--task", "fix-greeting", "--no-shell")
	if err != nil || len(plane.created) != 1 {
		t.Fatalf("open --remote: %v, asking for %d workspaces", err, len(plane.created))
	}
	spec := plane.created[0].GetSpec()
	if stored := spec.GetStored(); len(stored) != 1 || stored[0].GetRepository() != "services" || stored[0].GetAt() != "" || spec.GetTask() != "fix-greeting" || !spec.GetLand() || len(spec.GetRepositories()) != 0 {
		t.Errorf("open --remote asked for %v, want the repository services of the store on the task fix-greeting, landing, and nothing cloned", spec)
	}
	if !strings.Contains(printed, "services  held from the store, on the task fix-greeting\n") || !strings.Contains(printed, "not its branch, feature") || strings.Contains(printed, "not pushed:") || !strings.HasSuffix(printed, "services  made\n") {
		t.Errorf("open --remote printed %q, want what it holds, and its task", printed)
	}
	printed, _, err = runAt(t, context.Background(), "", "workspace", "open", "--remote", "--no-shell")
	if spec := plane.created[1].GetSpec(); err != nil || spec.GetTask() != "" || !spec.GetLand() || !strings.Contains(printed, "on the task services\n") {
		t.Errorf("open --remote with no task named: %v, asked for the task %q, printing %q; want none named, and the one the control plane named said", err, spec.GetTask(), printed)
	}
	printed, _, err = runAt(t, context.Background(), "", "workspace", "open", "--no-shell")
	local := plane.created[2].GetSpec()
	if err != nil || len(local.GetStored()) != 0 || local.GetTask() != "" || local.GetLand() || len(local.GetRepositories()) != 1 || local.GetRepositories()[0].GetRef() != "feature" {
		t.Errorf("the control, open without --remote: %v, asked for %v; want the repository cloned at its branch and nothing of the store", err, local)
	}
	if !strings.Contains(printed, "https://forge.example/acme/services.git  feature\n") || !strings.Contains(printed, "2 commits here are not pushed") || strings.Contains(printed, "store") {
		t.Errorf("the control printed %q", printed)
	}
}

// workspace create --remote asks for each repository named from the
// store, by its name, and for the entities as they are named, on the
// task, landing, with nothing to clone. The control is the same command
// without --remote.
func TestCreateRemoteAsksForWhatIsNamedFromTheStore(t *testing.T) {
	plane := remotelyServed(t, true)
	named := []string{"workspace", "create", "--repository", "https://forge.example/acme/services.git", "--repository", "https://forge.example/acme/platform", "--entity", "component:payments", "--no-shell"}
	if _, _, err := runAt(t, context.Background(), "", append(named, "--remote", "--task", "fix-refunds")...); err != nil {
		t.Fatal(err)
	}
	spec := plane.created[0].GetSpec()
	if stored := spec.GetStored(); len(stored) != 2 || stored[0].GetRepository() != "services" || stored[1].GetRepository() != "platform" || len(spec.GetRepositories()) != 0 {
		t.Errorf("create --remote asked for the stored repositories %v and to clone %v", spec.GetStored(), spec.GetRepositories())
	}
	if entities := spec.GetEntities(); len(entities) != 1 || entities[0].GetName() != "payments" || spec.GetTask() != "fix-refunds" || !spec.GetLand() {
		t.Errorf("create --remote asked for the entities %v on the task %q, landing %v", spec.GetEntities(), spec.GetTask(), spec.GetLand())
	}
	if _, _, err := runAt(t, context.Background(), "", named...); err != nil {
		t.Fatal(err)
	}
	if local := plane.created[1].GetSpec(); len(local.GetStored()) != 0 || local.GetTask() != "" || local.GetLand() || len(local.GetRepositories()) != 2 {
		t.Errorf("the control, create without --remote, asked for %v", local)
	}
}

// A control plane that serves no remote workspace refuses one by name:
// the command ends as one the server refused, and says the refusal, its
// reason, and that remote workspaces are Frontseat Enterprise's, and
// what a workspace is without --remote. The workspace is not selected
// on this machine. The control is the same command without --remote,
// which that control plane serves.
func TestARemoteWorkspaceRefusedByAControlPlaneThatServesNoneIsSaidByName(t *testing.T) {
	plane := remotelyServed(t, false)
	for _, command := range [][]string{
		{"workspace", "open", "--remote", "--no-shell"},
		{"workspace", "create", "--repository", "https://forge.example/acme/services.git", "--remote", "--no-shell"},
		{"workspace", "create", "--entity", "payments", "--remote", "--task", "fix", "--no-shell"},
	} {
		_, code, err := runAt(t, context.Background(), "", command...)
		ended, said := Failed(context.Background(), err)
		if err == nil || code != ExitRefused || ended != ExitRefused {
			t.Fatalf("%v, refused: %v, ending %d and %d, want it refused", command, err, code, ended)
		}
		for _, want := range []string{"this server serves no remote workspace", "this control plane does not serve it [STORE_NOT_SERVED]", "remote workspaces are served by Frontseat Enterprise", "without --remote the workspace is local"} {
			if !strings.Contains(said, want) {
				t.Errorf("%v, refused, said %q, want it to say %q", command, said, want)
			}
		}
	}
	if len(plane.created) != 3 {
		t.Errorf("the control plane was asked for %d workspaces, want each command to ask it once", len(plane.created))
	}
	if selected, _, _ := runAt(t, context.Background(), "", "workspace", "describe"); selected != "" {
		t.Errorf("a refused workspace is selected: describe printed %q", selected)
	}
	if printed, _, err := runAt(t, context.Background(), "", "workspace", "open", "--no-shell"); err != nil || !strings.HasSuffix(printed, "services  made\n") {
		t.Errorf("the control, a local workspace of the same control plane: %q, %v", printed, err)
	}
}

// What is said of a remote workspace is said with --remote, and what is
// said of a local one without it: a task with no --remote, a push with
// it, a repository at a branch with it, and either beside the name of a
// workspace there is, are each refused by the flag's name before any
// control plane is asked. The controls are the commands above.
func TestRemoteAndLocalFlagsAreNotMixed(t *testing.T) {
	plane := remotelyServed(t, true)
	for says, command := range map[string][]string{
		"--task":   {"workspace", "open", "--task", "fix"},
		"--push":   {"workspace", "open", "--remote", "--push"},
		"#REF":     {"workspace", "create", "--remote", "--repository", "https://forge.example/acme/services.git#main"},
		"--remote": {"workspace", "open", "services-main", "--remote"},
	} {
		_, code, err := runAt(t, context.Background(), "", command...)
		if err == nil || code != ExitUsage || !strings.Contains(err.Error(), says) {
			t.Errorf("%v: %v, ending %d; want it refused as written wrongly, naming %s", command, err, code, says)
		}
	}
	for says, command := range map[string][]string{
		"--task": {"workspace", "create", "--entity", "payments", "--task", "fix"},
		"--push": {"workspace", "create", "--entity", "payments", "--remote", "--push"},
	} {
		if _, _, err := runAt(t, context.Background(), "", command...); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%v: %v, want it refused, naming %s", command, err, says)
		}
	}
	if len(plane.created) != 0 {
		t.Errorf("what was refused asked the control plane for %d workspaces", len(plane.created))
	}
}
