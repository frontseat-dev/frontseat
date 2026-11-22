package run

import (
	"context"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/schedule"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
)

// BenchmarkConflicting is a run that fixes, over a thousand modules of
// four Go files each with a format check that may change them: what
// finding two fixes of one file costs before any action runs.
func BenchmarkConflicting(b *testing.B) {
	root := b.TempDir()
	var actions []plan.Action
	for d := range 1000 {
		module := fmt.Sprintf("m%04d", d)
		if err := os.MkdirAll(filepath.Join(root, module), 0o755); err != nil {
			b.Fatal(err)
		}
		for _, name := range []string{"go.mod", "main.go", "word.go", "word_test.go"} {
			if err := os.WriteFile(filepath.Join(root, module, name), []byte("package main\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		actions = append(actions, plan.Action{
			Label:       model.Label{Path: module, Plugin: "go", Name: "format"},
			ProjectRoot: module,
			Sources:     []pattern.Group{{Directory: module, Patterns: []string{"**/*.go"}}},
			Declared:    &pluginv1.Action{Name: "format", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"gofmt", "-w", "."}}, Rewrites: []string{"**/*.go"}}}},
		})
	}
	snapshot, err := new(source.Keeper).Take(context.Background(), source.Tree{Root: root, Paths: map[string]string{"": "."}})
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		conflicts, err := conflicting(context.Background(), snapshot, actions)
		if err != nil || len(conflicts) != 0 {
			b.Fatal(conflicts, err)
		}
	}
}

// Measured 2026-11-08 on an Apple M4 Max, three runs of
// `go test -run '^$' -bench '^BenchmarkConflicting$' -benchmem -count=3
// ./server/internal/exec/run/`: 0.954 to 0.996 ms and 11,030
// allocations. The time allowed is ten times that, the allocations a
// tenth more.
func TestBudgetOfFindingConflictingFixes(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 10 * time.Millisecond, Allocs: 12200}, BenchmarkConflicting)
}

// BenchmarkSelected is what one action is handed of what it reads, where
// it names all that each of four hundred fetches left, as a module does
// of its lock: it runs before every action is staged, on every build.
func BenchmarkSelected(b *testing.B) {
	const fetches = 400
	action := plan.Action{Reads: make([]plan.Read, fetches)}
	handed := make([]schedule.Handed, fetches)
	for i := range fetches {
		from := model.Label{Path: "lab", Plugin: "go", Name: fmt.Sprintf("fetch/x.com/m%04d@v1", i)}
		action.Reads[i] = plan.Read{From: from, Repository: "lab"}
		handed[i] = schedule.Handed{From: from, Left: map[string]exec.Held{fmt.Sprintf(".frontseat/home/go/pkg/mod/x.com/m%04d@v1", i): {}}}
	}
	tree := source.Tree{Paths: map[string]string{"lab": "lab"}}
	for b.Loop() {
		if got := selected(tree, action, handed); len(got) != fetches {
			b.Fatalf("handed %d, want %d", len(got), fetches)
		}
	}
}

// Measured 2026-11-09, three runs of `go test -run '^$' -bench
// '^BenchmarkSelected$' -benchmem -count=3 ./build/internal/exec/run`
// in server/: 18.1 us and 3 allocations for four hundred reads.
// When each thing handed was compared with every read, an independent
// review measured the same shape at 344 us, and four times it at 5.5 ms.
// The time allowed is ten times what was measured.
func TestBudgetOfWhatAnActionIsHanded(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 180 * time.Microsecond, Allocs: 4}, BenchmarkSelected)
}
