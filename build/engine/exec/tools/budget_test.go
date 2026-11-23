package tools_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// installing answers the action that installs a tool with a tree, at
// once.
type installing struct{}

func (installing) Execute(_ context.Context, action exec.Action) (exec.Result, error) {
	if toolstest.Asks(action) {
		return toolstest.Environment(action), nil
	}
	return toolstest.Installed(action), nil
}

// pinnedGo is a repository, at root, that pins Go; a provisioner of its
// tools over the grid given; and an action of it that builds with Go.
func pinnedGo(tb testing.TB, grid exec.Executor) (string, *tools.Provisioner, exec.Action) {
	tb.Helper()
	root := tb.TempDir()
	testpins.Go(tb, filepath.Join(root, "repo"))
	p := &tools.Provisioner{Executor: grid, Platform: testgrid.Platform()}
	return root, p, exec.Action{Label: "//repo/module:compile", Root: root, WorkingDirectory: "repo/module", Arguments: []string{"go", "build", "./..."}, Platform: p.Platform}
}

// installedGo has the provisioner install the Go the repository at root
// pins.
func installedGo(t *testing.T, p *tools.Provisioner, root string) {
	t.Helper()
	if _, _, err := p.Installed(context.Background(), root, nil, "repo", "go"); err != nil {
		t.Fatal(err)
	}
}

// using gives the action its Go, as a run that has it already does, for
// as long as the benchmark loops.
func using(b *testing.B, p *tools.Provisioner, action exec.Action) {
	b.Helper()
	use := func() {
		if _, err := p.Use(context.Background(), action, "repo", []string{"go"}, false, nil); err != nil {
			b.Fatal(err)
		}
	}
	use()
	for b.Loop() {
		use()
	}
}

// BenchmarkUse is an action given its tools by a run that has them
// already: what every action of a run but the first costs.
func BenchmarkUse(b *testing.B) {
	_, p, action := pinnedGo(b, installing{})
	using(b, p, action)
}

// Measured 2026-11-06 on an Apple M-series laptop, three runs of
// `go test -run TestBudgetOfGivingAnActionItsTools -v
// ./internal/exec/tools/`: 547 to 568 ns and 11 allocations. The time
// allowed is ten times that, the allocations a tenth more.
//
// It was 2.2 to 3.2 ms and 368 allocations while every action had what
// confines it written to a directory and its repository's pins read
// again; a run asks for each once.
//
// Measured again 2026-11-09 on an Apple M4 Max, three runs of `go test
// -run '^$' -bench 'BenchmarkUse' -benchmem -count=3
// ./build/engine/exec/tools/`, once mise was asked once for the
// run what an action's tools put in its environment, and no action ran
// under mise x: 710 to 778 ns and 13 allocations, and with the Go
// plugin's environment 880 to 979 ns and 13. The two allocations more
// than in 2026-11-06 are what a run keeps of the environment it was
// given, found by a key made in one allocation, and the action's
// arguments. The first budget is unchanged; the second allows ten times
// what it took and the same allocations.
func TestBudgetOfGivingAnActionItsTools(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 6 * time.Microsecond, Allocs: 13}, BenchmarkUse)
}

func TestBudgetOfGivingAnActionItsToolsAndItsEnvironment(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 10 * time.Microsecond, Allocs: 13}, BenchmarkUseWithAnEnvironment)
}

// BenchmarkUseWithAnEnvironment is BenchmarkUse for an action with the
// environment the Go plugin gives each of its actions: what every Go
// action of a run but the first costs.
func BenchmarkUseWithAnEnvironment(b *testing.B) {
	_, p, action := pinnedGo(b, installing{})
	action.Environment = map[string]string{"CGO_ENABLED": "0", "GOPROXY": "off", "GOSUMDB": "off", "MISE_GO_SET_GOBIN": "false"}
	using(b, p, action)
}
