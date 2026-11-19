package graph

import (
	"maps"
	"slices"

	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"google.golang.org/protobuf/proto"
)

// CloneProjects copies every mutable value reachable from project records.
func CloneProjects(projects []Project) []Project {
	out := slices.Clone(projects)
	for i := range out {
		p := &out[i]
		p.Options = slices.Clone(p.Options)
		for j := range p.Options {
			p.Options[j].Value = proto.CloneOf(p.Options[j].Value)
		}
		p.Provides = slices.Clone(p.Provides)
		for j := range p.Provides {
			p.Provides[j].Artifacts = slices.Clone(p.Provides[j].Artifacts)
			p.Provides[j].Sources = slices.Clone(p.Provides[j].Sources)
			p.Provides[j].Generated = pattern.CloneSelections(p.Provides[j].Generated)
		}
		p.Requires = slices.Clone(p.Requires)
	}
	return out
}

// Clone gives a caller its own graph and the projects returned by its accessors.
func (g *Graph) Clone() *Graph {
	if g == nil {
		return nil
	}
	return &Graph{projects: CloneProjects(g.projects), provider: maps.Clone(g.provider)}
}
