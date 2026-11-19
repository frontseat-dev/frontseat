package graph

import (
	"fmt"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"testing"
	"time"
)

// BenchmarkProjectDependencies resolves one thousand projects and follows a
// chain containing all of them. Adjacent plugins share directories, so the
// benchmark also proves that identity does not collapse those dependencies.
func BenchmarkProjectDependencies(b *testing.B) {
	projects := make([]Project, 1000)
	for i := range projects {
		name := fmt.Sprintf("module-%04d", i)
		projects[i] = module(fmt.Sprintf("pair-%04d", i/2), name)
		projects[i].Plugin = []string{"one", "two"}[i%2]
		if i < len(projects)-1 {
			projects[i].Requires = module("", "", fmt.Sprintf("module-%04d", i+1)).Requires
		}
	}
	for b.Loop() {
		resolved, err := Resolve(projects)
		if err != nil {
			b.Fatal(err)
		}
		if handed := resolved.Handed(projects[0]); len(handed) != len(projects)-1 {
			b.Fatalf("handed %d dependencies; want %d", len(handed), len(projects)-1)
		}
	}
}

// Measured 2026-11-07 with `mise exec -- go test -run '^$' -bench
// '^BenchmarkProjectDependencies$' -benchmem -count=3
// ./server/internal/engine/graph`: 452–511 us and 1,059 allocations
// for 1,000 projects, including colocated plugins and 999 transitive handovers.
func TestBudgetOfProjectDependencies(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 4 * time.Millisecond, Allocs: 1100}, BenchmarkProjectDependencies)
}
