package build

import (
	"context"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec/reapi/reapitest"
	sourcev1 "github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1"
	tell "github.com/frontseat-dev/frontseat/server/internal/told"
	"github.com/frontseat-dev/frontseat/server/internal/told/toldtest"
	"github.com/frontseat-dev/frontseat/server/internal/vcs/chosen"
	"github.com/frontseat-dev/frontseat/server/testsupport/backstagetest"
	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
)

// generations counts what the tests tell a daemon, as the control plane
// does.
var generations atomic.Int64

// set tells a daemon what to hold, as the control plane does, at a
// generation of its own, its entities resolved through a fake Backstage
// where one is given, and returns which.
func (c clients) set(ctx context.Context, spec *workspacev1.WorkspaceSpec, catalog ...backstagetest.Served) (int64, error) {
	generation := generations.Add(1)
	var through backstagetest.Served
	if len(catalog) > 0 {
		through = catalog[0]
	}
	telling, err := toldtest.Spec(ctx, spec, generation, through, chosen.System().NameOf)
	if err != nil {
		return generation, err
	}
	_, err = c.source.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: telling})
	return generation, err
}

// catalogOf is a fake Backstage of a forge's entities.
func catalogOf(t testing.TB, forge testforge.Forge) backstagetest.Served {
	t.Helper()
	return backstagetest.Serve(t, forge.Entities(t))
}

// populated tells a daemon to populate its workspace, as the control
// plane does, its entities resolved through a fake Backstage where one is
// given, and returns the workspace once it has settled.
func populated(t *testing.T, c clients, req *workspacev1.WorkspaceSpec, catalog ...backstagetest.Served) *workspacev1.Workspace {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	generation, err := c.set(ctx, req, catalog...)
	if err != nil {
		t.Fatal(err)
	}
	said, err := c.source.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: generation})
	if err != nil {
		t.Fatal(err)
	}
	last := &workspacev1.Workspace{Status: tell.Status(said.GetSource())}
	if last.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		t.Fatalf("the workspace settled %v: %s", last.GetStatus().GetState(), last.GetStatus().GetFailure())
	}
	return last
}

// planned returns the labels a build of a daemon's workspace plans.
func planned(t *testing.T, c clients) string {
	t.Helper()
	run := create(t, c.runs)
	var labels []string
	for _, e := range events(t, c.runs, run.GetId(), 0) {
		for _, a := range e.GetPlanned().GetActions() {
			labels = append(labels, a.GetLabel())
		}
	}
	return strings.Join(labels, " ")
}

func hello(whole bool) *workspacev1.WorkspaceSpec {
	return &workspacev1.WorkspaceSpec{
		Entities:          []*typev1.EntityRef{{Kind: "component", Namespace: "default", Name: "hello"}},
		WholeRepositories: whole,
	}
}

func TestABuildIsRefusedUntilTheWorkspaceHoldsARepository(t *testing.T) {
	c := serve(t, scripted{}, t.TempDir(), false)

	_, err := c.runs.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{})

	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("err = %v, want failed precondition", err)
	}
}

func TestARepositoryIsClonedAtItsCommitAndBuilt(t *testing.T) {
	forge := testforge.Make(t)
	c := serve(t, scripted{}, t.TempDir(), false)

	got := populated(t, c, &workspacev1.WorkspaceSpec{Repositories: []*workspacev1.RepositorySource{{Remote: forge.Remote, Ref: "main"}}})

	if len(got.GetStatus().GetRepositories()) != 1 {
		t.Fatalf("the workspace holds %v, want one repository", got.GetStatus().GetRepositories())
	}
	if r := got.GetStatus().GetRepositories()[0]; r.GetName() != "services" || len(r.GetCommit()) != 40 || len(r.GetRoots()) != 0 {
		t.Errorf("the repository is %v, want services, whole, at a commit", r)
	}
	if labels := planned(t, c); !strings.Contains(labels, "//services/hello:go.compile") || !strings.Contains(labels, "//services/greet:go.compile") {
		t.Errorf("a build plans %s, want both modules of the repository", labels)
	}
}

// forged is a forge of the test's own, and a daemon over an empty
// workspace, to be populated from the forge's catalog.
func forged(t *testing.T) (testforge.Forge, string, clients) {
	t.Helper()
	forge := testforge.Make(t)
	workspace := t.TempDir()
	return forge, workspace, serve(t, scripted{}, workspace, false)
}

func TestEntitiesAskedForWholeBringTheRepositoriesThatHoldThemWhole(t *testing.T) {
	forge, workspace, c := forged(t)

	got := populated(t, c, hello(true), catalogOf(t, forge))

	if r := got.GetStatus().GetRepositories()[0]; r.GetName() != "services" || len(r.GetRoots()) != 0 {
		t.Errorf("the repository is %v, want services, whole", r)
	}
	if _, err := os.Stat(filepath.Join(workspace, "services", "docs", "guide.md")); err != nil {
		t.Errorf("docs is not there, and the repository was to be whole: %v", err)
	}
}

