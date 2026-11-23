package plan

import "github.com/frontseat-dev/frontseat/build/engine/pattern"

import (
	"fmt"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
	"testing"
	"time"
)

// BenchmarkDependencyPlacements measures the validation shared by evaluation
// and staging for an action receiving one thousand dependency directories.
func BenchmarkDependencyPlacements(b *testing.B) {
	action := Action{Handed: make([]Handed, 1000)}
	for i := range action.Handed {
		action.Handed[i].Into = fmt.Sprintf(".dependencies/module-%04d", i)
	}
	for b.Loop() {
		if err := CheckHanded(action); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkProvidedArtifacts measures one immutable installation handed to
// one thousand framework projects, including producer edges and cycle checks.
func BenchmarkProvidedArtifacts(b *testing.B) {
	producer := model.Label{Path: "install", Plugin: "prepare", Name: "install"}
	baseline := make([]Action, 1001)
	baseline[0] = Action{Label: producer}
	for i := 1; i < len(baseline); i++ {
		root := fmt.Sprintf("consumer-%04d", i)
		baseline[i] = Action{Label: model.Label{Path: root, Plugin: "framework", Name: "test"}, Handed: []Handed{{Into: ".deps", Artifacts: []Read{{From: producer, ProjectRoot: "install", Output: "modules/"}}}}}
	}
	actions := make([]Action, len(baseline))
	for b.Loop() {
		copy(actions, baseline)
		if err := BindProvided(actions); err != nil {
			b.Fatal(err)
		}
	}
}

// `mise exec -- go test -run '^$' -bench 'BenchmarkProvidedArtifacts$'
// -benchmem -count=3 ./build/engine/engine/plan` measured about
// 0.4 ms and 6044 allocations for 1000 consumers on 2026-11-14.
func TestBudgetOfProvidedArtifacts(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 5 * time.Millisecond, Allocs: 6100}, BenchmarkProvidedArtifacts)
}

// BenchmarkGeneratedBindings measures a generated module handed to one thousand
// consumers, including inferred edges, input scope and cycle validation.
func BenchmarkGeneratedBindings(b *testing.B) {
	producer := model.Label{Path: "api", Plugin: "buf", Name: "generate"}
	baseline := make([]Action, 1001)
	baseline[0] = Action{Label: producer, ProjectRoot: "api"}
	for i := 1; i < len(baseline); i++ {
		root := fmt.Sprintf("module-%04d", i)
		baseline[i] = Action{Label: model.Label{Path: root, Plugin: "go", Name: "compile"}, ProjectRoot: root, Handed: []Handed{{Root: "api/gen/go", Into: ".frontseat/go/generated", Patterns: []string{"**/*.go"}, Generated: []pattern.Selection{{{"**/*.go"}}}}}}
	}
	claims := []GeneratedSource{{Producer: producer, ProjectRoot: "api", Directory: "api/gen/go", Output: "gen/go/", Patterns: []string{"**/*.pb.go"}}}
	actions := make([]Action, len(baseline))
	for b.Loop() {
		copy(actions, baseline)
		if err := BindGenerated(actions, claims); err != nil {
			b.Fatal(err)
		}
	}
}

// Measured with `mise exec -- go test -run '^$' -bench
// '^BenchmarkGeneratedBindings$' -benchmem -count=3
// ./build/engine/engine/plan`: 435–464 us and 7044 allocations
// for 1000 consumers on 2026-11-07. The time budget permits scheduler noise;
// allocation growth remains tightly bounded.
func TestBudgetOfGeneratedBindings(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 5 * time.Millisecond, Allocs: 7100}, BenchmarkGeneratedBindings)
}

// Measured 2026-11-07 with `mise exec -- go test -run '^$' -bench
// '^BenchmarkDependencyPlacements$' -benchmem -count=3
// ./build/engine/engine/plan`: 35–37 us and five allocations.
// Measured again 2026-11-08, after placements above a project were
// allowed: 42.3–43.2 us and five allocations; the budget is unchanged.
func TestBudgetOfDependencyPlacements(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 350 * time.Microsecond, Allocs: 6}, BenchmarkDependencyPlacements)
}
