package frontseat

import (
	"context"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// Release is one release of a workspace, by its id: what moves is
// built for production, then tagged.
type Release struct {
	id string
	// buildID is its production build's id, where it is known without
	// asking; built says whether it is.
	buildID string
	built   bool
	w       *Workspace
}

// ID is the release's id, as the daemon gave it: "rel_" and more.
func (r *Release) ID() string { return r.id }

// BuildID is the id of its production build, empty for a release that
// builds nothing: as Release was answered, or, for a release by its id
// alone, as the daemon describes it.
func (r *Release) BuildID(ctx context.Context) (string, error) {
	if r.built {
		return r.buildID, nil
	}
	said, err := r.Describe(ctx)
	if err != nil {
		return "", err
	}
	return said.GetBuildId(), nil
}

// ReleaseRequest is a release to plan, or to make.
type ReleaseRequest struct {
	// Entities are what to release, each as a person types one
	// (entity.ParsePattern reads greet, system:kiosk). Each is released
	// with its unit.
	Entities []entity.Pattern
	// Etag is the etag of the plan to release (ReleasePlan.Etag, as
	// PlanRelease answered it): Release releases that plan and no other,
	// and is refused without one. PlanRelease reads none.
	Etag string
}

// PlanRelease says what releasing entities would do now, and changes
// nothing. The plan's etag is what Release takes to release that plan
// and no other.
func (w *Workspace) PlanRelease(ctx context.Context, request ReleaseRequest) (*buildv1.ReleasePlan, error) {
	api, err := w.releases(ctx)
	if err != nil {
		return nil, err
	}
	named, err := refs(request.Entities)
	if err != nil {
		return nil, err
	}
	planned, err := api.PlanRelease(ctx, &buildv1.PlanReleaseRequest{Entities: named})
	return planned.GetPlan(), err
}

// Release releases entities, as the plan the request's etag names says
// (PlanRelease), and returns the release at once; it is refused where
// the workspace has moved since the plan was made (ErrEtagMismatch).
// The release goes on whether or not it is followed.
func (w *Workspace) Release(ctx context.Context, request ReleaseRequest) (*Release, error) {
	api, err := w.releases(ctx)
	if err != nil {
		return nil, err
	}
	named, err := refs(request.Entities)
	if err != nil {
		return nil, err
	}
	created, err := api.CreateRelease(ctx, &buildv1.CreateReleaseRequest{Entities: named, Etag: request.Etag})
	if err != nil {
		return nil, err
	}
	release := w.ReleaseByID(created.GetRelease().GetId())
	release.buildID, release.built = created.GetRelease().GetBuildId(), true
	return release, nil
}

// ReleaseByID is the workspace's release of an id. Nothing is asked of
// the daemon until a call is made.
func (w *Workspace) ReleaseByID(id string) *Release { return &Release{id: id, w: w} }

// Describe returns the release as it is now.
func (r *Release) Describe(ctx context.Context) (*buildv1.Release, error) {
	api, err := r.w.releases(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.DescribeRelease(ctx, &buildv1.DescribeReleaseRequest{ReleaseId: r.id})
	return said.GetRelease(), err
}

// Wait returns the release once it has ended: its tags pushed, tagged
// and not all pushed, or failed.
func (r *Release) Wait(ctx context.Context) (*buildv1.Release, error) {
	api, err := r.w.releases(ctx)
	if err != nil {
		return nil, err
	}
	said, err := api.WaitRelease(ctx, &buildv1.WaitReleaseRequest{ReleaseId: r.id})
	return said.GetRelease(), err
}

func (w *Workspace) releases(ctx context.Context) (buildv1alpha1connect.ReleaseServiceClient, error) {
	daemon, err := w.at(ctx)
	if err != nil {
		return nil, err
	}
	return buildv1alpha1connect.NewReleaseServiceClient(daemon), nil
}
