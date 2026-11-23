package evaluate

import (
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/record"
	"sort"
)

// dependsOn returns what each entity depends on, sorted as references
// are written: the entities that own what its projects require and what
// its actions need. Nobody writes this; it is what the source says.
func dependsOn(g *graph.Graph, projects []graph.Project, entities []record.Located, actions []plan.Action) map[entity.Ref][]entity.Ref {
	on := map[entity.Ref]map[entity.Ref]bool{}
	depend := func(from, to string) {
		a, owned := graph.Owner(entities, from)
		b, held := graph.Owner(entities, to)
		if !owned || !held || a.Ref == b.Ref {
			return
		}
		if on[a.Ref] == nil {
			on[a.Ref] = map[entity.Ref]bool{}
		}
		on[a.Ref][b.Ref] = true
	}
	for _, project := range projects {
		for _, requirement := range project.Requires {
			if provider, held := g.ProvidedBy(requirement.Coordinate); held {
				depend(project.Path(), provider.Path())
			}
		}
	}
	shared := map[string]bool{}
	for _, action := range actions {
		if action.IsShared() {
			shared[action.Label.String()] = true
		}
	}
	for _, action := range actions {
		if action.IsShared() {
			continue
		}
		for _, need := range action.Needs {
			// A shared fetch is no entity's: needing it depends on none.
			if shared[need.String()] {
				continue
			}
			depend(action.Label.Path, need.Path)
		}
	}
	out := map[entity.Ref][]entity.Ref{}
	for ref, set := range on {
		for other := range set {
			out[ref] = append(out[ref], other)
		}
		sort.Slice(out[ref], func(i, j int) bool { return out[ref][i].String() < out[ref][j].String() })
	}
	return out
}
