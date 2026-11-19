package evaluate

import (
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
	"github.com/frontseat-dev/frontseat/server/internal/wire"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"google.golang.org/protobuf/encoding/protojson"
	"testing"
	"time"
)

// A runtime fixture orders the test that reads it, without becoming an input
// of the compile action. The native source dependency stays with compilation.
func TestTypedInputsSelectOnlyTheDependencyTheirActionReads(t *testing.T) {
	source := model.Coordinate{Ecosystem: "go", Name: "example.com/lib"}
	fixture := model.Coordinate{Ecosystem: "wasm", Name: "fixture"}
	transitive := model.Coordinate{Ecosystem: "data", Name: "schema"}
	consumer := graph.Project{Root: "app", Plugin: "go", Requires: []graph.Requirement{{Coordinate: source}, {Coordinate: fixture}}}
	projects := []graph.Project{
		consumer,
		{Root: "lib", Plugin: "go", Provides: []graph.Provide{{Coordinate: source, Sources: []string{"**/*.go"}}}},
		{Root: "fixture", Plugin: "cargo", Provides: []graph.Provide{{Coordinate: fixture, Artifacts: []graph.Produced{{Action: "compile", Output: "fixture.wasm"}}}}, Requires: []graph.Requirement{{Coordinate: transitive}}},
		{Root: "schema", Plugin: "data", Provides: []graph.Provide{{Coordinate: transitive, Sources: []string{"schema.json"}}}},
	}
	action := func(name, inputs string) plan.Action {
		t.Helper()
		declared := &pluginv1.Action{}
		if err := protojson.Unmarshal([]byte(`{"name":"`+name+`","inputs":`+inputs+`}`), declared); err != nil {
			t.Fatalf("typed action inputs are not accepted: %v", err)
		}
		return plan.Action{Label: model.Label{Path: "app", Plugin: "go", Name: name}, ProjectRoot: "app", Declared: declared}
	}
	out := Evaluation{Projects: projects, Actions: []plan.Action{
		{Label: model.Label{Path: "fixture", Plugin: "cargo", Name: "compile"}, ProjectRoot: "fixture", Declared: &pluginv1.Action{Name: "compile"}},
		action("compile", `[{"dependency":{"coordinate":{"ecosystem":"go","name":"example.com/lib"},"into":"deps/{name}"}}]`),
		action("test", `[{"dependency":{"coordinate":{"ecosystem":"wasm","name":"fixture"},"into":"runtime/{name}"}}]`),
		action("lint", `[{"dependency":{"all":true,"into":"all/{name}"}}]`),
	}}
	if _, err := hand(t.Context(), nil, nil, nil, pluginv1.Profile_PROFILE_DEVELOPMENT, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := plan.BindProvided(out.Actions); err != nil {
		t.Fatal(err)
	}
	compile, test, all := out.Actions[1], out.Actions[2], out.Actions[3]
	if len(compile.Handed) != 1 || compile.Handed[0].Root != "lib" || len(compile.Needs) != 0 {
		t.Fatalf("compile lost its source or read the runtime fixture: %+v", compile)
	}
	if len(test.Handed) != 2 || test.Handed[0].Root != "fixture" || test.Handed[1].Root != "schema" || test.Handed[1].Into != "runtime/schema" {
		t.Fatalf("test did not receive its fixture and transitive data alone: %+v", test.Handed)
	}
	if len(test.Needs) != 1 || test.Needs[0] != out.Actions[0].Label {
		t.Fatalf("test does not wait for the fixture producer: %v", test.Needs)
	}
	if len(all.Handed) != 3 || len(all.Needs) != 1 {
		t.Fatalf("all-requirements control lost its handoffs: %+v", all)
	}
}

func TestDependencySelectionsPreserveClosureAndRejectConflictingPlacement(t *testing.T) {
	first := model.Coordinate{Ecosystem: "fixture", Name: "first"}
	shared := model.Coordinate{Ecosystem: "fixture", Name: "shared"}
	project := graph.Project{Root: "consumer", Plugin: "fixture", Requires: []graph.Requirement{{Coordinate: first}, {Coordinate: shared}}}
	resolved, err := graph.Resolve([]graph.Project{project,
		{Root: "first", Plugin: "fixture", Provides: []graph.Provide{{Coordinate: first}}, Requires: []graph.Requirement{{Coordinate: shared}}},
		{Root: "shared", Plugin: "fixture", Provides: []graph.Provide{{Coordinate: shared}}, Requires: []graph.Requirement{{Coordinate: first}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := func(c model.Coordinate, into string) *pluginv1.DependencyInput {
		return pluginhost.DependencyInput(wire.Coordinate(c), into).GetDependency()
	}
	selections := []*pluginv1.DependencyInput{input(first, "deps/{name}"), input(shared, "deps/{name}")}
	handed, err := selectedHandoffs(resolved, project, selections)
	if err != nil || len(handed) != 2 {
		t.Fatalf("diamond/cycle handoff was not included once: %v, %v", handed, err)
	}
	selections[1] = input(shared, "other/{name}")
	if _, err := selectedHandoffs(resolved, project, selections); err == nil {
		t.Fatal("conflicting placement accepted")
	}
	unknown := input(model.Coordinate{Ecosystem: "fixture", Name: "unknown"}, "deps/{name}")
	if _, err := selectedHandoffs(resolved, project, []*pluginv1.DependencyInput{unknown}); err == nil {
		t.Fatal("undeclared dependency accepted")
	}
}

func BenchmarkTypedDependencyHandoffs(b *testing.B) {
	benchmarkTypedDependencyHandoffs(b, false)
}

// Measured on Apple M4 Max, Go 1.27.3, with:
// mise exec -- go test ./server/internal/engine/evaluate -run '^$' -bench 'Benchmark(Individual)?TypedDependencyHandoffs$' -benchmem -count=3
// Four hundred providers: all 341–394 us/57 allocations; individual selections
// 375–441 us/71 allocations. The time allowance covers machine variation; the
// allocation bounds hold both shapes, including the dimension that multiplies.
func TestBudgetOfTypedDependencyHandoffs(t *testing.T) {
	t.Run("all", func(t *testing.T) {
		testbudget.Hold(t, testbudget.Budget{Time: 3 * time.Millisecond, Allocs: 60}, BenchmarkTypedDependencyHandoffs)
	})
	t.Run("individual", func(t *testing.T) {
		testbudget.Hold(t, testbudget.Budget{Time: 3 * time.Millisecond, Allocs: 75}, BenchmarkIndividualTypedDependencyHandoffs)
	})
}

func BenchmarkIndividualTypedDependencyHandoffs(b *testing.B) {
	benchmarkTypedDependencyHandoffs(b, true)
}

func benchmarkTypedDependencyHandoffs(b *testing.B, individual bool) {
	consumer := graph.Project{Root: "consumer", Plugin: "fixture"}
	var providers []graph.Project
	for i := range 400 {
		coordinate := model.Coordinate{Ecosystem: "fixture", Name: fmt.Sprintf("dependency-%d", i)}
		consumer.Requires = append(consumer.Requires, graph.Requirement{Coordinate: coordinate})
		providers = append(providers, graph.Project{Root: coordinate.Name, Plugin: "fixture", Provides: []graph.Provide{{Coordinate: coordinate, Sources: []string{"**"}}}})
	}
	resolved, err := graph.Resolve(append(providers, consumer))
	if err != nil {
		b.Fatal(err)
	}
	inputs := []*pluginv1.DependencyInput{pluginhosttest.AllDependencies("deps/{name}").GetDependency()}
	if individual {
		inputs = nil
		for _, requirement := range consumer.Requires {
			inputs = append(inputs, pluginhost.DependencyInput(wire.Coordinate(requirement.Coordinate), "deps/{name}").GetDependency())
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := selectedHandoffs(resolved, consumer, inputs); err != nil {
			b.Fatal(err)
		}
	}
}
