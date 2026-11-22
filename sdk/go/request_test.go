package frontseat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// daemon is a daemon's build and run services that keep what they were
// asked to create, and refuse a profile by name.
type daemon struct {
	buildv1alpha1connect.UnimplementedBuildServiceHandler
	buildv1alpha1connect.UnimplementedRunServiceHandler
	builds []*buildv1.CreateBuildRequest
	runs   []*buildv1.CreateRunRequest
}

func (d *daemon) CreateBuild(_ context.Context, r *buildv1.CreateBuildRequest) (*buildv1.CreateBuildResponse, error) {
	if r.GetProfile() == typev1.Profile(7) {
		refused := connect.NewError(connect.CodeInvalidArgument, "no one defined the profile")
		info, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: "PROFILE_UNKNOWN", Domain: "build.frontseat.dev"})
		if err != nil {
			return nil, err
		}
		return nil, refused.WithDetail(info)
	}
	d.builds = append(d.builds, r)
	return &buildv1.CreateBuildResponse{Build: &buildv1.Build{Id: "build_1"}}, nil
}

func (d *daemon) CreateRun(_ context.Context, r *buildv1.CreateRunRequest) (*buildv1.CreateRunResponse, error) {
	d.runs = append(d.runs, r)
	return &buildv1.CreateRunResponse{Run: &buildv1.Run{Id: "run_1", BuildId: "build_1"}}, nil
}

func servedDaemon(t *testing.T, d interface {
	buildv1alpha1connect.BuildServiceHandler
	buildv1alpha1connect.RunServiceHandler
}) *Workspace {
	t.Helper()
	api := connect.NewServer()
	buildv1alpha1connect.RegisterBuildServiceHandler(api, d)
	buildv1alpha1connect.RegisterRunServiceHandler(api, d)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return client(t, srv.URL).Here()
}

// A build and a run are asked for with the request id the caller gives,
// so that one sent again is the one the first call made; one given none
// is asked for with none.
func TestABuildAndARunAreAskedForWithTheCallersRequestID(t *testing.T) {
	d := &daemon{}
	here := servedDaemon(t, d)
	ctx := context.Background()
	if _, err := here.Build(ctx, BuildRequest{RequestID: "once"}); err != nil {
		t.Fatal(err)
	}
	if _, err := here.Run(ctx, RunRequest{Target: "//a:go.run/a", RequestID: "once"}); err != nil {
		t.Fatal(err)
	}
	if _, err := here.Build(ctx, BuildRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := d.builds[0].GetRequestId(); got != "once" {
		t.Errorf("a build was asked for with the request id %q, want once", got)
	}
	if got := d.runs[0].GetRequestId(); got != "once" {
		t.Errorf("a run was asked for with the request id %q, want once", got)
	}
	if got := d.builds[1].GetRequestId(); got != "" {
		t.Errorf("a build given no request id was asked for with %q", got)
	}
}

// What a server refuses by name is read by its reason and its domain;
// an error that carries none reads as none.
func TestARefusalIsReadByItsReason(t *testing.T) {
	here := servedDaemon(t, &daemon{})
	_, err := here.Build(context.Background(), BuildRequest{Profile: typev1.Profile(7)})
	if reason, domain := Reason(err); reason != "PROFILE_UNKNOWN" || domain != "build.frontseat.dev" {
		t.Errorf("the refusal %v is read as %q in %q, want PROFILE_UNKNOWN in build.frontseat.dev", err, reason, domain)
	}
	for _, none := range []error{nil, context.Canceled, connect.NewError(connect.CodeInternal, "no reason")} {
		if reason, domain := Reason(none); reason != "" || domain != "" {
			t.Errorf("%v is read as %q in %q, want nothing", none, reason, domain)
		}
	}
}
