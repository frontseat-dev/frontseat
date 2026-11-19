package evaluate

import (
	"context"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"github.com/frontseat-dev/frontseat/server/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"slices"
	"sync"
)

// retainedEngine keeps one evaluation of a workspace, and the plan last
// asked of it, between requests. The evaluator owns every check of what a
// repository says; the engine adds admission, one request at a time, reuse
// for as long as what the evaluation read is the same, and what is kept
// after a request that failed. Each request is given its own copy of what
// it asked for; what is kept is never handed out.
type retainedEngine struct {
	plugins   []*pluginhost.Plugin
	answers   *Answers
	retention RetentionLimits
	once      sync.Once
	gate      chan struct{}

	// The evaluation that is kept, what it was asked with, and what it
	// read, by which it is held to a later source.
	valid    bool
	profile  pluginv1.Profile
	supplied []record.Located
	result   retainedResult
	trace    *retainedTrace

	// The plan last made of that evaluation, and what it was asked with.
	planValid bool
	targets   []string
	only      []string
	asked     []plan.Action

	// What the engine counts of its own work: evaluations run, plans
	// made, plugin questions asked again to hold an evaluation to its
	// source, and times everything kept was dropped at a limit. Nothing
	// is decided by them; Session.Stats and the engine's tests read them.
	evaluations, plans, reasked, evictions int
}

// operationSelected admits one request, and gives it its own copy of the
// projection it asked for (kind), of the evaluation of src. Only the
// projections named here see what is kept.
func (p *retainedEngine) operationSelected(ctx context.Context, src Source, profile pluginv1.Profile, targets, only []string, kind projectionKind, supplied []record.Located) (projectionResult, error) {
	release, err := p.admit(ctx)
	if err != nil {
		return projectionResult{}, err
	}
	defer release()
	var own func(retainedResult, []plan.Action, *projectionResult)
	switch kind {
	case projectProjects:
		own = func(kept retainedResult, _ []plan.Action, out *projectionResult) {
			out.projects = kept.evaluation.ProjectList()
		}
	case projectFacts:
		own = func(kept retainedResult, _ []plan.Action, out *projectionResult) {
			out.facts = kept.evaluation.Clone()
			// Graph callers receive semantic facts, never stale per-run counters.
			out.facts.Asked, out.facts.Again = 0, 0
		}
	case projectPlan:
		own = func(kept retainedResult, asked []plan.Action, out *projectionResult) {
			out.plan = ownSelection(kept.actions, asked, kept.evaluation.Entities)
		}
	default:
		return projectionResult{}, fmt.Errorf("evaluate: projection %d is not one the engine makes", kind)
	}
	return p.projected(ctx, src, profile, targets, only, supplied, kind == projectPlan, own)
}

