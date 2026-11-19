package frontseat

import (
	"context"
	"iter"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// RunRequest is a program to build and run.
type RunRequest struct {
	// Target is the program, an action a plugin says is one, named as it
	// is printed: //services/greeter:go.run/greeter.
	Target string
	// Arguments are what the program is given.
	Arguments []string
	// Profile is the profile the program is built with: development, the
	// default, or production.
	Profile string
	// RequestID makes the request safe to send again, as
	// BuildRequest.RequestID does a build's: one sent again with the
	// same id is the run the first made.
	RequestID string
}

// Run is one run of a program, by its id: the program is built, then
// run where the workspace's daemon is.
type Run struct {
	// ID is the run's id, as the daemon gave it: "run_" and more.
	ID string
	// Build is the build of what the program is made of.
	Build *Build
	w     *Workspace
}

// Run builds a program of the workspace and runs it, and returns the
// run at once. The program is the daemon's: it goes on until it ends or
// the run is cancelled, whether or not it is followed.
func (w *Workspace) Run(ctx context.Context, request RunRequest) (*Run, error) {
	api, err := w.runs(ctx)
	if err != nil {
		return nil, err
	}
	created, err := api.CreateRun(ctx, &buildv1.CreateRunRequest{Target: request.Target, Arguments: request.Arguments, Profile: request.Profile, RequestId: request.RequestID})
	if err != nil {
		return nil, err
	}
	run := w.RunByID(created.GetRun().GetId())
	run.Build = w.BuildByID(created.GetRun().GetBuildId())
	return run, nil
}

// Runs lists the workspace's runs of programs, newest first: each that
// has not ended, and the newest of those that have.
func (w *Workspace) Runs(ctx context.Context) ([]*buildv1.Run, error) {
	api, err := w.runs(ctx)
	if err != nil {
		return nil, err
	}
	return Listed(func(token string) ([]*buildv1.Run, string, error) {
		page, err := api.ListRuns(ctx, &buildv1.ListRunsRequest{PageToken: token})
		return page.GetRuns(), page.GetNextPageToken(), err
	})
}

// RunByID is the workspace's run of an id. Nothing is asked of the
// daemon until a call is made; its Build is unset.
func (w *Workspace) RunByID(id string) *Run { return &Run{ID: id, w: w} }

// Describe returns the run as it is now.
func (r *Run) Describe(ctx context.Context) (*buildv1.Run, error) {
	api, err := r.w.runs(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.DescribeRun(ctx, &buildv1.DescribeRunRequest{RunId: r.ID})
	return said.GetRun(), err
}

// Wait returns the run once it has ended.
func (r *Run) Wait(ctx context.Context) (*buildv1.Run, error) {
	api, err := r.w.runs(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.WaitRun(ctx, &buildv1.WaitRunRequest{RunId: r.ID})
	return said.GetRun(), err
}

// Cancel cancels the run: its build is stopped, or its program ended. It
// returns the run as it is then.
func (r *Run) Cancel(ctx context.Context) (*buildv1.Run, error) {
	api, err := r.w.runs(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.CancelRun(ctx, &buildv1.CancelRunRequest{RunId: r.ID})
	return said.GetRun(), err
}

// Logs are the lines the program prints, each as it prints it, to its
// end. Where the stream breaks, the error comes last.
func (r *Run) Logs(ctx context.Context) iter.Seq2[*typev1.LogLine, error] {
	return flattened(func() (stream[*buildv1.StreamRunLogsResponse], error) {
		api, err := r.w.runs(ctx)
		if err != nil {
			return nil, err
		}
		return api.StreamRunLogs(ctx, &buildv1.StreamRunLogsRequest{RunId: r.ID})
	}, (*buildv1.StreamRunLogsResponse).GetLines)
}

func (w *Workspace) runs(ctx context.Context) (buildv1alpha1connect.RunServiceClient, error) {
	daemon, err := w.at(ctx)
	if err != nil {
		return nil, err
	}
	return buildv1alpha1connect.NewRunServiceClient(daemon), nil
}
