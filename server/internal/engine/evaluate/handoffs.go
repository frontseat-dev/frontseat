package evaluate

import (
	"fmt"
	"path"

	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/wire"
)

type placedHandoff struct {
	handoff graph.Handover
	into    string
}

// selectedHandoffs follows the existing project graph from only the requirements
// the action reads. Shared transitive handoffs are included once; requesting the
// same coordinate with conflicting placements is refused before arrangement.
func selectedHandoffs(resolved *graph.Graph, project graph.Project, inputs []*pluginv1.DependencyInput) ([]placedHandoff, error) {
	// Resolve a placement's union in one traversal. Reading several coordinates
	// must not traverse their shared transitive closure once per coordinate.
	type selection struct {
		into     string
		all      bool
		requires []graph.Requirement
	}
	var groups []selection
	byPlacement := map[string]int{}
	var required map[model.Coordinate]graph.Requirement
	for _, input := range inputs {
		index, exists := byPlacement[input.GetInto()]
		if !exists {
			index = len(groups)
			byPlacement[input.GetInto()] = index
			groups = append(groups, selection{into: input.GetInto()})
		}
		switch choice := input.GetSelection().(type) {
		case *pluginv1.DependencyInput_All:
			if choice.All == nil {
				return nil, fmt.Errorf("a dependency input selects all with nothing")
			}
			groups[index].all = true
		case *pluginv1.DependencyInput_Coordinate:
			if required == nil {
				required = make(map[model.Coordinate]graph.Requirement, len(project.Requires))
				for _, requirement := range project.Requires {
					required[requirement.Coordinate] = requirement
				}
			}
			coordinate := wire.ReadCoordinate(choice.Coordinate)
			requirement, exists := required[coordinate]
			if !exists {
				return nil, fmt.Errorf("dependency %s is not a declared requirement of %s", coordinate, project.Ref())
			}
			groups[index].requires = append(groups[index].requires, requirement)
		default:
			return nil, fmt.Errorf("a dependency input names no selection")
		}
	}
	var out []placedHandoff
	seen := map[model.Coordinate]string{}
	for _, group := range groups {
		selected := project
		if !group.all {
			selected.Requires = group.requires
		}
		for _, handed := range resolved.Handed(selected) {
			coordinate := handed.Provide.Coordinate
			if into, duplicate := seen[coordinate]; duplicate {
				if into != group.into {
					first, err := handoffPlacement(project, handed, into)
					if err != nil {
						return nil, err
					}
					second, err := handoffPlacement(project, handed, group.into)
					if err != nil {
						return nil, err
					}
					if first != second {
						return nil, fmt.Errorf("dependency %s is placed at both %q and %q", coordinate, into, group.into)
					}
				}
				continue
			}
			seen[coordinate] = group.into
			out = append(out, placedHandoff{handoff: handed, into: group.into})
		}
	}
	return out, nil
}

// handoffPlacement is shared by staging selection and duplicate comparison:
// different templates can name the same directory. Artifact-only handoffs may
// be co-located; source handoffs retain the existing same-root refusal.
func handoffPlacement(project graph.Project, handed graph.Handover, into string) (string, error) {
	if into == "{root}" && len(handed.Provide.Sources) == 0 && handed.From.Repository == project.Repository && path.Clean(handed.From.Root) == path.Clean(project.Root) {
		into = "."
	}
	placed, err := pluginhost.Placed(into, handed.Provide.Coordinate.Name, handed.From.Repository, project.Repository, project.Root, handed.From.Root)
	if err != nil {
		return "", err
	}
	return path.Clean(placed), nil
}
