package evaluate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func projectionUnknown(message proto.Message) {
	wire := protowire.AppendTag(nil, 999, protowire.BytesType)
	wire = protowire.AppendBytes(wire, []byte("private unknown payload"))
	message.ProtoReflect().SetUnknown(wire)
}

func TestRetainedProjectionProjectFieldsAreOwned(t *testing.T) {
	mutations := map[string]func(*buildv1.Project){
		"identity":        func(p *buildv1.Project) { p.Ref.Root = "caller" },
		"owner":           func(p *buildv1.Project) { p.Owner.Name = "caller" },
		"provides":        func(p *buildv1.Project) { p.Provides[0] = "caller" },
		"requirement":     func(p *buildv1.Project) { p.Requires[0].Origin.Path = "caller" },
		"provider":        func(p *buildv1.Project) { p.Requires[0].ProviderProject.Root = "caller" },
		"option identity": func(p *buildv1.Project) { p.Options[0].Name = "caller" },
		"nested option":   func(p *buildv1.Project) { p.Options[0].Value.GetStringListValue().Values[0] = "caller" },
		"option unknown":  func(p *buildv1.Project) { p.Options[0].Value.ProtoReflect().GetUnknown()[3] ^= 1 },
		"nested unknown":  func(p *buildv1.Project) { p.Options[0].Value.GetStringListValue().ProtoReflect().GetUnknown()[3] ^= 1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			e := ownedFixture(t)
			e.Projects[0].Requires[0].Coordinate = e.Projects[0].Provides[0].Coordinate
			var err error
			e.Graph, err = graph.Resolve(e.Projects)
			if err != nil {
				t.Fatal(err)
			}
			option := e.Projects[0].Options[0].Value
			projectionUnknown(option)
			projectionUnknown(option.GetStringListValue())
			originalOption := proto.CloneOf(option)
			got := e.ProjectList()
			want := proto.CloneOf(got)
			if !proto.Equal(got.Projects[0].Options[0].Value, originalOption) {
				t.Fatal("projection dropped option fields or unknowns")
			}
			mutate(got.Projects[0])
			if proto.Equal(got, want) {
				t.Fatal("mutation control did not alter the returned project")
			}
			if !proto.Equal(e.ProjectList(), want) || !proto.Equal(option, originalOption) {
				t.Fatal("project mutation reached retained values")
			}
		})
	}
}

func TestRetainedProjectionAskedSharesOnlyItsOwnedNeededPayload(t *testing.T) {
	e := ownedFixture(t)
	projectionUnknown(e.Actions[0].Declared)
	projectionUnknown(e.Actions[0].Context)
	projectionUnknown(e.Actions[0].Context.Options["tags"].GetStringListValue())
	beforeAction, beforeContext := proto.CloneOf(e.Actions[0].Declared), proto.CloneOf(e.Actions[0].Context)
	before := ownedFingerprint(t, e)
	first := ownSelection(e.Actions, e.Actions, e.Entities)
	second := ownSelection(e.Actions, e.Actions, e.Entities)
	if first.Asked[0].Declared != first.Needed[0].Declared || first.Asked[0].Context != first.Needed[0].Context {
		t.Fatal("asked roots did not reuse their owned needed payload")
	}
	if first.Needed[0].Declared == e.Actions[0].Declared || first.Needed[0].Declared == second.Needed[0].Declared {
		t.Fatal("payload is shared across owners")
	}
	if !proto.Equal(first.Asked[0].Declared, beforeAction) || !proto.Equal(first.Asked[0].Context, beforeContext) {
		t.Fatal("selection dropped protobuf unknowns")
	}
	first.Asked[0].Declared.ProtoReflect().GetUnknown()[3] ^= 1
	first.Asked[0].Context.ProtoReflect().GetUnknown()[3] ^= 1
	first.Asked[0].Context.Options["tags"].GetStringListValue().ProtoReflect().GetUnknown()[3] ^= 1
	first.Asked[0].Sources[0].Generated[0][1][0] = "caller"
	first.Asked[0].InStep.Fixed[0] = 'X'
	first.Asked[0].Beside.Environment["URL"] = "caller"
	first.Entities[0].Root = "caller"
	if proto.Equal(first.Needed[0].Declared, beforeAction) || proto.Equal(first.Needed[0].Context, beforeContext) || first.Needed[0].Beside.Environment["URL"] != "caller" {
		t.Fatal("mutation control did not reach this result's shared payload")
	}
	if !proto.Equal(e.Actions[0].Declared, beforeAction) || !proto.Equal(e.Actions[0].Context, beforeContext) || !bytes.Equal(before, ownedFingerprint(t, e)) {
		t.Fatal("asked mutation reached retained input")
	}
	if !proto.Equal(second.Needed[0].Declared, beforeAction) || !proto.Equal(second.Needed[0].Context, beforeContext) || second.Needed[0].Sources[0].Generated[0][1][0] == "caller" || second.Entities[0].Root == "caller" {
		t.Fatal("asked mutation reached another return")
	}
}

