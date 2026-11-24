package build

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"google.golang.org/grpc/codes"

	"github.com/frontseat-dev/frontseat/build/engine/engine/evaluate"
	"github.com/frontseat-dev/frontseat/build/engine/engine/release"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// releaseService serves releases: it plans, which reads the workspace as
// it stands and changes nothing, and carries a plan out, which builds its
// units for production and tags them in the daemon's checkout. It
// publishes nothing.
type releaseService struct {
	buildv1alpha1connect.UnimplementedReleaseServiceHandler
	graph    *graphService
	releases *releases
}

var _ buildv1alpha1connect.ReleaseServiceHandler = (*releaseService)(nil)

// PlanRelease says what releasing the entities named would do now.
func (s *releaseService) PlanRelease(ctx context.Context, request *buildv1.PlanReleaseRequest) (*buildv1.PlanReleaseResponse, error) {
	evaluated, err := s.graph.evaluated(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.releases.cloned(); err != nil {
		return nil, releaseFailure(err)
	}
	plan, err := release.Plan(ctx, releasing(evaluated), strategies(s.graph.runs.plugins), s.graph.runs.workspace, request)
	if err != nil {
		return nil, releaseFailure(err)
	}
	return &buildv1.PlanReleaseResponse{Plan: plan}, nil
}

// CreateRelease carries out a plan, and returns at once.
func (s *releaseService) CreateRelease(ctx context.Context, request *buildv1.CreateReleaseRequest) (*buildv1.CreateReleaseResponse, error) {
	made, err := s.releases.create(ctx, request)
	if err != nil {
		return nil, releaseFailure(err)
	}
	return &buildv1.CreateReleaseResponse{Release: made.snapshot()}, nil
}

func (s *releaseService) find(id string) (*made, error) {
	found, err := s.releases.get(id)
	if err != nil {
		return nil, failedRPC(errors.New("no release has the id asked for"), codes.NotFound, "RELEASE_NOT_FOUND")
	}
	return found, nil
}

// DescribeRelease returns a release as it is now.
func (s *releaseService) DescribeRelease(_ context.Context, request *buildv1.DescribeReleaseRequest) (*buildv1.DescribeReleaseResponse, error) {
	found, err := s.find(request.GetReleaseId())
	if err != nil {
		return nil, err
	}
	return &buildv1.DescribeReleaseResponse{Release: found.snapshot()}, nil
}

// WaitRelease returns a release once it has ended.
func (s *releaseService) WaitRelease(ctx context.Context, request *buildv1.WaitReleaseRequest) (*buildv1.WaitReleaseResponse, error) {
	found, err := s.find(request.GetReleaseId())
	if err != nil {
		return nil, err
	}
	select {
	case <-found.ended:
		return &buildv1.WaitReleaseResponse{Release: found.snapshot()}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ListReleases returns the releases the daemon remembers, newest first,
// a page at a time.
func (s *releaseService) ListReleases(_ context.Context, request *buildv1.ListReleasesRequest) (*buildv1.ListReleasesResponse, error) {
	listed, next, err := paged(s.releases.list(), request.GetPageSize(), request.GetPageToken())
	if err != nil {
		return nil, err
	}
	return &buildv1.ListReleasesResponse{Releases: listed, NextPageToken: next}, nil
}

// CancelRelease stops a release that has not begun tagging, and returns
// at once, as CancelBuild and CancelRun do; WaitRelease ends when it has
// stopped.
func (s *releaseService) CancelRelease(_ context.Context, request *buildv1.CancelReleaseRequest) (*buildv1.CancelReleaseResponse, error) {
	found, err := s.find(request.GetReleaseId())
	if err != nil {
		return nil, err
	}
	if !found.stop() {
		return nil, failedRPC(fmt.Errorf("the release %s is tagging, pushing or publishing, and is carried through", request.GetReleaseId()), codes.FailedPrecondition, "RELEASE_NOT_CANCELLABLE")
	}
	return &buildv1.CancelReleaseResponse{Release: found.snapshot()}, nil
}

// releaseFailure is a failure to plan, as whoever asked hears of it: a
// request refused for what it asks, a strategy's answer the plugin
// contract does not let it give, a mistake in what the workspace says,
// or what went wrong.
func releaseFailure(err error) error {
	// What is refused already, as evaluating the workspace refuses a
	// mistake in it, is refused as it was.
	var already *connect.Error
	if errors.As(err, &already) {
		return err
	}
	fallback, reason := codes.Internal, ""
	var refusal *release.Refusal
	var mistake *model.Mistake
	var refused *refusedRelease
	var wrong *release.WrongAnswer
	switch {
	case errors.As(err, &refused):
		fallback, reason = refused.code, refused.reason
	case errors.Is(err, errStopped):
		fallback, reason = codes.Unavailable, "DAEMON_ENDING"
	case errors.As(err, &refusal) && refusal.Unknown:
		fallback, reason = codes.NotFound, "ENTITY_NOT_FOUND"
	case errors.As(err, &refusal) && refusal.Unserved:
		fallback, reason = codes.Unimplemented, "RELEASE_NOT_SERVED"
	case errors.As(err, &refusal), errors.As(err, &wrong):
		fallback, reason = codes.InvalidArgument, "RELEASE_INVALID"
	case errors.As(err, &mistake):
		fallback, reason = codes.FailedPrecondition, "SOURCE_MISTAKEN"
	}
	return failedRPC(err, fallback, reason)
}

// releasing is what an evaluation holds, as releasing reads it.
func releasing(evaluated evaluate.Evaluation) release.Workspace {
	w := release.Workspace{Said: evaluated.Releases, Listed: evaluated.Listed, DependsOn: evaluated.DependsOn}
	for _, known := range evaluated.Catalog.Nodes(evaluated.DependsOn) {
		entity := release.Entity{Ref: known.Ref, Repository: known.Repository, Roots: known.Roots, Manifest: known.Manifest}
		for _, relation := range known.Relations {
			// What it is part of is kept whether or not the workspace holds
			// it: a group not held is refused by name, never left out.
			if relation.Type == "partOf" {
				entity.PartOf = append(entity.PartOf, relation.Target)
			}
		}
		w.Entities = append(w.Entities, entity)
	}
	return w
}

// strategies are the release strategies of the daemon's plugins. A
// strategy reads no file: it is given a source that has none.
type strategies []*pluginhost.Plugin

func (s strategies) Serves(plugin string, branching bool, name string) bool {
	found := pluginhost.Named(s, plugin)
	if found == nil {
		return false
	}
	served := found.Description.GetRelease().GetVersionings()
	if branching {
		served = found.Description.GetRelease().GetBranchings()
	}
	for _, each := range served {
		if each == name {
			return true
		}
	}
	return false
}

func (s strategies) Branch(ctx context.Context, plugin string, request *pluginv1.BranchRequest) (*pluginv1.BranchResponse, error) {
	found := pluginhost.Named(s, plugin)
	if found == nil {
		return nil, fmt.Errorf("there is no plugin %s", plugin)
	}
	answer := new(pluginv1.BranchResponse)
	if _, err := found.Call(ctx, unread{}, "Branch", request, answer); err != nil {
		return nil, err
	}
	return answer, nil
}

func (s strategies) Version(ctx context.Context, plugin string, request *pluginv1.VersionRequest) (*pluginv1.VersionResponse, error) {
	found := pluginhost.Named(s, plugin)
	if found == nil {
		return nil, fmt.Errorf("there is no plugin %s", plugin)
	}
	answer := new(pluginv1.VersionResponse)
	if _, err := found.Call(ctx, unread{}, "Version", request, answer); err != nil {
		return nil, err
	}
	return answer, nil
}

// unread is the source a strategy is given: it holds no file, and a
// strategy that read one would not answer.
type unread struct{}

var errStrategyRead = errors.New("a release strategy reads no file")

func (unread) Read(context.Context, string) ([]byte, bool, error) { return nil, false, errStrategyRead }
func (unread) Glob(context.Context, string, []string) ([]string, error) {
	return nil, errStrategyRead
}

// released is how an entity is released, as the graph API says it; nil
// for an entity released as nothing.
func released(resolved *release.Resolved, ref entity.Ref) *buildv1.EntityRelease {
	settings, ok := resolved.Of(ref)
	if !ok {
		return nil
	}
	out := &buildv1.EntityRelease{
		Group: entity.Proto(settings.Group), Grouping: buildv1.Grouping_GROUPING_INDEPENDENT,
		Tag: settings.Tag, Initial: settings.Initial, VersioningStrategy: settings.Versioning, BranchingStrategy: settings.Branching,
		Prerelease: settings.Prerelease, PrereleaseTag: settings.PrereleaseTag,
	}
	if settings.Synced {
		out.Grouping = buildv1.Grouping_GROUPING_SYNCED
	}
	if settings.Unit != (entity.Ref{}) {
		out.Unit = entity.Proto(settings.Unit)
	}
	for _, key := range []string{"grouping", "tag", "initial", "versioning", "branching", "prerelease", "prerelease-tag"} {
		if by, set := settings.SetBy[key]; set {
			out.SetBy = append(out.SetBy, &buildv1.ReleaseSettingOrigin{Key: key, Entity: entity.Proto(by)})
		}
	}
	return out
}
