package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// graphed is a workspace's daemon whose graph is three components:
// gateway needs ledger, which needs money, in another repository; both
// gateway and money require cobra, from outside the workspace. It keeps
// what it was asked to build.
type graphed struct {
	buildv1alpha1connect.UnimplementedGraphServiceHandler
	buildv1alpha1connect.UnimplementedBuildServiceHandler
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
	built []*buildv1.CreateBuildRequest
	// at is where the control plane says the workspace's build API is.
	at string
}

func component(name string) *typev1.EntityRef {
	return &typev1.EntityRef{Kind: "component", Namespace: "default", Name: name}
}

func (g *graphed) ListEntities(context.Context, *buildv1.ListEntitiesRequest) (*buildv1.ListEntitiesResponse, error) {
	return &buildv1.ListEntitiesResponse{Entities: []*buildv1.Entity{
		{Ref: component("gateway"), Repository: "platform", Roots: []string{"gateway"}, DependsOn: []*typev1.EntityRef{component("ledger")}},
		{Ref: component("ledger"), Repository: "platform", Roots: []string{"ledger"}, DependsOn: []*typev1.EntityRef{component("money")}},
		{Ref: component("money"), Repository: "libs", Roots: []string{"money"}},
	}}, nil
}

func (g *graphed) ListProjects(context.Context, *buildv1.ListProjectsRequest) (*buildv1.ListProjectsResponse, error) {
	return &buildv1.ListProjectsResponse{Projects: []*buildv1.Project{
		{Repository: "platform", Root: "gateway", Plugin: "go", Kind: "go-module", Name: "example.com/gateway", Owner: component("gateway"), Requires: []*buildv1.ProjectRequirement{
			{Coordinate: "go:example.com/ledger", Origin: "platform/gateway/go.mod:5", ProviderProject: &buildv1.ProjectRef{Repository: "platform", Root: "ledger", Plugin: "go"}},
			{Coordinate: "go:github.com/spf13/cobra", Origin: "platform/gateway/go.mod:6", Version: "v1.9.1"},
		}},
		{Repository: "platform", Root: "ledger", Plugin: "go", Kind: "go-module", Name: "example.com/ledger", Owner: component("ledger"), Requires: []*buildv1.ProjectRequirement{
			{Coordinate: "go:example.com/money", Origin: "platform/ledger/go.mod:4", ProviderProject: &buildv1.ProjectRef{Repository: "libs", Root: "money", Plugin: "go"}},
		}},
		{Repository: "libs", Root: "money", Plugin: "go", Kind: "go-module", Name: "example.com/money", Owner: component("money"), Requires: []*buildv1.ProjectRequirement{
			{Coordinate: "go:github.com/spf13/cobra", Origin: "libs/money/go.mod:3", Version: "v1.8.0"},
		}},
	}}, nil
}

func (g *graphed) DescribeWorkspace(context.Context, *workspacev1.DescribeWorkspaceRequest) (*workspacev1.DescribeWorkspaceResponse, error) {
	return &workspacev1.DescribeWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: "graphed", Status: &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_READY, BuildUri: g.at, Repositories: []*workspacev1.Repository{
		{Name: "platform", Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{Name: "libs", Commit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}}}}, nil
}

func (g *graphed) CreateBuild(_ context.Context, request *buildv1.CreateBuildRequest) (*buildv1.CreateBuildResponse, error) {
	g.built = append(g.built, request)
	return &buildv1.CreateBuildResponse{Build: &buildv1.Build{Id: "build_1"}}, nil
}

func (g *graphed) StreamBuildEvents(_ context.Context, _ *buildv1.StreamBuildEventsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildEventsServerStream) error {
	return stream.Send(&buildv1.StreamBuildEventsResponse{Events: []*buildv1.BuildEvent{{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: &buildv1.Build{Id: "build_1", ExitCode: &buildv1.ExitCode{Name: "TESTS_FAILED", Code: 3}}}}}}})
}

// asking runs the command line against the graphed daemon.
func asking(t *testing.T, g *graphed, args ...string) (string, int, error) {
	t.Helper()
	builds := connect.NewServer()
	buildv1alpha1connect.RegisterGraphServiceHandler(builds, g)
	buildv1alpha1connect.RegisterBuildServiceHandler(builds, g)
	daemon := http.NewServeMux()
	connecthttp.Mount(daemon, builds)
	plane := http.NewServeMux()
	keeps := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(keeps, g)
	connecthttp.Mount(plane, keeps)
	plane.Handle("/workspaces/graphed/build/", http.StripPrefix("/workspaces/graphed/build", daemon))
	srv := httptest.NewServer(plane)
	t.Cleanup(srv.Close)
	g.at = srv.URL + "/workspaces/graphed/build"
	serverAt(t, srv.URL)
	lastOpened(t, srv.URL, "graphed")
	return runAt(t, context.Background(), "", args...)
}

