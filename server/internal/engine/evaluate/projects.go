package evaluate

import (
	"sort"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/wire"
	"google.golang.org/protobuf/proto"
)

// ProjectList is the evaluation's projects as GraphService lists them, in
// order, with option values that are the caller's own. It selects nothing
// and checks no selection.
func (e Evaluation) ProjectList() *buildv1.ListProjectsResponse {
	out := &buildv1.ListProjectsResponse{}
	for _, project := range e.Projects {
		ref := project.Ref()
		listed := &buildv1.Project{
			Repository: ref.Repository,
			Root:       ref.Root,
			Plugin:     ref.Plugin,
			Kind:       project.Kind,
			Name:       project.Name,
			Manifest:   project.Manifest,
		}
		if owner, owned := graph.Owner(e.Entities, project.Path()); owned {
			listed.Owner = entity.Proto(owner.Ref)
		}
		for _, provide := range project.Provides {
			listed.Provides = append(listed.Provides, provide.Coordinate.String())
		}
		for _, requirement := range project.Requires {
			required := &buildv1.ProjectRequirement{Coordinate: requirement.Coordinate.String(), Origin: requirement.Origin, Version: requirement.Version}
			if by, held := e.Graph.ProvidedBy(requirement.Coordinate); held {
				required.ProviderProject = wire.ProjectRef(by.Ref())
			}
			listed.Requires = append(listed.Requires, required)
		}
		for _, option := range project.Options {
			listed.Options = append(listed.Options, &buildv1.ProjectOption{Name: option.Name, Value: proto.CloneOf(option.Value), Origin: option.Origin})
		}
		out.Projects = append(out.Projects, listed)
	}
	sort.SliceStable(out.Projects, func(i, j int) bool {
		a, b := out.Projects[i], out.Projects[j]
		if a.GetRepository() != b.GetRepository() {
			return a.GetRepository() < b.GetRepository()
		}
		if a.GetRoot() != b.GetRoot() {
			return a.GetRoot() < b.GetRoot()
		}
		return a.GetPlugin() < b.GetPlugin()
	})
	return out
}
