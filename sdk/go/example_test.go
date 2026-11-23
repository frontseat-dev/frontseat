package frontseat_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// A Client is made for a server, and asks nothing of it until a call is
// made. A control plane on another machine is named with https; the
// local stack's is http://127.0.0.1:7700.
func ExampleNew() {
	fs, err := frontseat.New("https://frontseat.example/", frontseat.WithToken("the-token-the-control-plane-gave"))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(fs.Server())
	// Output: https://frontseat.example
}

// Open gives the workspace of a repository and a branch: a new one, or
// the one there is, as it was left. It returns once the workspace can be
// worked in.
func ExampleClient_Open() {
	fs, server := standIn()
	defer server.Close()
	ctx := context.Background()

	opened, err := fs.Open(ctx, frontseat.WorkspaceSpec{
		Repositories: []frontseat.Repository{{Remote: "https://forge.example/acme/services.git", Ref: "main"}},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(opened.Workspace.Name(), opened.Outcome == frontseat.OpenOutcomeCreated)
	for _, repository := range opened.Status.GetStatus().GetRepositories() {
		fmt.Println(repository.GetName(), repository.GetRemote())
	}
	// Output:
	// services-main true
	// services https://forge.example/acme/services.git
}

// A build is started, followed by its events to the one that says how it
// ended, and waited for. It goes on whether or not it is followed. It is
// asked of a build daemon, which a Client is made for as it is for a
// control plane: what the daemon builds is Here.
func ExampleWorkspace_Build() {
	daemon, server := standIn()
	defer server.Close()
	ctx := context.Background()

	ws := daemon.Here()
	build, err := ws.Build(ctx, frontseat.BuildRequest{Targets: []string{"greeter"}})
	if err != nil {
		fmt.Println(err)
		return
	}
	for event, err := range build.Events(ctx, 0) {
		if err != nil {
			fmt.Println(err)
			return
		}
		if result := event.GetActionFinished().GetResult(); result != nil {
			fmt.Println(result.GetLabel(), result.GetOutcome() == buildv1.Outcome_OUTCOME_PASSED)
		}
	}
	ended, err := build.Wait(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(build.ID(), ended.GetExitCode())
	// Output:
	// //services/greeter:go.compile true
	// //services/greeter:go.test true
	// build_1 EXIT_CODE_SUCCESS
}

// A release is planned first, which changes nothing; the plan's etag
// then releases that plan and no other, and is refused where the
// workspace has moved since.
func ExampleWorkspace_Release() {
	daemon, server := standIn()
	defer server.Close()
	ctx := context.Background()

	ws := daemon.Here()
	plan, err := ws.PlanRelease(ctx, frontseat.ReleaseRequest{Entities: []entity.Pattern{{Kind: "component", Name: "greeter"}}})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, unit := range plan.GetUnits() {
		fmt.Println(entity.FromProto(unit.GetEntity()).Short(), unit.GetLastVersion(), "->", unit.GetNextVersion())
	}
	release, err := ws.Release(ctx, frontseat.ReleaseRequest{Entities: []entity.Pattern{{Kind: "component", Name: "greeter"}}, Etag: plan.GetEtag()})
	if err != nil {
		fmt.Println(err)
		return
	}
	ended, err := release.Wait(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(release.ID(), ended.GetState() == frontseat.ReleaseSucceeded)
	// Output:
	// greeter 1.2.0 -> 1.3.0
	// rel_1 true
}

// What a server refuses by name is read by its reason, which each call's
// contract lists, and never by its message, which is for a person.
func ExampleError() {
	daemon, server := standIn()
	defer server.Close()

	ws := daemon.Here()
	_, err := ws.Build(context.Background(), frontseat.BuildRequest{Profile: typev1.Profile(7)})
	var refused *frontseat.Error
	if errors.As(err, &refused) && refused.Reason == frontseat.ReasonProfileUnknown {
		fmt.Println(refused.Code, refused.Reason, refused.Domain)
	}
	// Output: invalid_argument PROFILE_UNKNOWN build.frontseat.dev
}

// standIn serves, on this machine, what the examples ask a control plane
// and a build daemon, at one address for the examples' sake: one
// workspace, services-main, one build of two actions that pass, and one
// release of one unit; and a Client of it.
func standIn() (*frontseat.Client, *httptest.Server) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	api := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, plane{})
	buildv1alpha1connect.RegisterBuildServiceHandler(api, builder{})
	buildv1alpha1connect.RegisterReleaseServiceHandler(api, releaser{})
	connecthttp.Mount(mux, api)
	fs, err := frontseat.New(server.URL)
	if err != nil {
		panic(err)
	}
	return fs, server
}

// plane is the examples' control plane.
type plane struct {
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
}

func (p plane) held(state workspacev1.WorkspaceStatus_State) *workspacev1.Workspace {
	return &workspacev1.Workspace{Name: "services-main", Status: &workspacev1.WorkspaceStatus{
		State:        state,
		Repositories: []*workspacev1.Repository{{Name: "services", Remote: "https://forge.example/acme/services.git"}},
	}}
}

func (p plane) CreateWorkspace(context.Context, *workspacev1.CreateWorkspaceRequest) (*workspacev1.CreateWorkspaceResponse, error) {
	return &workspacev1.CreateWorkspaceResponse{Created: true, Workspace: p.held(workspacev1.WorkspaceStatus_STATE_STARTING)}, nil
}

func (p plane) WaitWorkspace(context.Context, *workspacev1.WaitWorkspaceRequest) (*workspacev1.WaitWorkspaceResponse, error) {
	return &workspacev1.WaitWorkspaceResponse{Workspace: p.held(workspacev1.WorkspaceStatus_STATE_READY)}, nil
}

func (p plane) DescribeWorkspace(context.Context, *workspacev1.DescribeWorkspaceRequest) (*workspacev1.DescribeWorkspaceResponse, error) {
	return &workspacev1.DescribeWorkspaceResponse{Workspace: p.held(workspacev1.WorkspaceStatus_STATE_READY)}, nil
}

// builder is the build service of the examples' daemon.
type builder struct {
	buildv1alpha1connect.UnimplementedBuildServiceHandler
}

// passed is the examples' one build, as it ended.
var passed = &buildv1.Build{Id: "build_1", State: buildv1.Build_STATE_SUCCEEDED, ExitCode: buildv1.ExitCode_EXIT_CODE_SUCCESS}

func (builder) CreateBuild(_ context.Context, asked *buildv1.CreateBuildRequest) (*buildv1.CreateBuildResponse, error) {
	if asked.GetProfile() != typev1.Profile_PROFILE_UNSPECIFIED {
		named, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: "PROFILE_UNKNOWN", Domain: "build.frontseat.dev"})
		if err != nil {
			return nil, err
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, "the workspace defines no such profile").WithDetail(named)
	}
	return &buildv1.CreateBuildResponse{Build: &buildv1.Build{Id: passed.GetId(), State: buildv1.Build_STATE_RUNNING}}, nil
}