// projected is one admitted request: it holds what is kept to src, or
// evaluates again, plans where the request is for a plan (planned), and
// has own take the request's copy of what is then kept. The caller holds
// the engine's gate.
func (p *retainedEngine) projected(ctx context.Context, src Source, profile pluginv1.Profile, targets, only []string, supplied []record.Located, planned bool, own func(kept retainedResult, asked []plan.Action, out *projectionResult)) (out projectionResult, err error) {
	// What is kept is changed only by what succeeded, below: a request
	// that fails takes nothing with it. An evaluation that failed leaves
	// the one before, which the next request holds to its source again; a
	// plan that failed, or a caller that gave up, leaves the evaluation.
	defer p.enforceRetention()
	parent := ctx
	ctx, span := telemetry.Start(ctx, "evaluate")
	ended := false
	endEvaluation := func(failure error) {
		if ended {
			return
		}
		ended = true
		span.SetAttributes(attribute.Int("asked", out.work.Asked), attribute.Int("again", out.work.Again), attribute.Bool("retained", out.work.Retained))
		if reads, ok := src.(interface{ Hashed() int }); ok {
			span.SetAttributes(attribute.Int("read", reads.Hashed()))
		}
		telemetry.End(span, failure)
	}
	defer func() { endEvaluation(err) }()
	var planning trace.Span
	defer func() {
		if planning != nil {
			planning.SetAttributes(attribute.Bool("retained", out.work.PlanRetained))
			telemetry.End(planning, err)
		}
	}()
	same := false
	if p.valid && p.profile == profile && slices.Equal(p.supplied, supplied) {
		same, err = p.trace.same(ctx, src, p.answers, &p.reasked, &out.work)
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			// The ordinary evaluator owns diagnostic order and context.
			same = false
		}
	}
	out.work.Retained = same
	if same {
		p.stillAsked()
	}
	result := p.result
	if !same {
		observed := &trackedSource{Source: src, recorders: map[string]*pluginhost.Recorder{}}
		queries := &retainedAsking{Answers: p.answers, observed: observed}
		result = retainedResult{}
		p.evaluations++
		result.evaluation, err = evaluate(ctx, observed, p.plugins, queries, profile, slices.Clone(supplied))
		measured := queries.measured()
		out.work.Asked += measured.Asked
		out.work.Again += measured.Again
		if err != nil {
			return out, err
		}
		// Whole, and of this source: kept from here, whatever becomes of
		// the plan asked of it. No plan of the evaluation before is its.
		p.trace = &retainedTrace{structural: observed.reads(), queries: queries.queries, rested: queries.rested, repositories: queries.repositories}
		p.valid, p.profile, p.supplied, p.result = true, profile, slices.Clone(supplied), result
		p.planValid, p.asked, p.targets, p.only = false, nil, nil, nil
	}
	if planned {
		endEvaluation(nil)
		_, planning = telemetry.Start(parent, "plan")
		out.work.PlanRetained = p.planValid && slices.Equal(targets, p.targets) && slices.Equal(only, p.only)
	}
	if planned && !out.work.PlanRetained {
		// A plan that cannot be made, as for a target that names nothing,
		// leaves the plan there was: it is still the evaluation's.
		actions, asked, err := plan.Of(result.evaluation.Actions, result.evaluation.Entities, targets, only)
		if err != nil {
			return out, err
		}
		p.plans++
		result.actions = actions
		p.result, p.asked = result, asked
		p.planValid, p.targets, p.only = true, slices.Clone(targets), slices.Clone(only)
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	own(result, p.asked, &out)
	if err = ctx.Err(); err != nil {
		return projectionResult{work: out.work}, err
	}
	return out, nil
}

// A query borrows the cache's immutable wire answer. Protobuf objects supplied
// to normalization are never retained. Types reconstruct only a changed query.
type retainedQuery struct {
	plugin                    *pluginhost.Plugin
	repository, method        string
	request                   []byte
	requestType, responseType protoreflect.MessageType
	value                     *answer
	guarded                   bool
	generated                 []plan.GeneratedSource
}

// retainedTrace is what an evaluation read, in order: what it read by itself,
// and each plugin question whose answer it was made from.
type retainedTrace struct {
	structural *trackedReads
	queries    []retainedQuery
	// rested are the other answers the evaluation was given.
	rested []*answer
	// repositories are those whose configuration the evaluation was told.
	repositories []string
}

// retainedAsking explicitly separates eligible query reads from structural reads.
// Unsupported views, reconciliation and producer analysis remain structural.
type retainedAsking struct {
	*Answers
	observed *trackedSource
	mu       sync.Mutex
	queries  []retainedQuery
	work     Work
	// rested are the answers given that the evaluation is not held to its
	// source by: it rests on them all the same.
	rested []*answer
	// repositories are those whose configuration the evaluation was told.
	repositories []string
}

func (a *retainedAsking) ask(ctx context.Context, plugin *pluginhost.Plugin, view repositoryView, method string, request, response proto.Message) (bool, error) {
	again, _, err := a.askObserved(ctx, plugin, view, method, request, response)
	return again, err
}

func (a *retainedAsking) askObserved(ctx context.Context, plugin *pluginhost.Plugin, view repositoryView, method string, request, response proto.Message) (bool, []pluginhost.Read, error) {
	eligible := method == "Arrange" || method == "Analyze" && !plugin.Description.GetGeneratesSources()
	q := retainedQuery{plugin: plugin, repository: view.repository, method: method}
	queryView := view
	switch src := view.source.(type) {
	case *trackedSource:
		eligible = eligible && src == a.observed
		queryView.source = src.Source
	case generatedView:
		eligible = eligible && src.Source == a.observed
		q.guarded, q.generated = true, slices.Clone(src.generated)
		queryView.source = generatedView{Source: a.observed.Source, generated: src.generated}
	default:
		eligible = false
	}
	if !eligible {
		again, _, value, err := a.Answers.answerObserved(ctx, plugin, view, method, request, response)
		if err != nil {
			return false, nil, err
		}
		a.count(again)
		a.rest(value)
		return again, value.reads, nil
	}
	again, wire, value, err := a.Answers.answerObserved(ctx, plugin, queryView, method, request, response)
	if err != nil {
		return false, nil, err
	}
	q.request, q.value = wire, value
	q.requestType, q.responseType = request.ProtoReflect().Type(), response.ProtoReflect().Type()
	a.mu.Lock()
	a.queries = append(a.queries, q)
	if again {
		a.work.Again++
	} else {
		a.work.Asked++
	}
	a.mu.Unlock()
	return again, value.reads, nil
}

