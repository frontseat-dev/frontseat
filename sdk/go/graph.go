package frontseat

import (
	"context"
	"iter"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
)

// Entities are the workspace's entities, each with the repository and
// the directories it governs and what it depends on, in the order of
// their references, asked for a page at a time as they are read. Reading
// them runs nothing.
func (w *Workspace) Entities(ctx context.Context) iter.Seq2[*buildv1.Entity, error] {
	return paged(func(token string) ([]*buildv1.Entity, string, error) {
		api, err := w.graph(ctx)
		if err != nil {
			return nil, "", err
		}
		page, err := api.ListEntities(ctx, &buildv1.ListEntitiesRequest{PageToken: token})
		return page.GetEntities(), page.GetNextPageToken(), err
	})
}

// Projects are what the plugins found in the workspace to build, each
// with the entity that owns it, what it provides and what it requires,
// and where each requirement is said, asked for a page at a time as they
// are read. Reading them runs nothing.
func (w *Workspace) Projects(ctx context.Context) iter.Seq2[*buildv1.Project, error] {
	return paged(func(token string) ([]*buildv1.Project, string, error) {
		api, err := w.graph(ctx)
		if err != nil {
			return nil, "", err
		}
		page, err := api.ListProjects(ctx, &buildv1.ListProjectsRequest{PageToken: token})
		return page.GetProjects(), page.GetNextPageToken(), err
	})
}

func (w *Workspace) graph(ctx context.Context) (buildv1alpha1connect.GraphServiceClient, error) {
	daemon, err := w.at(ctx)
	if err != nil {
		return nil, err
	}
	return buildv1alpha1connect.NewGraphServiceClient(daemon), nil
}