// askingInside is asking, inside a workspace's sandbox, of its daemon.
func askingInside(t *testing.T, g *graphed, args ...string) (string, int, error) {
	t.Helper()
	return runAt(t, context.Background(), serving(t, func(api *connect.Server) {
		buildv1alpha1connect.RegisterGraphServiceHandler(api, g)
		buildv1alpha1connect.RegisterBuildServiceHandler(api, g)
	}), args...)
}

// query with nothing named lists every entity, where it is and what it
// depends on.
func TestQueryListsTheEntitiesAndWhatEachDependsOn(t *testing.T) {
	printed, code, err := asking(t, &graphed{}, "query")
	want := "gateway  platform  gateway\n  depends on ledger\nledger  platform  ledger\n  depends on money\nmoney  libs  money\n"
	if err != nil || code != 0 || printed != want {
		t.Errorf("query ended %d, %v, printing\n%s\nwant\n%s", code, err, printed, want)
	}
}

// query of an entity says what it depends on, what depends on it, and
// each of its projects with what it requires, where that is said and
// what in the workspace provides it.
func TestQueryOfAnEntitySaysWhatItNeedsAndWhatNeedsIt(t *testing.T) {
	printed, code, err := asking(t, &graphed{}, "query", "ledger")
	want := "ledger  platform  ledger\n  depends on money\n  needed by gateway\n" +
		"  go-module example.com/ledger  platform/ledger\n    go:example.com/money  platform/ledger/go.mod:4  money\n"
	if err != nil || code != 0 || printed != want {
		t.Errorf("query ledger ended %d, %v, printing\n%s\nwant\n%s", code, err, printed, want)
	}
	// A path is the projects at or beneath it.
	printed, _, err = asking(t, &graphed{}, "query", "libs/")
	if want := "  go-module example.com/money  libs/money  money\n    go:github.com/spf13/cobra  libs/money/go.mod:3 at v1.8.0\n"; err != nil || printed != want {
		t.Errorf("query of a path: %v, printing\n%s\nwant\n%s", err, printed, want)
	}
}

// Why one entity depends on another is the shortest chain between them:
// each step with what is required, the line that says so, and the
// commit the entity required is at in its repository.
func TestQueryWhySaysWhyAndAtWhichCommit(t *testing.T) {
	printed, code, err := asking(t, &graphed{}, "query", "gateway", "--why", "money")
	want := "gateway -> ledger -> money\n" +
		"  gateway -> ledger  go:example.com/ledger  platform/gateway/go.mod:5  ledger at platform@aaaaaaaaaaaa\n" +
		"  ledger -> money  go:example.com/money  platform/ledger/go.mod:4  money at libs@bbbbbbbbbbbb\n"
	if err != nil || code != 0 || printed != want {
		t.Errorf("query --why ended %d, %v, printing\n%s\nwant\n%s", code, err, printed, want)
	}
	// What the workspace does not hold is named as it is required: each
	// chain to what requires it, the line that says so, and the version
	// the lock resolves it to there, which may differ from one to another.
	printed, code, err = asking(t, &graphed{}, "query", "gateway", "--why", "go:github.com/spf13/cobra")
	want = "gateway -> go:github.com/spf13/cobra\n" +
		"  gateway -> go:github.com/spf13/cobra  platform/gateway/go.mod:6 at v1.9.1\n" +
		"gateway -> ledger -> money -> go:github.com/spf13/cobra\n" +
		"  gateway -> ledger  go:example.com/ledger  platform/gateway/go.mod:5  ledger at platform@aaaaaaaaaaaa\n" +
		"  ledger -> money  go:example.com/money  platform/ledger/go.mod:4  money at libs@bbbbbbbbbbbb\n" +
		"  money -> go:github.com/spf13/cobra  libs/money/go.mod:3 at v1.8.0\n"
	if err != nil || code != 0 || printed != want {
		t.Errorf("query --why of a requirement ended %d, %v, printing\n%s\nwant\n%s", code, err, printed, want)
	}
	// Inside a workspace's sandbox, which describes no workspace, the
	// same chain is said without the commits.
	printed, code, err = askingInside(t, &graphed{}, "query", "gateway", "--why", "money")
	want = "gateway -> ledger -> money\n" +
		"  gateway -> ledger  go:example.com/ledger  platform/gateway/go.mod:5\n" +
		"  ledger -> money  go:example.com/money  platform/ledger/go.mod:4\n"
	if err != nil || code != 0 || printed != want {
		t.Errorf("query --why inside ended %d, %v, printing\n%s\nwant\n%s", code, err, printed, want)
	}
	// One that does not depend on the other says so, and exits 1: the
	// control is the other way round, above.
	printed, code, err = asking(t, &graphed{}, "query", "money", "--why", "gateway")
	if err != nil || code != 1 || printed != "money does not depend on gateway\n" {
		t.Errorf("query --why of what is not depended on ended %d, %v, printing %q", code, err, printed)
	}
	// What names nothing of the workspace is refused, and so is an
	// action, which is a build's.
	for _, args := range [][]string{{"query", "gateway", "--why", "nothing-here"}, {"query", "nothing-here"}, {"query", "//platform/gateway:go.test"}, {"query", "--why", "money"}} {
		if _, _, err := asking(t, &graphed{}, args...); err == nil {
			t.Errorf("%v was answered", args)
		}
	}
}

