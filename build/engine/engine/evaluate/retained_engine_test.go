package evaluate

import (
	"context"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"math"
	"slices"
	"sync"
	"weak"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// What the engine's tests ask of it beside what a session asks: an engine
// by itself, with no limit on what it keeps, each projection by its kind,
// and the whole of what is kept, owned, which no request of a session takes.

// newRetainedEngine is an engine over the test plugins that keeps without
// limit, as a session's does where nothing goes over its limits.
func newRetainedEngine() *retainedEngine {
	plugins, _ := retainedLoaded()
	return &retainedEngine{plugins: plugins, answers: &Answers{}, retention: RetentionLimits{Records: math.MaxInt64, WireBytes: math.MaxInt64}}
}

// projectFull is the whole evaluation and its plan.
const projectFull projectionKind = 0

// testProjection is what a test's request returned: a projection's result
// as the engine gives it, or the whole result where it asked for
// projectFull.
type testProjection struct {
	work     Work
	facts    Evaluation
	full     retainedResult
	projects *buildv1.ListProjectsResponse
	plan     selectedPlan
}

func testProjectionOf(out projectionResult, full retainedResult) testProjection {
	return testProjection{work: out.work, facts: out.facts, full: full, projects: out.projects, plan: out.plan}
}

// wholeCopies counts, for each engine, the whole results taken of it.
var wholeCopies = struct {
	sync.Mutex
	of map[weak.Pointer[retainedEngine]]*int
}{of: map[weak.Pointer[retainedEngine]]*int{}}

func fullCopies(p *retainedEngine) *int {
	wholeCopies.Lock()
	defer wholeCopies.Unlock()
	key := weak.Make(p)
	if wholeCopies.of[key] == nil {
		wholeCopies.of[key] = new(int)
	}
	return wholeCopies.of[key]
}

// operation is one request for a projection, as a session makes it, with
// no entities supplied and nothing left out of the plan. Plugins that did
// not load fail it.
func (p *retainedEngine) operation(ctx context.Context, src Source, profile typev1.Profile, targets []string, kind projectionKind) (testProjection, error) {
	if _, err := retainedLoaded(); err != nil {
		return testProjection{}, err
	}
	if kind != projectFull {
		out, err := p.operationSelected(ctx, src, profile, targets, nil, kind, nil)
		return testProjectionOf(out, retainedResult{}), err
	}
	release, err := p.admit(ctx)
	if err != nil {
		return testProjection{}, err
	}
	defer release()
	var full retainedResult
	copies := fullCopies(p)
	out, err := p.projected(ctx, src, profile, targets, nil, nil, true, func(kept retainedResult, _ []plan.Action, _ *projectionResult) {
		*copies++
		full = kept.owned()
	})
	if err != nil {
		full = retainedResult{}
	}
	return testProjectionOf(out, full), err
}

func (p *retainedEngine) run(ctx context.Context, src Source, profile typev1.Profile, targets []string) (retainedResult, error) {
	out, err := p.operation(ctx, src, profile, targets, projectFull)
	return out.full, err
}

func (p *retainedEngine) projectList(ctx context.Context, src Source, profile typev1.Profile) (*buildv1.ListProjectsResponse, error) {
	out, err := p.operation(ctx, src, profile, nil, projectProjects)
	return out.projects, err
}

func (p *retainedEngine) selectedPlan(ctx context.Context, src Source, profile typev1.Profile, targets []string) (selectedPlan, error) {
	out, err := p.operation(ctx, src, profile, targets, projectPlan)
	return out.plan, err
}

// owned is the whole of what is kept, as a caller's own: the plan's
// actions are the copies the evaluation holds, not copies of their own.
func (r retainedResult) owned() retainedResult {
	out := retainedResult{evaluation: r.evaluation.Clone(), actions: slices.Clone(r.actions)}
	positions := make(map[model.Label]int, len(out.evaluation.Actions))
	for i, a := range out.evaluation.Actions {
		positions[a.Label] = i
	}
	for i, a := range out.actions {
		out.actions[i] = out.evaluation.Actions[positions[a.Label]]
	}
	return out
}
