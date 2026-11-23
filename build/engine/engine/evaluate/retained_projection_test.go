package evaluate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/catalog/catalogtest"
	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"google.golang.org/protobuf/proto"
)

func projectionFresh(t testing.TB, src Source, profile typev1.Profile) Evaluation {
	t.Helper()
	plugins := retainedPlugins(t)
	e, err := Evaluate(t.Context(), src, plugins, &Answers{}, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func projectionBytes(t testing.TB, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// freshPlan is the plan the ordinary planner makes of a fresh evaluation
// for targets; a refusal fails the test.
func freshPlan(t testing.TB, fresh Evaluation, targets []string) selectedPlan {
	t.Helper()
	needed, asked, err := plan.Of(fresh.Actions, fresh.Entities, targets, nil)
	if err != nil {
		t.Fatal(err)
	}
	return selectedPlan{needed, asked, fresh.Entities}
}

// planMatches is the retained engine's plan of targets in root, over a
// snapshot taken through files; the test fails, with message, unless it is
// the plan a fresh evaluation makes.
func planMatches(t testing.TB, p *retainedEngine, files *source.Keeper, root string, profile typev1.Profile, targets []string, message string) selectedPlan {
	t.Helper()
	got, err := p.selectedPlan(t.Context(), retainedTake(t, files, root), profile, targets)
	if err != nil {
		t.Fatal(err)
	}
	fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
	if !bytes.Equal(projectionBytes(t, got), projectionBytes(t, freshPlan(t, fresh, targets))) {
		t.Fatal(message)
	}
	return got
}

// writeCycle has the component named name, at m0000, declare two custom
// actions that each read the other's output: a cycle no plan can be
// made of, though the graph can still be read.
func writeCycle(t testing.TB, root, name string) {
	t.Helper()
	retainedWrite(t, root, "m0000/catalog-info.yaml", catalogtest.Component(name, catalogtest.Cycle))
}

func TestRetainedProjectionCopiesOnlyTheRequestedResult(t *testing.T) {
	for _, op := range []string{"projects", "plan"} {
		t.Run(op, func(t *testing.T) {
			root := retainedFixture(t, 20)
			p, files := newRetainedEngine(), &source.Keeper{}
			profile := typev1.Profile_PROFILE_DEVELOPMENT
			// The legacy full-result control proves the materialization counter.
			retainedOracle(t, p, files, root, profile, nil)
			if *fullCopies(p) != 1 {
				t.Fatal("full-copy control did not count")
			}
			*fullCopies(p) = 0
			fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
			if op == "projects" {
				got, err := p.projectList(t.Context(), retainedTake(t, files, root), profile)
				if err != nil {
					t.Fatal(err)
				}
				if !proto.Equal(got, fresh.ProjectList()) {
					t.Fatal("project fields differ from ordinary mapping")
				}
			} else {
				targets := []string{"m0001/"}
				got, err := p.selectedPlan(t.Context(), retainedTake(t, files, root), profile, targets)
				if err != nil {
					t.Fatal(err)
				}
				want := freshPlan(t, fresh, targets)
				if !bytes.Equal(projectionBytes(t, got), projectionBytes(t, want)) {
					t.Fatal("selected execution payload differs from ordinary plan")
				}
				if len(got.Needed) >= len(fresh.Actions) {
					t.Fatal("selection control did not narrow actions")
				}
			}
			if *fullCopies(p) != 0 {
				t.Fatal("operation materialized the complete evaluation")
			}
		})
	}
}

func TestRetainedProjectionProjectQueryDoesNotPlanABuild(t *testing.T) {
	root := retainedFixture(t, 2)
	writeCycle(t, root, "alpha")
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
	_, _, wantErr := plan.Of(fresh.Actions, fresh.Entities, []string{"m0001/"}, nil)
	if wantErr == nil {
		t.Fatal("cycle control must prevent a build")
	}
	p, files := newRetainedEngine(), &source.Keeper{}
	got, err := p.projectList(t.Context(), retainedTake(t, files, root), profile)
	if err != nil {
		t.Fatalf("read-only project query incorrectly planned: %v", err)
	}
	if !proto.Equal(got, fresh.ProjectList()) || p.plans != 0 {
		t.Fatal("project query changed normal graph semantics")
	}
	_, err = p.selectedPlan(t.Context(), retainedTake(t, files, root), profile, []string{"m0001/"})
	if fmt.Sprint(err) != fmt.Sprint(wantErr) {
		t.Fatalf("build refusal differs: %v vs %v", err, wantErr)
	}
}

func TestRetainedProjectionSwitchesOperationsWithoutStalePlans(t *testing.T) {
	root := retainedFixture(t, 20)
	retainedWrite(t, root, "frontseat.yaml", "plugins:\n  go: {test-shards: 2}\n  backstage: {}\n")
	p, files := newRetainedEngine(), &source.Keeper{}
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	query := func() {
		t.Helper()
		got, err := p.projectList(t.Context(), retainedTake(t, files, root), profile)
		if err != nil {
			t.Fatal(err)
		}
		fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
		if !proto.Equal(got, fresh.ProjectList()) {
			t.Fatal("stale project projection")
		}
	}
	build := func(targets ...string) {
		t.Helper()
		planMatches(t, p, files, root, profile, targets, "stale execution payload or asked targets")
	}
	query()
	if p.plans != 0 {
		t.Fatal("query made a plan")
	}
	build("m0001/")
	query()
	build("m0001/")
	if p.evaluations != 1 || p.plans != 1 {
		t.Fatal("query discarded valid plan")
	}
	build("m0011/")
	build()
	build("m0001/")
	if p.evaluations != 1 || p.plans != 4 {
		t.Fatal("target changes did not only replan")
	}
	writeTestEdit(t, root, 1, 0)
	query()
	if p.evaluations != 2 || p.plans != 4 || p.planValid {
		t.Fatal("changed query left an old plan valid")
	}
	build("m0001/")
	if p.plans != 5 {
		t.Fatal("plan did not refresh after query")
	}
	profile = typev1.Profile_PROFILE_PRODUCTION
	query()
	build("m0001/")
	if p.evaluations != 3 || p.plans != 6 || *fullCopies(p) != 0 {
		t.Fatal("profile switch or projection counters differ")
	}
}