func TestEntitiesBringOnlyTheDirectoriesTheyGovern(t *testing.T) {
	forge, workspace, c := forged(t)

	got := populated(t, c, hello(false), catalogOf(t, forge))

	if roots := strings.Join(got.GetStatus().GetRepositories()[0].GetRoots(), ","); roots != "greet,hello" {
		t.Errorf("the roots are %s, want hello and greet, which it depends on", roots)
	}
	if _, err := os.Stat(filepath.Join(workspace, "services", "docs")); !os.IsNotExist(err) {
		t.Errorf("docs is there, and no entity governs it (%v)", err)
	}
	if labels := planned(t, c); !strings.Contains(labels, "//services/hello:go.compile") || !strings.Contains(labels, "//services/greet:go.compile") {
		t.Errorf("a build plans %s, want both entities' modules", labels)
	}
}

// gateway asks for the entity gateway, which is in one repository and
// depends on an entity in another.
func gateway(whole bool) *workspacev1.WorkspaceSpec {
	return &workspacev1.WorkspaceSpec{
		Entities:          []*typev1.EntityRef{{Kind: "component", Namespace: "default", Name: "gateway"}},
		WholeRepositories: whole,
	}
}

// ranGateway brings gateway into a daemon's workspace, resolved through
// a fake Backstage of a forge's entities, and returns a build of it as
// it ended.
func ranGateway(t *testing.T, c clients, forge testforge.Forge) *buildv1.Build {
	t.Helper()
	populated(t, c, gateway(false), catalogOf(t, forge))
	return wait(t, c.runs, create(t, c.runs).GetId())
}