func (builder) StreamBuildEvents(_ context.Context, _ *buildv1.StreamBuildEventsRequest, events buildv1alpha1connect.BuildServiceStreamBuildEventsServerStream) error {
	finished := func(label string) *buildv1.BuildEvent_ActionFinished_ {
		return &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{Label: label, Outcome: buildv1.Outcome_OUTCOME_PASSED}}}
	}
	return events.Send(&buildv1.StreamBuildEventsResponse{Events: []*buildv1.BuildEvent{
		{Sequence: 1, Event: finished("//services/greeter:go.compile")},
		{Sequence: 2, Event: finished("//services/greeter:go.test")},
		{Sequence: 3, Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: passed}}},
	}})
}

func (builder) WaitBuild(context.Context, *buildv1.WaitBuildRequest) (*buildv1.WaitBuildResponse, error) {
	return &buildv1.WaitBuildResponse{Build: passed}, nil
}

// releaser is the release service of the examples' daemon.
type releaser struct {
	buildv1alpha1connect.UnimplementedReleaseServiceHandler
}

func (releaser) PlanRelease(context.Context, *buildv1.PlanReleaseRequest) (*buildv1.PlanReleaseResponse, error) {
	return &buildv1.PlanReleaseResponse{Plan: &buildv1.ReleasePlan{Etag: "94d84b5ff042", Units: []*buildv1.ReleaseUnit{
		{Entity: &typev1.EntityRef{Kind: "component", Namespace: "default", Name: "greeter"}, LastVersion: "1.2.0", NextVersion: "1.3.0"},
	}}}, nil
}

func (releaser) CreateRelease(context.Context, *buildv1.CreateReleaseRequest) (*buildv1.CreateReleaseResponse, error) {
	return &buildv1.CreateReleaseResponse{Release: &buildv1.Release{Id: "rel_1", State: buildv1.Release_STATE_TAGGING}}, nil
}

func (releaser) WaitRelease(context.Context, *buildv1.WaitReleaseRequest) (*buildv1.WaitReleaseResponse, error) {
	return &buildv1.WaitReleaseResponse{Release: &buildv1.Release{Id: "rel_1", State: buildv1.Release_STATE_SUCCEEDED}}, nil
}