func (a *retainedAsking) count(again bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if again {
		a.work.Again++
	} else {
		a.work.Asked++
	}
}
func (a *retainedAsking) measured() Work {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.work
}
func (a *retainedAsking) askAlone(ctx context.Context, plugin *pluginhost.Plugin, method string, request, response proto.Message) (bool, error) {
	again, value, err := a.Answers.answerAlone(ctx, plugin, method, request, response)
	if err == nil {
		a.count(again)
		a.rest(value)
	}
	return again, err
}

// said is Answers.said, and notes the repository whose configuration the
// evaluation was told.
func (a *retainedAsking) said(ctx context.Context, view repositoryView) (*config.Repository, map[string]string, error) {
	said, pinned, err := a.Answers.said(ctx, view)
	if err == nil {
		a.mu.Lock()
		a.repositories = append(a.repositories, view.repository)
		a.mu.Unlock()
	}
	return said, pinned, err
}

// rest notes an answer the evaluation was given: one it rests on, whether
// or not it is one the evaluation is held to its source by.
func (a *retainedAsking) rest(value *answer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rested = append(a.rested, value)
}

func (t *retainedTrace) same(ctx context.Context, src Source, answers *Answers, reasked *int, work *Work) (bool, error) {
	same, err := t.structural.same(ctx, src)
	if err != nil || !same {
		return false, err
	}
	for _, i := range t.checkOrder(src) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		q := &t.queries[i]
		view := repositoryView{source: src, repository: q.repository}
		if q.guarded {
			view.source = generatedView{Source: src, generated: q.generated}
		}
		same, err := pluginhost.Same(ctx, view, q.value.reads)
		if err != nil {
			return false, err
		}
		if same {
			continue
		}
		request, before := q.requestType.New().Interface(), q.responseType.New().Interface()
		if err := proto.Unmarshal(q.request, request); err != nil {
			return false, err
		}
		if err := proto.Unmarshal(q.value.response, before); err != nil {
			return false, err
		}
		after := q.responseType.New().Interface()
		*reasked = *reasked + 1
		again, _, value, err := answers.answerObserved(ctx, q.plugin, view, q.method, request, after)
		if err == nil {
			if again {
				work.Again++
			} else {
				work.Asked++
			}
		}
		if err != nil || !proto.Equal(before, after) {
			// Full evaluation owns contextual errors and every domain check.
			return false, ctx.Err()
		}
		// Equal replies can read different files. Keep the new read set too.
		q.value = value
	}
	return true, nil
}

// Metadata hints only order checks. They never suppress Same, and never move
// Arrange across Analyze: only contiguous Analyze groups are reordered. Sources
// without hints retain capture order. The run boundary delegates any validation
// failure to ordinary evaluation, which owns externally returned diagnostics.
func (t *retainedTrace) checkOrder(src Source) []int {
	hints, hinted := src.(interface{ MayHaveChanged(string, string) bool })
	priority := make([]bool, len(t.queries))
	if hinted {
		for i, q := range t.queries {
			if q.method != "Analyze" {
				continue
			}
			for _, read := range q.value.reads {
				if read.Import == "read" && read.Found && hints.MayHaveChanged(q.repository, read.Argument) {
					priority[i] = true
					break
				}
			}
		}
	}
	order := make([]int, 0, len(t.queries))
	for start := 0; start < len(t.queries); {
		if t.queries[start].method != "Analyze" {
			order = append(order, start)
			start++
			continue
		}
		end := start + 1
		for end < len(t.queries) && t.queries[end].method == "Analyze" {
			end++
		}
		for i := start; i < end; i++ {
			if priority[i] {
				order = append(order, i)
			}
		}
		for i := start; i < end; i++ {
			if !priority[i] {
				order = append(order, i)
			}
		}
		start = end
	}
	return order
}
