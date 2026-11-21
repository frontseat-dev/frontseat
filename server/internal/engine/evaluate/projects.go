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
	out := &buildv1.ListProjectsResponse{Projects: make([]*buildv1.Project, 0, len(e.Projects))}
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
		// Each list is made at the length it ends at: a project is listed
		// on every graph request, and a list grown as it is filled is made
		// again each time it grows.
		if len(project.Provides) > 0 {
			listed.Provides = make([]string, 0, len(project.Provides))
		}
		if len(project.Requires) > 0 {
			listed.Requires = make([]*buildv1.ProjectRequirement, 0, len(project.Requires))
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
		// A project's options are made together, and each is named from
		// where it stands among them.
		if len(project.Options) > 0 {
			options := make([]buildv1.ProjectOption, len(project.Options))
			listed.Options = make([]*buildv1.ProjectOption, len(project.Options))
			for i, option := range project.Options {
				options[i].Name, options[i].Value, options[i].Origin = option.Name, proto.CloneOf(option.Value), option.Origin
				listed.Options[i] = &options[i]
			}
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