// With --json, query writes one JSON document, for a program to read:
// the same answer, in a form that is kept stable.
func TestQueryWritesJSONForAProgram(t *testing.T) {
	printed, _, err := asking(t, &graphed{}, "query", "gateway", "--why", "money", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Why struct {
			From  string `json:"from"`
			To    string `json:"to"`
			Paths [][]struct {
				From    string `json:"from"`
				To      string `json:"to"`
				Because []struct {
					Coordinate string `json:"coordinate"`
					Origin     string `json:"origin"`
				} `json:"because"`
				Repository string `json:"repository"`
				Commit     string `json:"commit"`
			} `json:"paths"`
		} `json:"why"`
	}
	if err := json.Unmarshal([]byte(printed), &got); err != nil {
		t.Fatalf("%v: %s", err, printed)
	}
	if got.Why.From != "component:default/gateway" || got.Why.To != "component:default/money" || len(got.Why.Paths) != 1 || len(got.Why.Paths[0]) != 2 {
		t.Fatalf("query --json wrote %s", printed)
	}
	last := got.Why.Paths[0][1]
	if last.From != "component:default/ledger" || last.To != "component:default/money" || last.Commit != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || last.Repository != "libs" || last.Because[0].Origin != "platform/ledger/go.mod:4" {
		t.Errorf("the last step is %+v", last)
	}
	// What is required from outside says the version its lock resolves
	// it to, for a program too.
	printed, _, err = asking(t, &graphed{}, "query", "gateway", "--why", "go:github.com/spf13/cobra", "--json")
	var versioned struct {
		Why struct {
			Paths [][]struct {
				Because []struct {
					Version string `json:"version"`
				} `json:"because"`
			} `json:"paths"`
		} `json:"why"`
	}
	if err != nil || json.Unmarshal([]byte(printed), &versioned) != nil || len(versioned.Why.Paths) != 2 || versioned.Why.Paths[0][0].Because[0].Version != "v1.9.1" || versioned.Why.Paths[1][2].Because[0].Version != "v1.8.0" {
		t.Errorf("query --why --json of a requirement: %v, wrote %s", err, printed)
	}
	printed, _, err = asking(t, &graphed{}, "query", "--json")
	var listed struct {
		Entities []struct {
			Ref       string   `json:"ref"`
			DependsOn []string `json:"dependsOn"`
		} `json:"entities"`
	}
	if err != nil || json.Unmarshal([]byte(printed), &listed) != nil || len(listed.Entities) != 3 || listed.Entities[0].DependsOn[0] != "component:default/ledger" {
		t.Errorf("query --json of everything: %v, wrote %s", err, printed)
	}
	if strings.Count(printed, "\n") != 1 {
		t.Errorf("query --json wrote more than one line: %q", printed)
	}
}

// test builds what the tests need and runs every test action: the build
// it asks for keeps the actions that run tests, as their plugins say,
// whatever they are named, of what it is pointed at, with the profile
// given, and it exits as the build did. The control is build, which
// keeps every action.
func TestTestRunsTheTestActionsOfWhatItIsPointedAt(t *testing.T) {
	g := &graphed{}
	printed, code, err := asking(t, g, "test", "gateway", "platform/", "--profile", "production")
	if err != nil || code != 3 || !strings.Contains(printed, "TESTS_FAILED") {
		t.Fatalf("test ended %d, %v, printing\n%s", code, err, printed)
	}
	if len(g.built) != 1 || !g.built[0].GetTests() || len(g.built[0].GetOnly()) != 0 || strings.Join(g.built[0].GetTargets(), ",") != "gateway,platform/" || g.built[0].GetProfile() != "production" || g.built[0].GetFix() {
		t.Errorf("test asked for %v", g.built)
	}
	if _, _, err := asking(t, g, "build", "gateway"); err != nil || len(g.built) != 2 || len(g.built[1].GetOnly()) != 0 || g.built[1].GetTests() {
		t.Errorf("build asked for %v (%v)", g.built, err)
	}
}

