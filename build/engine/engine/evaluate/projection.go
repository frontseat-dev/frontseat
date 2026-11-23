package evaluate

import (
	"slices"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	"github.com/frontseat-dev/frontseat/common/record"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// projectionKind names what a request takes of the evaluation that is
// kept: its projects as the graph API lists them, the plan of what was
// asked for, or the evaluation's facts.
type projectionKind uint8

const (
	projectProjects projectionKind = iota + 1
	projectPlan
	projectFacts
)

// Work belongs to one operation, never to a retained evaluation. Counters are
// successful query answers actually obtained during validation/reconstruction.
// A full retained-value hit has no query replays. Equal changed answers can
// perform query work while retaining the derived evaluation.
type Work struct {
	Asked, Again           int
	Retained, PlanRetained bool
}

// BuildPlan is the owned execution selection and observations of this request.
type BuildPlan struct {
	Needed, Asked []plan.Action
	// Publishes are the actions of the workspace that publish, whatever
	// the build was pointed at: no build runs them, and a release does.
	Publishes []plan.Action
	Entities  []record.Located
	Work      Work
}

// projectionResult is one request's own result: the work the request did,
// and the projection it asked for.
type projectionResult struct {
	work     Work
	facts    Evaluation
	projects *buildv1.ListProjectsResponse
	plan     selectedPlan
	// publishes are the request's own copies of the actions that
	// publish, of the whole evaluation.
	publishes []plan.Action
}

// selectedPlan contains the full execution payloads, asked roots and ownership
// records needed by the run. It is not merely the public planned-event summary.
type selectedPlan struct {
	Needed, Asked []plan.Action
	Entities      []record.Located
}

// plan.Of has already checked the entire graph and formed the dependency
// closure. Clone only those complete action payloads and the ownership records.
func ownSelection(needed, asked []plan.Action, entities []record.Located) selectedPlan {
	out := selectedPlan{Needed: plan.CloneActions(needed), Asked: slices.Clone(asked), Entities: slices.Clone(entities)}

	positions := make(map[model.Label]int, len(out.Needed))
	for i, a := range out.Needed {
		positions[a.Label] = i
	}
	for i, a := range out.Asked {
		out.Asked[i] = out.Needed[positions[a.Label]]
	}
	return out
}
