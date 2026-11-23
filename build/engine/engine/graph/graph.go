package graph

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/frontseat-dev/frontseat/build/engine/model"
	"github.com/frontseat-dev/frontseat/common/record"
)

// Graph is a workspace's projects and what provides what each requires.
type Graph struct {
	projects []Project
	// provider is the project that provides a coordinate, by its place
	// among projects.
	provider map[model.Coordinate]int
}

// Resolve finds, for everything the projects require, the project that
// provides it. What no project provides is left to come from outside the
// workspace. A coordinate two projects provide is a model.Mistake that names
// both: nothing could say which was meant.
func Resolve(projects []Project) (*Graph, error) {
	g := &Graph{projects: projects, provider: map[model.Coordinate]int{}}
	seen := make(map[model.ProjectRef]bool, len(projects))
	for i, project := range projects {
		ref := project.Ref()
		if seen[ref] {
			return nil, &model.Mistake{Message: fmt.Sprintf("project %s was declared twice", ref)}
		}
		seen[ref] = true
		for _, provide := range project.Provides {
			if first, taken := g.provider[provide.Coordinate]; taken {
				return nil, &model.Mistake{Message: fmt.Sprintf("%s is provided by %s and %s", provide.Coordinate, projects[first].Ref(), project.Ref())}
			}
			g.provider[provide.Coordinate] = i
		}
	}
	return g, nil
}

// ProvidedBy returns the project of the workspace that provides a
// coordinate.
func (g *Graph) ProvidedBy(coordinate model.Coordinate) (Project, bool) {
	i, held := g.provider[coordinate]
	if !held {
		return Project{}, false
	}
	return g.projects[i], true
}

// Handover is something a project is handed: what a project it requires
// provides.
type Handover struct {
	// From is the project that hands it.
	From Project
	// Provide is what it hands.
	Provide Provide
}

// Handed returns what a project is handed: what each project that
// provides something it requires hands, and what the projects those
// require hand in turn, each once, the nearest first. A
// tool that is given a project it requires needs what that one requires
// too.
func (g *Graph) Handed(project Project) []Handover {
	seen := map[model.Coordinate]bool{}
	var out []Handover
	var follow func(of Project)
	follow = func(of Project) {
		var next []Project
		for _, requirement := range of.Requires {
			if seen[requirement.Coordinate] {
				continue
			}
			seen[requirement.Coordinate] = true
			from, held := g.ProvidedBy(requirement.Coordinate)
			if !held || from.Ref() == project.Ref() {
				continue
			}
			for _, provide := range from.Provides {
				if provide.Coordinate == requirement.Coordinate {
					out = append(out, Handover{From: from, Provide: provide})
				}
			}
			next = append(next, from)
		}
		for _, from := range next {
			follow(from)
		}
	}
	follow(project)
	return out
}

// Owner returns the entity that owns what is at a path of the workspace:
// the one whose directory it is in, the deepest where one entity's
// directory is in another's.
func Owner(entities []record.Located, at string) (record.Located, bool) {
	at = path.Clean(at)
	if !filepath.IsLocal(filepath.FromSlash(at)) {
		return record.Located{}, false
	}
	var owner record.Located
	found := false
	depth := -1
	for _, entity := range entities {
		root := path.Clean(entity.Path())
		if root != "." && at != root && !strings.HasPrefix(at, root+"/") {
			continue
		}
		length := len(root)
		if root == "." {
			length = 0
		}
		if !found || length > depth {
			owner, found = entity, true
			depth = length
		}
	}
	return owner, found
}
