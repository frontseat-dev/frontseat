package run

import (
	"context"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/schedule"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

var (
	testBuild = model.Label{Path: "lab/fixtures", Plugin: "go", Name: "compile-tests"}
	labTree   = source.Tree{Paths: map[string]string{"lab": "lab"}}
)

// reading is a test that reads one program of what builds the tests,
// and says, or does not, that it has nothing to do without it.
func reading(name, program string, idle bool) plan.Action {
	read := plan.Read{From: testBuild, Repository: "lab", ProjectRoot: "fixtures", Output: program}
	action := plan.Action{
		Label: model.Label{Path: "lab/fixtures", Plugin: "go", Name: name}, Repository: "lab", ProjectRoot: "fixtures",
		Declared: &pluginv1.Action{Name: name, Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{IdleWithoutInputs: idle}}, Inputs: []*pluginv1.Input{{Kind: &pluginv1.Input_Output{Output: program}}}},
		Reads:    []plan.Read{read},
		Needs:    []model.Label{testBuild},
	}
	// As Resolve leaves it: what its plugin named one by one.
	if idle {
		action.IdleWithout = []plan.Read{read}
	}
	return action
}

// An action that says so has nothing to do where an output it reads was
// not left: beside others its action left, and where its action left
// nothing at all, which is then not among what it is handed.
func TestAnActionHasNothingToDoWhereWhatItReadsWasNotLeft(t *testing.T) {
	others := []schedule.Handed{{From: testBuild, Left: map[string]exec.Held{"lab/fixtures/_tests/0/other.test": {}}}}
	its := []schedule.Handed{{From: testBuild, Left: map[string]exec.Held{"lab/fixtures/_tests/0/elsewhere.test": {}}}}
	test := reading("test/elsewhere", "_tests/0/elsewhere.test", true)
	for says, handed := range map[string][]schedule.Handed{"another program": others, "nothing at all": nil, "nothing, said": {{From: testBuild}}} {
		missing, idle := nothingToDo(labTree, test, handed)
		if !idle || missing != "//lab/fixtures:go.compile-tests left no _tests/0/elsewhere.test" {
			t.Errorf("its build left %s: idle %v, missing %q; want it idle for the program it reads", says, idle, missing)
		}
	}
	// The controls: its program left, and an action that does not say so.
	if missing, idle := nothingToDo(labTree, test, its); idle {
		t.Errorf("its program was left, and it is idle for %q", missing)
	}
	if _, idle := nothingToDo(labTree, reading("test/elsewhere", "_tests/0/elsewhere.test", false), others); idle {
		t.Error("an action that does not say it may have nothing to do is idle")
	}
}

// asked is a runner that says which actions would have nothing to do,
// with what the schedule hands them.
type asked struct {
	mu   sync.Mutex
	idle map[string]bool
}

func (a *asked) Run(_ context.Context, action plan.Action, handed []schedule.Handed) schedule.Ending {
	if action.Label == testBuild {
		return schedule.Ending{Outcome: schedule.Passed, Left: map[string]exec.Held{}}
	}
	_, idle := nothingToDo(labTree, action, handed)
	a.mu.Lock()
	a.idle[action.Label.Name] = idle
	a.mu.Unlock()
	return schedule.Ending{Outcome: schedule.Passed}
}

func (a *asked) Skip(plan.Action, model.Label, schedule.Outcome) {}

// Through the schedule, which is what hands: what builds a module's
// tests passes and leaves nothing, as where every test of the module is
// for another system, and each test has nothing to do.
func TestWhatLeftNothingAtAllLeavesItsReadersWithNothingToDo(t *testing.T) {
	build := plan.Action{Label: testBuild, Repository: "lab", ProjectRoot: "fixtures", Declared: &pluginv1.Action{Name: "compile-tests",
		Outputs: []*pluginv1.Output{{Path: "_tests/0/elsewhere.test", Optional: true}}}}
	runner := &asked{idle: map[string]bool{}}
	schedule.All(t.Context(), []plan.Action{build, reading("test/elsewhere", "_tests/0/elsewhere.test", true)}, 4, runner)
	if !runner.idle["test/elsewhere"] {
		t.Fatal("the test would be run without its program: what left nothing is not handed, and that was taken for nothing lacking")
	}
}