// The reference uses the ordinary evaluator and planner. Unknown protobufs
// have separate proto.Equal controls above; JSON here compares real fixtures.
func projectionWant(t *testing.T, e Evaluation, kind projectionKind, targets []string) (testProjection, error) {
	t.Helper()
	var out testProjection
	if kind == projectProjects {
		out.projects = e.ProjectList()
		return out, nil
	}
	needed, asked, err := plan.Of(e.Actions, e.Entities, targets, nil)
	if err != nil {
		return out, err
	}
	if kind == projectPlan {
		out.plan = selectedPlan{needed, asked, e.Entities}
	} else {
		out.full = retainedResult{evaluation: e, actions: needed}
	}
	return out, nil
}

// projectionWanted is what projectionWant gives where the ordinary
// evaluator and planner refuse nothing; a refusal fails the test.
func projectionWanted(t *testing.T, e Evaluation, kind projectionKind, targets []string) testProjection {
	t.Helper()
	want, err := projectionWant(t, e, kind, targets)
	if err != nil {
		t.Fatal(err)
	}
	return want
}

// projectionMatches asks the retained engine for one operation over src
// and fails the test unless it succeeds with what is wanted.
func projectionMatches(t *testing.T, p *retainedEngine, src Source, profile typev1.Profile, targets []string, kind projectionKind, want testProjection) {
	t.Helper()
	got, err := p.operation(t.Context(), src, profile, targets, kind)
	if err != nil {
		t.Fatal(err)
	}
	projectionEqual(t, got, want, kind)
}

func projectionEqual(t *testing.T, got, want testProjection, kind projectionKind) {
	t.Helper()
	switch kind {
	case projectProjects:
		if !proto.Equal(got.projects, want.projects) {
			t.Fatal("project projection differs from ordinary evaluation")
		}
	case projectPlan:
		if !bytes.Equal(projectionBytes(t, got.plan), projectionBytes(t, want.plan)) {
			t.Fatal("selection differs from ordinary planning")
		}
	case projectFull:
		a, b := retainedSemantics(t, got.full.evaluation, want.full.evaluation)
		if !bytes.Equal(a, b) || !bytes.Equal(projectionBytes(t, got.full.actions), projectionBytes(t, want.full.actions)) {
			t.Fatal("full result differs from ordinary evaluation")
		}
	}
}

