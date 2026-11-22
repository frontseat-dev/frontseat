package cmd

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1/credentialv1alpha1connect"
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
	if proxy := optionOf(options, "ProxyCommand"); proxy != `"/usr/local/bin/frontseat" workspace ssh --stdio payments` {
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
	// The subject of a workspace command is named as its argument, and
	// by no flag.
	for _, path := range [][]string{{"workspace", "ssh"}, {"workspace", "describe"}, {"workspace", "add"}, {"workspace", "delete"}, {"workspace", "open"}} {
		if sub, _, err := rootCmd.Find(path); err != nil || sub.Flags().Lookup("workspace") != nil {
			t.Errorf("%v takes --workspace", path)
		}
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
// named: a directory, or what is said of a workspace when it is made,
// given beside a workspace's name is refused, never dropped. Open names
// a workspace there is, or none: a repository or an entity is told to
// workspace create.
func TestADirectoryIsNotGivenBesideWhatIsNamed(t *testing.T) {
	serverAt(t, "http://127.0.0.1:1")
	for _, flag := range [][]string{{"--directory", t.TempDir()}, {"--name", "x"}, {"--push"}, {"--image", "box"}} {
		_, _, err := runAt(t, context.Background(), "", append([]string{"workspace", "open", "services-main"}, flag...)...)
		if err == nil || !strings.Contains(err.Error(), flag[0]) {
			t.Errorf("a name with %s: %v, want it refused by the flag's name", flag[0], err)
		}
	}
	for _, named := range []string{"https://example.com/a.git", "component:payments"} {
		_, _, err := runAt(t, context.Background(), "", "workspace", "open", named)
		if err == nil || !strings.Contains(err.Error(), "workspace create") {
			t.Errorf("open %s: %v, want it told to workspace create", named, err)
		}
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "open", "a", "b"); err == nil {
		t.Error("open of two names was taken")
	}
}

// making is a control plane that gives whatever workspace it is asked
// for, as answers says it came to be, and keeps what it was asked.
type making struct {
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
	answers func() *workspacev1.CreateWorkspaceResponse
	created []*workspacev1.CreateWorkspaceRequest
}

func (m *making) CreateWorkspace(_ context.Context, req *workspacev1.CreateWorkspaceRequest) (*workspacev1.CreateWorkspaceResponse, error) {
	m.created = append(m.created, req)
	return m.answers(), nil
}

func (m *making) WaitWorkspace(_ context.Context, req *workspacev1.WaitWorkspaceRequest) (*workspacev1.WaitWorkspaceResponse, error) {
	return &workspacev1.WaitWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: req.GetName(), Status: &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_READY}}}, nil
}

// came is a control plane's answer for a workspace in a state, made now
// or there already.
func came(created bool, state workspacev1.WorkspaceStatus_State) func() *workspacev1.CreateWorkspaceResponse {
	return func() *workspacev1.CreateWorkspaceResponse {
		return &workspacev1.CreateWorkspaceResponse{Created: created, Workspace: &workspacev1.Workspace{Name: "payments", Status: &workspacev1.WorkspaceStatus{State: state}}}
	}
}

// A workspace made of what is named says how it came to be the one
// opened, as one opened of a checkout does: made, there and filled now,
// or as it was left.
func TestAWorkspaceOpenedOfWhatIsNamedSaysHowItCame(t *testing.T) {
	plane := &making{}
	serverAt(t, serving(t, func(api *connect.Server) { workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, plane) }))
	for want, answers := range map[string]func() *workspacev1.CreateWorkspaceResponse{
		"payments  made\n":           came(true, workspacev1.WorkspaceStatus_STATE_POPULATING),
		"payments  filled\n":         came(false, workspacev1.WorkspaceStatus_STATE_POPULATING),
		"payments  as it was left\n": came(false, workspacev1.WorkspaceStatus_STATE_READY),
	} {
		plane.answers = answers
		if got, _, err := runAt(t, context.Background(), "", "workspace", "create", "--entity", "component:payments"); err != nil || got != want {
			t.Errorf("opened, it printed %q (%v), want %q", got, err, want)
		}
	}
}

