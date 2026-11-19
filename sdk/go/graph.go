package frontseat

import (
	"context"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
)

// Entities are the workspace's entities, each with the repository and
// the directories it governs and what it depends on, in the order of
// their references. Reading them runs nothing.
func (w *Workspace) Entities(ctx context.Context) ([]*buildv1.Entity, error) {
	api, err := w.graph(ctx)
	if err != nil {
		return nil, err
	}
	return Listed(func(token string) ([]*buildv1.Entity, string, error) {
		page, err := api.ListEntities(ctx, &buildv1.ListEntitiesRequest{PageToken: token})
		return page.GetEntities(), page.GetNextPageToken(), err
	})
}

// Projects are what the plugins found in the workspace to build, each
// with the entity that owns it, what it provides and what it requires,
// and where each requirement is said. Reading them runs nothing.
func (w *Workspace) Projects(ctx context.Context) ([]*buildv1.Project, error) {
	api, err := w.graph(ctx)
	if err != nil {
		return nil, err
	}
	return Listed(func(token string) ([]*buildv1.Project, string, error) {
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