// noTests is a daemon whose build of the tests of what it was pointed at
// found none, and ended so, as Bazel's `bazel test` does.
type noTests struct{ graphed }

func (n *noTests) StreamBuildEvents(_ context.Context, _ *buildv1.StreamBuildEventsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildEventsServerStream) error {
	return stream.Send(&buildv1.StreamBuildEventsResponse{Events: []*buildv1.BuildEvent{{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: &buildv1.Build{Id: "build_1", Failure: "nothing ledger points at runs tests", ExitCode: &buildv1.ExitCode{Name: "NO_TESTS_FOUND", Code: 4}}}}}}})
}

// test of what holds no tests exits 4 and says so, as Bazel does; the
// control above, a build whose tests ran and failed, exits 3.
func TestTestOfWhatHoldsNoTestsExitsFour(t *testing.T) {
	n := &noTests{}
	printed, code, err := runAt(t, context.Background(), serving(t, func(api *connect.Server) {
		buildv1alpha1connect.RegisterBuildServiceHandler(api, n)
	}), "test", "ledger")
	if err != nil || code != 4 || !strings.Contains(printed, "NO_TESTS_FOUND") || !strings.Contains(printed, "runs tests") || len(n.built) != 1 || !n.built[0].GetTests() {
		t.Errorf("test of what holds no tests ended %d, %v, printing\n%s", code, err, printed)
	}
}

// ambiguous is a daemon whose graph holds two components of one name, in
// two namespaces, and a project of a daemon given one directory, whose repository
// has no name.
type ambiguous struct{ graphed }

func (a *ambiguous) ListEntities(ctx context.Context, r *buildv1.ListEntitiesRequest) (*buildv1.ListEntitiesResponse, error) {
	listed, _ := a.graphed.ListEntities(ctx, r)
	listed.Entities = append(listed.Entities, &buildv1.Entity{Ref: &typev1.EntityRef{Kind: "component", Namespace: "other", Name: "money"}, Repository: "libs"})
	return listed, nil
}

func (a *ambiguous) ListProjects(context.Context, *buildv1.ListProjectsRequest) (*buildv1.ListProjectsResponse, error) {
	return &buildv1.ListProjectsResponse{Projects: []*buildv1.Project{{Root: "cli", Plugin: "go", Kind: "go-module", Name: "example.com/cli"}}}, nil
}

// A name of two entities is refused as such, and not taken for a
// requirement; written with its namespace it is one, which is the
// control. A
// path of a daemon given one directory is the project's directory.
func TestQuerySaysWhereANameIsOfTwoEntities(t *testing.T) {
	a := &ambiguous{}
	ask := func(args ...string) (string, int, error) {
		t.Helper()
		return runAt(t, context.Background(), serving(t, func(api *connect.Server) {
			buildv1alpha1connect.RegisterGraphServiceHandler(api, a)
			workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, a)
		}), args...)
	}
	if _, _, err := ask("query", "gateway", "--why", "money"); err == nil || strings.Contains(err.Error(), "require") {
		t.Errorf("a name of two entities: %v, want it said to be ambiguous", err)
	}
	if _, code, err := ask("query", "gateway", "--why", "default/money"); err != nil || code != 0 {
		t.Errorf("a name written with its kind: %d, %v", code, err)
	}
	if printed, _, err := ask("query", "cli/"); err != nil || !strings.Contains(printed, "go-module example.com/cli  cli") {
		t.Errorf("a project of a repository with no name: %v, printed %q", err, printed)
	}
}

// A build is followed to the event that says how it ended: events that
// end before it are said to, and not taken for a build that passed.
func TestABuildWhoseEventsEndBeforeItIsNoPass(t *testing.T) {
	g := &cutShort{}
	_, code, err := runAt(t, context.Background(), serving(t, func(api *connect.Server) {
		buildv1alpha1connect.RegisterBuildServiceHandler(api, g)
	}), "build", "events", "build_1")
	if err == nil || code != 0 || !strings.Contains(err.Error(), "ended before it did") {
		t.Errorf("events that ended before the build: %d, %v", code, err)
	}
}

// cutShort sends one event of a build, and ends the stream.
type cutShort struct{ graphed }

func (*cutShort) StreamBuildEvents(_ context.Context, _ *buildv1.StreamBuildEventsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildEventsServerStream) error {
	return stream.Send(&buildv1.StreamBuildEventsResponse{Events: []*buildv1.BuildEvent{{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{Outcome: buildv1.Outcome_OUTCOME_PASSED, Label: "//x:go.compile"}}}}}})
}
