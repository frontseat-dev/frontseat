package build

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// forgetful is a grid that holds a tool anew each time it is asked for
// it: what it left the last time is gone, as a grid may let it go, and
// what it leaves now is under another digest. It keeps which tree of Go
// each action was handed.
type forgetful struct {
	scripted

	mu       sync.Mutex
	installs map[string]int
	handed   []string
}

func (f *forgetful) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	f.mu.Lock()
	if strings.HasPrefix(action.Label, "mise install ") {
		defer f.mu.Unlock()
		if f.installs == nil {
			f.installs = map[string]int{}
		}
		f.installs[action.Label]++
		left := map[string]exec.Held{}
		for _, output := range action.Outputs {
			left[output] = exec.Held{Digest: exec.Digest{Hash: fmt.Sprintf("%064d", f.installs[action.Label]), Size: 1}, Directory: true}
		}
		return exec.Result{Outputs: left}, nil
	}
	for at, each := range action.Held {
		if strings.Contains(at, "installs/go") {
			f.handed = append(f.handed, each.Digest.Hash)
		}
	}
	f.mu.Unlock()
	return f.scripted.Execute(ctx, action)
}

// took returns the trees of Go the actions since the last call were
// handed, each once, and how often the grid was asked to install Go.
func (f *forgetful) took() (trees []string, installs int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, tree := range f.handed {
		if len(trees) == 0 || trees[len(trees)-1] != tree {
			trees = append(trees, tree)
		}
	}
	f.handed = nil
	for label, count := range f.installs {
		if strings.Contains(label, "go") {
			installs += count
		}
	}
	return trees, installs
}

// The grid is what knows whether it still holds a tool: it answers from
// what it kept only while all of that is there, and installs the tool
// again when it is not. A run asks it once for each tool and holds the
// answer while it runs; the next run asks again. A daemon that kept the
// answer from one run to the next would hand an action a tree the grid
// has let go.
func TestEveryRunAsksTheGridWhereItsToolsAre(t *testing.T) {
	grid := &forgetful{}
	c := serve(t, grid, oneModule(t), true)

	first := wait(t, c.runs, create(t, c.runs).GetId())
	trees, installs := grid.took()
	if first.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || len(trees) != 1 || trees[0] != fmt.Sprintf("%064d", 1) || installs != 1 {
		t.Fatalf("the first run ended %s, its actions handed Go at %v after %d installs; want all of them handed the one tree the grid installed, once", first.GetExitCode(), trees, installs)
	}

	second := wait(t, c.runs, create(t, c.runs).GetId())
	trees, installs = grid.took()
	if second.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || len(trees) != 1 || trees[0] != fmt.Sprintf("%064d", 2) || installs != 2 {
		t.Errorf("the second run ended %s, its actions handed Go at %v after %d installs in all; want it to have asked the grid again, and all of them handed where Go is now", second.GetExitCode(), trees, installs)
	}
}

// dropping is a grid that, so many times, no longer holds something the
// module's lint was handed.
type dropping struct {
	scripted

	mu    sync.Mutex
	times int
}

func (d *dropping) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if action.Label == "//hello:go.lint" {
		d.mu.Lock()
		drop := d.times > 0
		d.times--
		d.mu.Unlock()
		if drop {
			return exec.Result{}, fmt.Errorf("the grid: its tools: %w", exec.ErrNotKept)
		}
	}
	return d.scripted.Execute(ctx, action)
}

// startedOver returns the run's events that say it started over, and
// the labels it planned after the last of them.
func startedOver(t *testing.T, c clients, run string) (over []*buildv1.BuildEvent_StartedOver, planned []string) {
	t.Helper()
	for _, event := range events(t, c.runs, run, 0) {
		if again := event.GetStartedOver(); again != nil {
			over, planned = append(over, again), nil
		}
		for _, action := range event.GetPlanned().GetActions() {
			planned = append(planned, action.GetLabel())
		}
	}
	return over, planned
}

// A grid may let go of what it kept while a run is under way: a tool,
// or what one action left for another. The action handed it was not run
// and has not failed. The run starts over, once: it asks the grid again
// where everything is, and the grid makes again what it let go.
func TestARunStartsOverWhenTheGridLetGoOfWhatAnActionWasHanded(t *testing.T) {
	c := serve(t, &dropping{times: 1}, oneModule(t), true)

	run := wait(t, c.runs, create(t, c.runs).GetId())

	if run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || run.GetCounts().GetPassed() != 3 || run.GetCounts().GetErrored() != 0 || run.GetCounts().GetTotal() != 3 {
		t.Errorf("the run ended %s with %v; want SUCCESS, its three actions passed and none in error", run.GetExitCode(), run.GetCounts())
	}
	over, planned := startedOver(t, c, run.GetId())
	if len(over) != 1 || over[0].GetLabel() != "//hello:go.lint" || !strings.Contains(over[0].GetReason(), "keeps nothing under that digest") {
		t.Fatalf("the run says it started over %v; want once, for the lint, with what the grid said", over)
	}
	if len(planned) != 3 {
		t.Errorf("after starting over the run planned %v; want its three actions again", planned)
	}
	for label, action := range actions(t, c, run) {
		if action.GetOutcome() != buildv1.Outcome_OUTCOME_PASSED {
			t.Errorf("%s ended %s; want what it did after the run started over", label, action.GetOutcome())
		}
	}
}

// A grid that lets the same go again is not waited on: the run ends, and
// says why.
func TestARunStartsOverNoMoreThanOnce(t *testing.T) {
	c := serve(t, &dropping{times: 5}, oneModule(t), true)

	run := wait(t, c.runs, create(t, c.runs).GetId())

	over, _ := startedOver(t, c, run.GetId())
	if len(over) != 1 {
		t.Errorf("the run started over %d times, want once", len(over))
	}
	lint := actions(t, c, run)["//hello:go.lint"]
	if run.GetExitCode() == buildv1.ExitCode_EXIT_CODE_SUCCESS || lint.GetOutcome() != buildv1.Outcome_OUTCOME_ERRORED || !strings.Contains(lint.GetMessage(), "keeps nothing under that digest") {
		t.Errorf("the run ended %s, its lint %s (%s); want the run not to succeed and the lint in error for what the grid let go", run.GetExitCode(), lint.GetOutcome(), lint.GetMessage())
	}
}
