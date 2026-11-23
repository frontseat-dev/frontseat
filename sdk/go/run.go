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
	// Profile is the profile the program is built with:
	// PROFILE_DEVELOPMENT, the default where it is unspecified, or
	// PROFILE_PRODUCTION.
	Profile typev1.Profile
	// RequestID makes the request safe to send again, as
	// BuildRequest.RequestID does a build's: one sent again with the
	// same id is the run the first made.
	RequestID string
}

// Run is one run of a program, by its id: the program is built, then
// run where the workspace's daemon is.
type Run struct {
	id string
	// buildID is the id of the build of what the program is made of,
	// where it is known without asking: empty for a run by its id alone.
	buildID string
	w       *Workspace
}

// ID is the run's id, as the daemon gave it: "run_" and more.
func (r *Run) ID() string { return r.id }

// BuildID is the id of the build of what the program is made of: as
// Run was answered, or, for a run by its id alone, as the daemon
// describes it.
func (r *Run) BuildID(ctx context.Context) (string, error) {
	if r.buildID != "" {
		return r.buildID, nil
	}
	said, err := r.Describe(ctx)
	if err != nil {
		return "", err
	}
	return said.GetBuildId(), nil
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
	run.buildID = created.GetRun().GetBuildId()
	return run, nil
}

// Runs are the workspace's runs of programs, newest first: each that
// has not ended, and the newest of those that have, asked for a page at
// a time as they are read.
func (w *Workspace) Runs(ctx context.Context) iter.Seq2[*buildv1.Run, error] {
	return paged(func(token string) ([]*buildv1.Run, string, error) {
		api, err := w.runs(ctx)
		if err != nil {
			return nil, "", err
		}
		page, err := api.ListRuns(ctx, &buildv1.ListRunsRequest{PageToken: token})
		return page.GetRuns(), page.GetNextPageToken(), err
	})
}

// RunByID is the workspace's run of an id. Nothing is asked of the
// daemon until a call is made.
func (w *Workspace) RunByID(id string) *Run { return &Run{id: id, w: w} }

// Describe returns the run as it is now.
func (r *Run) Describe(ctx context.Context) (*buildv1.Run, error) {
	api, err := r.w.runs(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.DescribeRun(ctx, &buildv1.DescribeRunRequest{RunId: r.id})
	return said.GetRun(), err
}

// Wait returns the run once it has ended.
func (r *Run) Wait(ctx context.Context) (*buildv1.Run, error) {
	api, err := r.w.runs(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.WaitRun(ctx, &buildv1.WaitRunRequest{RunId: r.id})
	return said.GetRun(), err
}

// Cancel cancels the run: its build is stopped, or its program ended. It
// returns at once, with the run as it is then; Wait returns once it has
// ended.
func (r *Run) Cancel(ctx context.Context) (*buildv1.Run, error) {
	api, err := r.w.runs(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.CancelRun(ctx, &buildv1.CancelRunRequest{RunId: r.id})
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
		return api.StreamRunLogs(ctx, &buildv1.StreamRunLogsRequest{RunId: r.id})
	}, (*buildv1.StreamRunLogsResponse).GetLines)
}

func (w *Workspace) runs(ctx context.Context) (buildv1alpha1connect.RunServiceClient, error) {
	daemon, err := w.at()
	if err != nil {
		return nil, err
	}
	return buildv1alpha1connect.NewRunServiceClient(daemon), nil
}