func TestRetainedProjectionOperationTargetProfileAndRefusalTransitions(t *testing.T) {
	root := retainedFixture(t, 3)
	p, files := newRetainedEngine(), &source.Keeper{}
	dev, prod := typev1.Profile_PROFILE_DEVELOPMENT, typev1.Profile_PROFILE_PRODUCTION
	steps := []struct {
		kind    projectionKind
		profile typev1.Profile
		targets []string
	}{
		{projectProjects, dev, nil}, {projectPlan, dev, []string{"m0000/"}},
		{projectFull, dev, []string{"m0001/"}}, {projectPlan, dev, []string{"m0001/"}},
		{projectProjects, dev, nil}, {projectPlan, dev, []string{"missing/"}},
		{projectProjects, dev, nil}, {projectPlan, dev, nil},
		{projectPlan, prod, []string{"m0002/"}}, {projectFull, prod, nil},
		{projectProjects, dev, nil}, {projectPlan, dev, []string{"m0002/", "m0002/"}},
	}
	for i, step := range steps {
		fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), step.profile)
		want, wantErr := projectionWant(t, fresh, step.kind, step.targets)
		plans := p.plans
		got, gotErr := p.operation(t.Context(), retainedTake(t, files, root), step.profile, step.targets, step.kind)
		if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
			t.Fatalf("step %d: retained=%v ordinary=%v", i, gotErr, wantErr)
		}
		if wantErr != nil {
			// Asked again, the same is refused the same way: nothing kept
			// answers what was refused.
			if _, again := p.operation(t.Context(), retainedTake(t, files, root), step.profile, step.targets, step.kind); fmt.Sprint(again) != fmt.Sprint(wantErr) {
				t.Fatalf("step %d: a refusal was answered from what was kept: %v", i, again)
			}
			continue
		}
		projectionEqual(t, got, want, step.kind)
		if step.kind == projectProjects && p.plans != plans {
			t.Fatal("project operation planned a build")
		}
	}
	before := p.trace
	if _, err := p.operation(t.Context(), retainedTake(t, files, root), dev, nil, projectionKind(255)); err == nil {
		t.Fatal("unknown operation accepted")
	}
	if !p.valid || before != p.trace {
		t.Fatal("unknown operation damaged previous valid state")
	}
	retainedOracle(t, p, files, root, dev, nil)
}

func TestRetainedProjectionSelectionCannotHideInvalidOutsideProvider(t *testing.T) {
	for _, kind := range []projectionKind{projectProjects, projectPlan} {
		root := retainedFixture(t, 3)
		p, files := newRetainedEngine(), &source.Keeper{}
		profile, targets := typev1.Profile_PROFILE_DEVELOPMENT, []string{"m0000/"}
		if _, err := p.operation(t.Context(), retainedTake(t, files, root), profile, targets, kind); err != nil {
			t.Fatal(err)
		}
		retainedWrite(t, root, "m0002/go.mod", "module example.com/m0000\n\ngo 1.27\n")
		plugins := retainedPlugins(t)
		_, wantErr := Evaluate(t.Context(), retainedTake(t, &source.Keeper{}, root), plugins, &Answers{}, profile, nil)
		_, gotErr := p.operation(t.Context(), retainedTake(t, files, root), profile, targets, kind)
		if wantErr == nil || gotErr == nil || wantErr.Error() != gotErr.Error() {
			t.Fatalf("outside provider: retained=%v ordinary=%v", gotErr, wantErr)
		}
		if _, again := p.operation(t.Context(), retainedTake(t, files, root), profile, targets, kind); again == nil || again.Error() != wantErr.Error() {
			t.Fatalf("an invalid provider was answered from what was kept: %v", again)
		}
		retainedWrite(t, root, "m0002/go.mod", "module example.com/m0002\n\ngo 1.27\n")
		fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
		want := projectionWanted(t, fresh, kind, targets)
		projectionMatches(t, p, retainedTake(t, files, root), profile, targets, kind, want)
	}
}

