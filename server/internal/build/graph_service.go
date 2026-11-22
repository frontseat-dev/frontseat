package build

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/evaluate"
	"github.com/frontseat-dev/frontseat/server/internal/engine/release"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
)

// graphService serves what a workspace is made of, without running any
// of it.
type graphService struct {
	buildv1alpha1connect.UnimplementedGraphServiceHandler
	runs    *runs
	session *evaluate.Session
}

var _ buildv1alpha1connect.GraphServiceHandler = (*graphService)(nil)

// evaluated returns what there is to build in the workspace as it
// stands. Nothing is run. A mistake in what a repository or an entity
// says is whoever asked's to hear of, as a precondition that failed.
func (s *graphService) evaluated(ctx context.Context) (evaluate.Evaluation, error) {
	src, entities, err := s.source(ctx)
	if err != nil {
		return evaluate.Evaluation{}, graphFailure(err)
	}
	evaluated, err := evaluate.Evaluate(ctx, src, s.runs.plugins, s.runs.answers, pluginv1.Profile_PROFILE_DEVELOPMENT, entities)
	return evaluated, graphFailure(err)
}

type graphSourceError struct {
	error
	code codes.Code
}

func (s *graphService) source(ctx context.Context) (evaluate.Source, []record.Located, error) {
	tree, err := sourceOf(s.runs.workspace)
	if err != nil {
		return nil, nil, &graphSourceError{err, codes.FailedPrecondition}
	}
	held, err := s.runs.files.Take(ctx, tree)
	if err != nil {
		return nil, nil, &graphSourceError{err, codes.Internal}
	}
	return held, s.runs.workspace.Entities(), nil
}
func graphFailure(err error) error {
	fallback, reason := codes.Internal, ""
	var source *graphSourceError
	var mistake *model.Mistake
	if errors.As(err, &source) {
		err, fallback = source.error, source.code
		if fallback == codes.FailedPrecondition {
			reason = "WORKSPACE_NOT_READY"
		}
	} else if errors.As(err, &mistake) {
		fallback, reason = codes.FailedPrecondition, "SOURCE_MISTAKEN"
	} else if errors.Is(err, evaluate.ErrSessionClosed) {
		fallback, reason = codes.Unavailable, "DAEMON_ENDING"
	}
	return failedRPC(err, fallback, reason)
}

// ListProjects returns the projects the plugins found, each with the
// options it is built with and where each is set, what it requires and
// what provides that.
func (s *graphService) ListProjects(ctx context.Context, req *buildv1.ListProjectsRequest) (*buildv1.ListProjectsResponse, error) {
	out, err := s.session.Projects(ctx)
	if err != nil {
		return nil, graphFailure(err)
	}
	// What the session answers is its own: the page is a response of
	// its own.
	projects, next, err := paged(out.GetProjects(), req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	return &buildv1.ListProjectsResponse{Projects: projects, NextPageToken: next}, nil
}

// ListEntities returns the entities the workspace holds, each with what
// it depends on: what the source says, which nobody wrote.
func (s *graphService) ListEntities(ctx context.Context, req *buildv1.ListEntitiesRequest) (*buildv1.ListEntitiesResponse, error) {
	evaluated, err := s.session.Facts(ctx)
	if err != nil {
		return nil, graphFailure(err)
	}
	out := &buildv1.ListEntitiesResponse{}
	resolved, err := release.Resolve(releasing(evaluated))
	if err != nil {
		return nil, releaseFailure(err)
	}
	for _, known := range evaluated.Catalog.Nodes(evaluated.DependsOn) {
		listed := &buildv1.Entity{
			Ref:        entity.Proto(known.Ref),
			Repository: known.Repository,
			Roots:      known.Roots,
			Manifest:   known.Manifest,
			DependsOn:  entity.Protos(evaluated.DependsOn[known.Ref]),
			Release:    released(resolved, known.Ref),
		}
		for _, relation := range known.Relations {
			listed.Relations = append(listed.Relations, &buildv1.EntityRelation{Type: relation.Type, Target: entity.Proto(relation.Target), TargetPresent: relation.TargetPresent})
		}
		out.Entities = append(out.Entities, listed)
	}
	if out.Entities, out.NextPageToken, err = paged(out.GetEntities(), req.GetPageSize(), req.GetPageToken()); err != nil {
		return nil, err
	}
	return out, nil
}