// roots returns each repository of a workspace with its roots, as
// "name:root,root", sorted; a whole repository is "name:".
func roots(held *workspacev1.Workspace) string {
	var out []string
	for _, r := range held.GetStatus().GetRepositories() {
		out = append(out, r.GetName()+":"+strings.Join(r.GetRoots(), ","))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func exists(parts ...string) bool {
	_, err := os.Stat(filepath.Join(parts...))
	return err == nil
}

func TestEntitiesInSeveralRepositoriesAskedForWholeBringEachOfThem(t *testing.T) {
	forge, workspace, c := forged(t)

	got := populated(t, c, gateway(true), catalogOf(t, forge))

	if held := roots(got); held != "platform: services:" {
		t.Errorf("the workspace holds %q, want platform and services, each whole", held)
	}
	for _, there := range [][]string{{"platform", "gateway", "go.mod"}, {"platform", "notes", "readme.md"}, {"services", "greet", "go.mod"}, {"services", "docs", "guide.md"}} {
		if !exists(append([]string{workspace}, there...)...) {
			t.Errorf("%s is not there, and both repositories were to be whole", filepath.Join(there...))
		}
	}
	if labels := planned(t, c); !strings.Contains(labels, "//platform/gateway:go.compile") || !strings.Contains(labels, "//services/greet:go.compile") {
		t.Errorf("a build plans %s, want modules of both repositories", labels)
	}
}

func TestEntitiesInSeveralRepositoriesBringOnlyWhatTheyGovernInEach(t *testing.T) {
	forge, workspace, c := forged(t)

	got := populated(t, c, gateway(false), catalogOf(t, forge))

	if held := roots(got); held != "platform:gateway services:greet" {
		t.Errorf("the workspace holds %q, want gateway of platform and greet of services", held)
	}
	for _, gone := range [][]string{{"platform", "log"}, {"platform", "notes"}, {"services", "hello"}, {"services", "docs"}} {
		if exists(append([]string{workspace}, gone...)...) {
			t.Errorf("%s is there, and no entity asked for governs it", filepath.Join(gone...))
		}
	}
	for _, pinned := range []string{"platform", "services"} {
		if !exists(workspace, pinned, "mise.lock") {
			t.Errorf("%s has no mise.lock: a partial checkout keeps what pins the repository's tools", pinned)
		}
	}
	labels := strings.Fields(planned(t, c))
	if len(labels) != 6 {
		t.Errorf("a build plans %v, want the three actions of gateway and of greet and no others", labels)
	}
}

func TestAWorkspaceOfEntitiesInSeveralRepositoriesBuildsOnTheGrid(t *testing.T) {
	grid := reapitest.Dial(t)
	forge := testforge.Make(t)
	c := serve(t, grid, t.TempDir(), false)
	ended := ranGateway(t, c, forge)

	if ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || ended.GetCounts().GetPassed() != 6 {
		actions, _ := c.runs.ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{BuildId: ended.GetId()})
		t.Fatalf("the run ended %s (%s) with %d passed, want SUCCESS and 6: %v", ended.GetExitCode(), ended.GetFailure(), ended.GetCounts().GetPassed(), actions.GetActions())
	}
}

func TestMoreEntitiesAreAddedToAWorkspaceThatHoldsSome(t *testing.T) {
	forge, _, c := forged(t)
	populated(t, c, gateway(false), catalogOf(t, forge))

	// log is in a repository the workspace holds part of; hello is in the
	// other, beside greet, which is there already.
	got := populated(t, c, &workspacev1.WorkspaceSpec{
		Entities: []*typev1.EntityRef{{Kind: "component", Namespace: "default", Name: "log"}, {Kind: "component", Namespace: "default", Name: "hello"}},
	}, catalogOf(t, forge))

	if held := roots(got); held != "platform:gateway,log services:greet,hello" {
		t.Errorf("the workspace holds %q, want each repository widened by the entity added to it", held)
	}
	if labels := strings.Fields(planned(t, c)); len(labels) != 12 {
		t.Errorf("a build plans %v, want the three actions of each of the four modules", labels)
	}
}

// What brings a workspace's repositories ends with the daemon: with a
// repository being cloned from a forge that accepts and never answers,
// Close returns once the clone has stopped, and the workspace says why
// it was not populated. The control is every test that populates a
// workspace from a forge that answers, which is ready.
func TestClosingADaemonEndsWhatIsBringingItsWorkspacesRepositories(t *testing.T) {
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 16)
	go func() {
		defer close(accepted)
		for {
			conn, err := silent.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	t.Cleanup(func() {
		_ = silent.Close()
		for conn := range accepted {
			_ = conn.Close()
		}
	})
	c := serve(t, scripted{}, t.TempDir(), false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	generation, err := c.set(ctx, &workspacev1.WorkspaceSpec{Repositories: []*workspacev1.RepositorySource{{Remote: "http://" + silent.Addr().String() + "/acme/code.git", Ref: "main"}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-ctx.Done():
		t.Fatal("the forge was not asked for the repository")
	}

	closed := make(chan struct{})
	// The daemon's program stops the build, and then what fills its
	// directory.
	go func() { c.daemon.Close(); c.held.Close(); close(closed) }()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("Close did not return: what brings the repository was not stopped")
	}

	said, err := c.source.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: generation})
	if err != nil {
		t.Fatalf("the workspace did not settle once its daemon closed: %v", err)
	}
	if status := tell.Status(said.GetSource()); status.GetState() != workspacev1.WorkspaceStatus_STATE_FAILED || !strings.Contains(status.GetFailure(), "the daemon stopped") {
		t.Errorf("the workspace is %v (%s), want failed, saying the daemon stopped", status.GetState(), status.GetFailure())
	}
}

func TestAMountedCheckoutIsNotPopulated(t *testing.T) {
	forge := testforge.Make(t)
	c := serve(t, scripted{}, t.TempDir(), true)

	_, err := c.set(context.Background(), &workspacev1.WorkspaceSpec{Repositories: []*workspacev1.RepositorySource{{Remote: forge.Remote}}})

	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("err = %v, want failed precondition", err)
	}
}

// A repository is named by whoever asks for it, and its name is the
// directory it is cloned into: one that leads out of the workspace is
// refused as what was asked, before anything is cloned.
func TestARepositoryNamedOutOfTheWorkspaceIsNotCloned(t *testing.T) {
	forge := testforge.Make(t)
	for name, checkout := range map[string]*sourcev1.RepositorySource{
		"named out of the workspace":                 {Name: "../elsewhere", Remote: forge.Remote, Ref: "main"},
		"named by a path":                            {Name: "a/b", Remote: forge.Remote, Ref: "main"},
		"named where the workspace keeps its record": {Name: ".frontseat", Remote: forge.Remote, Ref: "main"},
		"a remote that names no directory":           {Name: chosen.System().NameOf("file:///"), Remote: "file:///", Ref: "main"},
		"a directory out of the repository":          {Remote: forge.Remote, Ref: "main", Roots: []string{"../other"}},
	} {
		t.Run(name, func(t *testing.T) {
			outside := t.TempDir()
			directory := filepath.Join(outside, "workspace")
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			c := serve(t, scripted{}, directory, false)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			generation := generations.Add(1)
			_, err := c.source.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: &sourcev1.SourceSpec{Generation: generation, Repositories: []*sourcev1.RepositorySource{checkout}}})

			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("err = %v, want what was asked refused as invalid", err)
			}
			if err == nil {
				// What was started is let end, so that what it made is seen.
				if _, err := c.source.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: generation}); err != nil {
					t.Fatal(err)
				}
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 1 {
				t.Errorf("something was made beside the workspace: %v", entries)
			}
			if entries, _ := os.ReadDir(directory); len(entries) != 0 {
				t.Errorf("the workspace holds %v, and nothing was to be cloned", entries)
			}
		})
	}
}