func TestRetainedProjectionConcurrentOperationsAndEarlierMutation(t *testing.T) {
	root := retainedFixture(t, 3)
	p, files := newRetainedEngine(), &source.Keeper{}
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
	firstProjects, err := p.projectList(t.Context(), retainedTake(t, files, root), profile)
	if err != nil {
		t.Fatal(err)
	}
	firstPlan, err := p.selectedPlan(t.Context(), retainedTake(t, files, root), profile, []string{"m0001/"})
	if err != nil {
		t.Fatal(err)
	}
	started, stop := make(chan struct{}), make(chan struct{})
	var mutator sync.WaitGroup
	mutator.Go(func() {
		firstProjects.Projects[0].Ref.Root = "caller"
		firstPlan.Asked[0].Declared.Name = "caller"
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
			}
			firstProjects.Projects[0].Options[0].Name = "caller"
			firstPlan.Asked[0].Declared.Name = "caller"
			for _, a := range firstPlan.Needed {
				if a.Context != nil {
					a.Context.Tools["go"] = "caller"
				}
			}
		}
	})
	defer func() { close(stop); mutator.Wait() }()
	<-started
	var calls sync.WaitGroup
	type returned struct {
		got, want testProjection
		kind      projectionKind
		err       error
	}
	results := make(chan returned, 12)
	for i := range 12 {
		kind, targets := projectionKind(i%3), []string{fmt.Sprintf("m%04d/", i%3)}
		want := projectionWanted(t, fresh, kind, targets)
		snapshot := retainedTake(t, files, root)
		calls.Go(func() {
			got, err := p.operation(t.Context(), snapshot, profile, targets, kind)
			results <- returned{got, want, kind, err}
		})
	}
	calls.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		projectionEqual(t, result.got, result.want, result.kind)
	}
}

func TestRetainedProjectionCancellationAcrossOperations(t *testing.T) {
	for _, activeKind := range []projectionKind{projectProjects, projectPlan} {
		for _, cancelActive := range []bool{false, true} {
			t.Run(fmt.Sprintf("kind%d/activeCancelled%v", activeKind, cancelActive), func(t *testing.T) {
				root, p, files := warmRetained(t, 2)
				profile := typev1.Profile_PROFILE_DEVELOPMENT
				gate := &retainedDigestGate{Source: retainedTake(t, files, root), started: make(chan struct{}), release: make(chan struct{})}
				activeCtx, activeCancel := context.WithCancel(t.Context())
				queuedCtx, queuedCancel := context.WithCancel(t.Context())
				var wait sync.WaitGroup
				defer func() { activeCancel(); queuedCancel(); wait.Wait() }()
				type response struct {
					result testProjection
					err    error
				}
				active, queued := make(chan response, 1), make(chan response, 1)
				wait.Go(func() {
					r, err := p.operation(activeCtx, gate, profile, []string{"m0000/"}, activeKind)
					active <- response{r, err}
				})
				select {
				case <-gate.started:
				case <-time.After(5 * time.Second):
					t.Fatal("active validation did not block")
				}
				queuedKind := projectPlan
				if activeKind == projectPlan {
					queuedKind = projectProjects
				}
				queuedSource := retainedTake(t, files, root)
				wait.Go(func() {
					r, err := p.operation(queuedCtx, queuedSource, profile, []string{"m0001/"}, queuedKind)
					queued <- response{r, err}
				})
				if cancelActive {
					activeCancel()
				} else {
					queuedCancel()
					close(gate.release)
				}
				var first, second response
				select {
				case first = <-active:
				case <-time.After(5 * time.Second):
					t.Fatal("active cancellation/release hung")
				}
				select {
				case second = <-queued:
				case <-time.After(5 * time.Second):
					t.Fatal("queued operation hung")
				}
				cancelled, allowed := second, first
				allowedKind, targets := activeKind, []string{"m0000/"}
				if cancelActive {
					cancelled, allowed = first, second
					allowedKind, targets = queuedKind, []string{"m0001/"}
				}
				if !errors.Is(cancelled.err, context.Canceled) || allowed.err != nil {
					t.Fatalf("cancelled=%v allowed=%v", cancelled.err, allowed.err)
				}
				if cancelled.result.projects != nil || cancelled.result.plan.Needed != nil || cancelled.result.full.actions != nil {
					t.Fatal("cancelled operation returned a partial result")
				}
				fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
				want := projectionWanted(t, fresh, allowedKind, targets)
				projectionEqual(t, allowed.result, want, allowedKind)
				if !p.valid || p.trace == nil {
					t.Fatal("cancelled peer poisoned successful operation")
				}
			})
		}
	}
}