// --whole is said of the entities a workspace is made of: with
// repositories alone it would change nothing, and is refused before
// anything is asked, never dropped; workspace open, of a checkout's
// repository, takes none. A repository is named by its address over
// http or https, and nothing named is refused. The control is an entity,
// whose workspace is asked for whole.
func TestWholeIsSaidOfEntitiesAlone(t *testing.T) {
	plane := &making{answers: came(true, workspacev1.WorkspaceStatus_STATE_POPULATING)}
	serverAt(t, serving(t, func(api *connect.Server) { workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, plane) }))
	for _, args := range [][]string{
		{"workspace", "create", "--whole", "--repository", "https://example.com/a.git"},
		{"workspace", "create", "--whole"},
		{"workspace", "create"},
		{"workspace", "create", "--repository", "git@example.com:acme/a.git"},
		{"workspace", "open", "--whole"},
	} {
		if _, _, err := runAt(t, context.Background(), "", args...); err == nil {
			t.Errorf("%v was taken", args)
		}
	}
	if len(plane.created) != 0 {
		t.Errorf("what was refused asked the control plane for %d workspaces", len(plane.created))
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "create", "--whole", "--entity", "component:payments"); err != nil || len(plane.created) != 1 || !plane.created[0].GetSpec().GetWhole() {
		t.Errorf("the control: --whole with an entity: %v, asked %v", err, plane.created)
	}
}

// --whole is said of the entities a workspace is given more of: with
// repositories alone it is refused before anything is asked, never
// dropped and never said of what the workspace held already. The
// control is an entity added, which the workspace is told whole.
func TestWholeIsSaidOfTheEntitiesAddedAlone(t *testing.T) {
	plane := &told{keeping: keeping{daemon: func() string { return "" }}}
	at := serving(t, func(api *connect.Server) { workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, plane) })
	serverAt(t, at)
	lastOpened(t, at, "named")
	_, _, err := runAt(t, context.Background(), "", "workspace", "add", "--whole", "--repository", "https://forge.example/acme/more.git")
	if err == nil || !strings.Contains(err.Error(), "--whole") {
		t.Errorf("--whole with a repository alone: %v, want it refused by the flag's name", err)
	}
	if plane.changes() != 0 {
		t.Errorf("what was refused told the control plane %d changes", plane.changes())
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "add", "--whole", "--entity", "component:payments"); err != nil || plane.changes() != 1 || !plane.updated[0].GetWhole() {
		t.Errorf("the control: --whole with an entity: %v, told %v", err, plane.updated)
	}
}

// Flags that say two things at once are refused, and neither is done:
// an SSH client's connection carried and its host printed, a host
// printed and a command run. The control is --config alone.
func TestFlagsThatDoNotGoTogetherAreRefused(t *testing.T) {
	k := &keeper{}
	serverAt(t, serving(t, func(api *connect.Server) { credentialv1alpha1connect.RegisterCredentialServiceHandler(api, k) }))
	for _, command := range [][]string{
		{"workspace", "ssh", "--stdio", "--config"},
		{"workspace", "ssh", "--config", "--", "ls"},
		{"workspace", "ssh", "--stdio", "--", "ls"},
	} {
		printed, _, err := runAt(t, context.Background(), "", command...)
		if err == nil || printed != "" {
			t.Errorf("frontseat %s: %v, printing %q, want it refused", strings.Join(command, " "), err, printed)
		}
	}
	if len(k.forgot)+len(k.given) != 0 {
		t.Errorf("what was refused had the control plane forget %v and keep %v", k.forgot, k.given)
	}
	if printed, _, err := runAt(t, context.Background(), "", "workspace", "ssh", "--config"); err != nil || !strings.HasPrefix(printed, "Host frontseat-workspace\n") {
		t.Errorf("the control: --config alone printed %q (%v)", printed, err)
	}
}

// A workspace made at a terminal is gone on into, unless --no-shell says
// it is not: with it, the command returns once the workspace is made,
// and runs no SSH client.
func TestAWorkspaceIsNotGoneIntoWithNoShell(t *testing.T) {
	wasAt := atTerminal
	t.Cleanup(func() { atTerminal = wasAt })
	atTerminal = func() bool { return true }
	plane := &making{answers: came(true, workspacev1.WorkspaceStatus_STATE_POPULATING)}
	serverAt(t, serving(t, func(api *connect.Server) { workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, plane) }))
	t.Setenv("PATH", t.TempDir())
	if printed, _, err := runAt(t, context.Background(), "", "workspace", "create", "--entity", "payments", "--no-shell"); err != nil || printed != "payments  made\n" {
		t.Errorf("made with --no-shell at a terminal: %q, %v", printed, err)
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "create", "--entity", "payments"); err == nil || !strings.Contains(err.Error(), "no SSH client") {
		t.Errorf("the control: made at a terminal, it went on into no shell: %v", err)
	}
	if _, _, err := runAt(t, context.Background(), "", "workspace", "create", "--entity", "payments", "--shell", "--no-shell"); err == nil {
		t.Error("--shell and --no-shell were taken together")
	}
}
