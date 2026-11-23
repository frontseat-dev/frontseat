package evaluate

import (
	"sort"
	"strings"

	"github.com/frontseat-dev/frontseat/build/engine/engine/graph"
	"github.com/frontseat-dev/frontseat/build/engine/wire"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"google.golang.org/protobuf/proto"
)

// ProjectList is the evaluation's projects as GraphService lists them, in
// order, with option values that are the caller's own. It selects nothing
// and checks no selection.
func (e Evaluation) ProjectList() *buildv1.ListProjectsResponse {
	// The list's messages of each type are made in one allocation, not
	// one each: every graph request lists every project.
	requirements := 0
	for _, project := range e.Projects {
		requirements += len(project.Requires)
	}
	projects := make([]buildv1.Project, len(e.Projects))
	refs := make([]buildv1.ProjectRef, len(e.Projects)+requirements)
	required := make([]buildv1.ProjectRequirement, requirements)
	origins := make([]typev1.SourceLocation, requirements)
	next := 0
	out := &buildv1.ListProjectsResponse{Projects: make([]*buildv1.Project, 0, len(e.Projects))}
	for p, project := range e.Projects {
		ref := project.Ref()
		listed := &projects[p]
		listed.Ref, listed.Kind, listed.Name, listed.Manifest = wire.ProjectRefIn(&refs[p], ref), project.Kind, project.Name, project.Manifest
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
			origin := &origins[next]
			origin.Repository, origin.Path, origin.Line = ref.Repository, requirement.File, requirement.Line
			requires := &required[next]
			requires.Coordinate, requires.Version, requires.Origin = requirement.Coordinate.String(), requirement.Version, origin
			if by, held := e.Graph.ProvidedBy(requirement.Coordinate); held {
				requires.ProviderProject = wire.ProjectRefIn(&refs[len(e.Projects)+next], by.Ref())
			}
			listed.Requires = append(listed.Requires, requires)
			next++
		}
		// A project's options are made together, and each is named from
		// where it stands among them.
		if len(project.Options) > 0 {
			options := make([]buildv1.ProjectOption, len(project.Options))
			listed.Options = make([]*buildv1.ProjectOption, len(project.Options))
			for i, option := range project.Options {
				options[i].Name, options[i].Value = option.Name, proto.CloneOf(option.Value)
				// What sets it is said from the workspace, its repository's
				// name before the file: the location names the two apart.
				if option.Origin != "" {
					options[i].Origin = &typev1.SourceLocation{Repository: ref.Repository, Path: strings.TrimPrefix(option.Origin, ref.Repository+"/")}
				}
				listed.Options[i] = &options[i]
			}
		}
		out.Projects = append(out.Projects, listed)
	}
	sort.SliceStable(out.Projects, func(i, j int) bool {
		a, b := out.Projects[i].GetRef(), out.Projects[j].GetRef()
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
