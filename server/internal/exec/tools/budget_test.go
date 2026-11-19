package tools_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools/toolstest"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
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
// ./server/internal/exec/tools/`, once mise was asked once for the
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

// counting is a grid that installs at once and counts how often it is
// asked for the environment of the tools, holding each answer a moment,
// so that actions asking at once overlap, unless it is given up on.
type counting struct {
	installing
	asked, gaveUp atomic.Int32
}

func (c *counting) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if toolstest.Asks(action) {
		c.asked.Add(1)
		// As a grid does, it gives up on what is given up on.
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			c.gaveUp.Add(1)
			return exec.Result{}, ctx.Err()
		}
	}
	return c.installing.Execute(ctx, action)
}

// Actions of a run that ask at once for the environment of the same
// tools wait for one answer, and the grid is asked once; another tool
// set is asked of its own.
func TestActionsThatAskAtOnceAreGivenOneEnvironment(t *testing.T) {
	grid := &counting{}
	_, p, action := pinnedGo(t, grid)
	var joined sync.WaitGroup
	for range 50 {
		joined.Go(func() {
			if _, err := p.Use(context.Background(), action, "repo", []string{"go"}, false, nil); err != nil {
				t.Error(err)
			}
		})
	}
	joined.Wait()
	if asked := grid.asked.Load(); asked != 1 {
		t.Errorf("50 actions asking at once asked the grid %d times; want once", asked)
	}
	if _, err := p.Use(context.Background(), action, "repo", nil, false, nil); err != nil {
		t.Fatal(err)
	}
	if asked := grid.asked.Load(); asked != 2 {
		t.Errorf("an action of no tool asked the grid %d times in all; want its own once more", asked)
	}
}

// An action that gives up waiting for the environment of its tools does
// not take it from another waiting for the same: the other is answered,
// the grid is asked once, and the one that gave up is told so.
func TestAnActionThatGivesUpWaitingTakesTheEnvironmentFromNoOther(t *testing.T) {
	grid := &counting{}
	root, p, action := pinnedGo(t, grid)
	// The tool is installed first, so that both wait on the environment.
	installedGo(t, p, root)
	impatient, giveUp := context.WithCancel(context.Background())
	var joined sync.WaitGroup
	var gaveUp, waited error
	joined.Go(func() { _, gaveUp = p.Use(impatient, action, "repo", []string{"go"}, false, nil) })
	time.Sleep(10 * time.Millisecond)
	joined.Go(func() { _, waited = p.Use(context.Background(), action, "repo", []string{"go"}, false, nil) })
	time.Sleep(10 * time.Millisecond)
	giveUp()
	joined.Wait()
	if !errors.Is(gaveUp, context.Canceled) {
		t.Errorf("the action that gave up was told %v, want that it was cancelled", gaveUp)
	}
	if waited != nil {
		t.Errorf("the action that waited was told %v", waited)
	}
	if asked := grid.asked.Load(); asked != 1 {
		t.Errorf("the grid was asked %d times, want once", asked)
	}
}

// BenchmarkUseWithAnEnvironment is BenchmarkUse for an action with the
// environment the Go plugin gives each of its actions: what every Go
// action of a run but the first costs.
func BenchmarkUseWithAnEnvironment(b *testing.B) {
	_, p, action := pinnedGo(b, installing{})
	action.Environment = map[string]string{"CGO_ENABLED": "0", "GOPROXY": "off", "GOSUMDB": "off", "MISE_GO_SET_GOBIN": "false"}
	using(b, p, action)
}

// An action that comes as the environment of its tools is being given
// to those that asked is given it too, and does not ask again: the grid
// is asked once, however the actions of a run fall about the answer.
func TestAnActionThatComesAsTheEnvironmentIsGivenDoesNotAskAgain(t *testing.T) {
	for trial := range 20 {
		grid := &counting{}
		root, p, action := pinnedGo(t, grid)
		installedGo(t, p, root)
		var joined sync.WaitGroup
		for n := range 200 {
			// Spread about the moment the answer comes, 50ms in.
			after := 40*time.Millisecond + time.Duration(n)*100*time.Microsecond
			joined.Go(func() {
				time.Sleep(after)
				if _, err := p.Use(context.Background(), action, "repo", []string{"go"}, false, nil); err != nil {
					t.Error(err)
				}
			})
		}
		go func() { _, _ = p.Use(context.Background(), action, "repo", []string{"go"}, false, nil) }()
		joined.Wait()
		if asked := grid.asked.Load(); asked != 1 {
			t.Fatalf("trial %d: the grid was asked %d times, want once", trial, asked)
		}
	}
}

// Asking stops when none waits for the answer: the grid is told to give
// up, nothing is kept of it, and an action that asks later asks anew and
// is answered.
func TestAskingForAnEnvironmentNoneWaitsForStops(t *testing.T) {
	grid := &counting{}
	root, p, action := pinnedGo(t, grid)
	installedGo(t, p, root)
	impatient, giveUp := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer giveUp()
	if _, err := p.Use(impatient, action, "repo", []string{"go"}, false, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the action that gave up was told %v", err)
	}
	for wait := time.Now(); grid.gaveUp.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Since(wait) > time.Second {
			t.Fatal("the grid was never told to give up: the asking goes on with none waiting for it")
		}
	}
	if _, err := p.Use(context.Background(), action, "repo", []string{"go"}, false, nil); err != nil {
		t.Fatalf("an action that asked later was told %v", err)
	}
	if asked := grid.asked.Load(); asked != 2 {
		t.Errorf("the grid was asked %d times, want once for each", asked)
	}
}
