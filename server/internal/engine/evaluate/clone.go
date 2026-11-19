package evaluate

import (
	"maps"
	"slices"

	"github.com/frontseat-dev/frontseat/server/internal/engine/catalog"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
)

// Clone gives a caller its own evaluation: nothing that can be changed
// through the copy is shared with the original. A session answers a graph
// request with a clone of the evaluation it keeps (Session.Facts).
func (e Evaluation) Clone() Evaluation {
	out := e
	out.Actions = plan.CloneActions(e.Actions)
	out.Projects = graph.CloneProjects(e.Projects)
	out.Generated = plan.CloneGenerated(e.Generated)
	out.producerReads = slices.Clone(e.producerReads)
	for i := range out.producerReads {
		out.producerReads[i].reads = slices.Clone(out.producerReads[i].reads)
	}
	out.Entities = slices.Clone(e.Entities)
	out.Catalog = e.Catalog.Clone()
	out.descriptors = catalog.CloneDescriptors(e.descriptors)
	out.Graph = e.Graph.Clone()
	out.Releases = slices.Clone(e.Releases)
	for i := range out.Releases {
		out.Releases[i].Settings = maps.Clone(out.Releases[i].Settings)
	}
	out.Listed = maps.Clone(e.Listed)
	for key, values := range out.Listed {
		out.Listed[key] = slices.Clone(values)
	}
	out.DependsOn = maps.Clone(e.DependsOn)
	for key, values := range out.DependsOn {
		out.DependsOn[key] = slices.Clone(values)
	}
	out.shared = maps.Clone(e.shared)
	return out
}
