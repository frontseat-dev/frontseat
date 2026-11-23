package frontseat

import (
	"context"
	"iter"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// BuildRequest is a build to start.
type BuildRequest struct {
	// Targets are what to build, each pointed at as a person points at
	// it: a name is an entity (gateway), a path is all beneath it
	// (platform/), an action is named as it is printed
	// (//platform/gateway:go.test). None is the whole workspace.
	Targets []string
	// Only keeps the actions of these names, such as lint; none keeps
	// every action.
	Only []string
	// Profile is the profile to build with: PROFILE_DEVELOPMENT, the
	// default where it is unspecified, or PROFILE_PRODUCTION, as a
	// release builds.
	Profile typev1.Profile
	// Fix writes what is out of step as it should be, where a check would
	// fail and say so.
	Fix bool
	// Tests keeps the actions that run tests, as the plugins that
	// declared them say, and what they need. A build of targets that
	// hold none ends EXIT_CODE_NO_TESTS_FOUND.
	Tests bool
	// RequestID makes the request safe to send again: one sent again
	// with the same id, where the first was lost on the way back, is
	// the build the first made, not another. At most 128 bytes; empty,
	// every request is a build of its own.
	RequestID string
}

// Build is one build of a workspace, by its id.
type Build struct {
	id string
	w  *Workspace
}

// ID is the build's id, as the daemon gave it: "build_" and more.
func (b *Build) ID() string { return b.id }

// Build starts a build, and returns it at once: it goes on whether or
// not it is followed.
func (w *Workspace) Build(ctx context.Context, request BuildRequest) (*Build, error) {
	api, err := w.builds(ctx)
	if err != nil {
		return nil, err
	}
	created, err := api.CreateBuild(ctx, &buildv1.CreateBuildRequest{Targets: request.Targets, Only: request.Only, Profile: request.Profile, Fix: request.Fix, Tests: request.Tests, RequestId: request.RequestID})
	if err != nil {
		return nil, err
	}
	return w.BuildByID(created.GetBuild().GetId()), nil
}

// Test starts a build of the tests of what the targets point at, as
// Build does: what the tests need is built, and every action that runs
// tests runs. It is Build with request.Tests.
func (w *Workspace) Test(ctx context.Context, request BuildRequest) (*Build, error) {
	request.Tests = true
	return w.Build(ctx, request)
}

// Builds are the workspace's builds, newest first, asked for a page at
// a time as they are read.
func (w *Workspace) Builds(ctx context.Context) iter.Seq2[*buildv1.Build, error] {
	return paged(func(token string) ([]*buildv1.Build, string, error) {
		api, err := w.builds(ctx)
		if err != nil {
			return nil, "", err
		}
		page, err := api.ListBuilds(ctx, &buildv1.ListBuildsRequest{PageToken: token})
		return page.GetBuilds(), page.GetNextPageToken(), err
	})
}

// BuildByID is the workspace's build of an id. Nothing is asked of the
// daemon until a call is made.
func (w *Workspace) BuildByID(id string) *Build { return &Build{id: id, w: w} }

// Describe returns the build as it is now.
func (b *Build) Describe(ctx context.Context) (*buildv1.Build, error) {
	api, err := b.w.builds(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.DescribeBuild(ctx, &buildv1.DescribeBuildRequest{BuildId: b.id})
	return said.GetBuild(), err
}

// Wait returns the build once it has ended.
func (b *Build) Wait(ctx context.Context) (*buildv1.Build, error) {
	api, err := b.w.builds(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.WaitBuild(ctx, &buildv1.WaitBuildRequest{BuildId: b.id})
	return said.GetBuild(), err
}

// Cancel stops the build, and returns at once, with the build as it is
// then; Wait returns once it has ended.
func (b *Build) Cancel(ctx context.Context) (*buildv1.Build, error) {
	api, err := b.w.builds(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.CancelBuild(ctx, &buildv1.CancelBuildRequest{BuildId: b.id})
	return said.GetBuild(), err
}

// Events are the build's events from a sequence number on, in order,
// each as it happens, to the build's end; the last is the one that says
// how it ended. Where the stream breaks, the error comes last.
func (b *Build) Events(ctx context.Context, from int64) iter.Seq2[*buildv1.BuildEvent, error] {
	return flattened(func() (stream[*buildv1.StreamBuildEventsResponse], error) {
		api, err := b.w.builds(ctx)
		if err != nil {
			return nil, err
		}
		return api.StreamBuildEvents(ctx, &buildv1.StreamBuildEventsRequest{BuildId: b.id, FromSequence: from})
	}, (*buildv1.StreamBuildEventsResponse).GetEvents)
}

// Actions are what each of the build's actions did, as they stand,
// asked for a page at a time as they are read.
func (b *Build) Actions(ctx context.Context) iter.Seq2[*buildv1.ActionResult, error] {
	return paged(func(token string) ([]*buildv1.ActionResult, string, error) {
		api, err := b.w.builds(ctx)
		if err != nil {
			return nil, "", err
		}
		page, err := api.ListBuildActions(ctx, &buildv1.ListBuildActionsRequest{BuildId: b.id, PageToken: token})
		return page.GetActions(), page.GetNextPageToken(), err
	})
}

// Logs are what one action of the build printed, by its label: every
// line, in order, as a run's are. Where the stream breaks, the error
// comes last.
func (b *Build) Logs(ctx context.Context, label string) iter.Seq2[*typev1.LogLine, error] {
	return flattened(func() (stream[*buildv1.StreamBuildLogsResponse], error) {
		api, err := b.w.builds(ctx)
		if err != nil {
			return nil, err
		}
		return api.StreamBuildLogs(ctx, &buildv1.StreamBuildLogsRequest{BuildId: b.id, Label: label})
	}, (*buildv1.StreamBuildLogsResponse).GetLines)
}

func (w *Workspace) builds(ctx context.Context) (buildv1alpha1connect.BuildServiceClient, error) {
	daemon, err := w.at()
	if err != nil {
		return nil, err
	}
	return buildv1alpha1connect.NewBuildServiceClient(daemon), nil
}
