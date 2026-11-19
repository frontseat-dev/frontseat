package frontseat

import (
	"context"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
)

// Release is one release of a workspace, by its id: what moves is
// built for production, then tagged.
type Release struct {
	// ID is the release's id, as the daemon gave it: "rel_" and more.
	ID string
	// Build is its production build; nil for a release that builds
	// nothing.
	Build *Build
	w     *Workspace
}

// PlanRelease says what releasing entities would do now, each named as
// a person types one (greet, system:kiosk), and changes nothing. The
// plan's etag is what Release takes to release that plan and no other.
func (w *Workspace) PlanRelease(ctx context.Context, entities ...string) (*buildv1.ReleasePlan, error) {
	api, err := w.releases(ctx)
	if err != nil {
		return nil, err
	}
	named, err := refs(entities)
	if err != nil {
		return nil, err
	}
	planned, err := api.PlanRelease(ctx, &buildv1.PlanReleaseRequest{Entities: named})
	return planned.GetPlan(), err
}

// Release releases entities, as the plan an etag names says
// (PlanRelease), and returns the release at once; it is refused where
// the workspace has moved since the plan was made. The release goes on
// whether or not it is followed.
func (w *Workspace) Release(ctx context.Context, etag string, entities ...string) (*Release, error) {
	api, err := w.releases(ctx)
	if err != nil {
		return nil, err
	}
	named, err := refs(entities)
	if err != nil {
		return nil, err
	}
	created, err := api.CreateRelease(ctx, &buildv1.CreateReleaseRequest{Entities: named, Etag: etag})
	if err != nil {
		return nil, err
	}
	release := w.ReleaseByID(created.GetRelease().GetId())
	if id := created.GetRelease().GetBuildId(); id != "" {
		release.Build = w.BuildByID(id)
	}
	return release, nil
}

// ReleaseByID is the workspace's release of an id. Nothing is asked of
// the daemon until a call is made; its Build is unset.
func (w *Workspace) ReleaseByID(id string) *Release { return &Release{ID: id, w: w} }

// Describe returns the release as it is now.
func (r *Release) Describe(ctx context.Context) (*buildv1.Release, error) {
	api, err := r.w.releases(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.DescribeRelease(ctx, &buildv1.DescribeReleaseRequest{ReleaseId: r.ID})
	return said.GetRelease(), err
}

// Wait returns the release once it has ended: tagged, or failed.
func (r *Release) Wait(ctx context.Context) (*buildv1.Release, error) {
	api, err := r.w.releases(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.WaitRelease(ctx, &buildv1.WaitReleaseRequest{ReleaseId: r.ID})
	return said.GetRelease(), err
}

func (w *Workspace) releases(ctx context.Context) (buildv1alpha1connect.ReleaseServiceClient, error) {
	daemon, err := w.at(ctx)
	if err != nil {
		return nil, err
	}
	return buildv1alpha1connect.NewReleaseServiceClient(daemon), nil
}
