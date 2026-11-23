package schedule

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/plan/plantest"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/model"
)

// action is an action of the project "p", by name, with what it needs.
var action = plantest.Action

// recorder is a runner that writes down what it is asked.
type recorder struct {
	mu sync.Mutex
	// fails are the names of the actions that fail.
	fails map[string]bool
	// each is called while an action runs, where a test wants it.
	each func(name string)
	// ran are the names of the actions that ran, each with the paths it
	// was handed; skipped are the ones not run, each with what stopped
	// it.
	ran     []string
	skipped []string
}

func (r *recorder) Run(_ context.Context, a plan.Action, given []Handed) Ending {
	if r.each != nil {
		r.each(a.Label.Name)
	}
	var handed []string
	for _, each := range given {
		for at := range each.Left {
			handed = append(handed, at)
		}
	}
	sort.Strings(handed)
	r.mu.Lock()
	r.ran = append(r.ran, strings.TrimSpace(a.Label.Name+" "+strings.Join(handed, ",")))
	r.mu.Unlock()
	if r.fails[a.Label.Name] {
		return Ending{Outcome: Failed}
	}
	return Ending{Outcome: Passed, Left: map[string]exec.Held{"out/" + a.Label.Name: {}}}
}

func (r *recorder) Skip(a plan.Action, need model.Label, ended Outcome) {
	said := map[Outcome]string{Failed: "failed", Skipped: "was not run"}[ended]
	r.mu.Lock()
	r.skipped = append(r.skipped, a.Label.Name+": "+need.Name+" "+said)
	r.mu.Unlock()
}

func sorted(names []string) string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return strings.Join(out, "; ")
}

func TestAnActionRunsAfterWhatItNeedsWithWhatThatLeft(t *testing.T) {
	r := &recorder{}

	All(context.Background(), []plan.Action{action("compile"), action("notice"), action("package", "compile", "notice")}, 8, r)

	if got := r.ran[len(r.ran)-1]; got != "package out/compile,out/notice" {
		t.Errorf("the last to run was %q, want package, handed what compile and notice left", got)
	}
}

func TestWhatNeedsAFailureIsNotRunAndHearsWhichStoppedIt(t *testing.T) {
	r := &recorder{fails: map[string]bool{"compile": true}}

	All(context.Background(), []plan.Action{action("compile"), action("package", "compile"), action("publish", "package"), action("lint")}, 8, r)

	if got := sorted(r.ran); got != "compile; lint" {
		t.Errorf("ran %q, want compile and lint alone", got)
	}
	if got := sorted(r.skipped); got != "package: compile failed; publish: package was not run" {
		t.Errorf("skipped %q", got)
	}
}

func TestActionsThatDoNotNeedEachOtherRunAtTheSameTime(t *testing.T) {
	// Each action waits for the other to have started: run one after the
	// other, neither would end.
	var both sync.WaitGroup
	both.Add(2)
	met := make(chan struct{})
	go func() { both.Wait(); close(met) }()
	r := &recorder{each: func(string) {
		both.Done()
		select {
		case <-met:
		case <-time.After(10 * time.Second):
		}
	}}

	All(context.Background(), []plan.Action{action("lint"), action("compile")}, 8, r)

	select {
	case <-met:
	default:
		t.Error("the two actions never ran at the same time")
	}
}

func TestNoMoreRunAtOnceThanAreAllowed(t *testing.T) {
	var mu sync.Mutex
	now, most := 0, 0
	r := &recorder{each: func(string) {
		mu.Lock()
		now++
		most = max(most, now)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		now--
		mu.Unlock()
	}}
	var actions []plan.Action
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		actions = append(actions, action(name))
	}

	All(context.Background(), actions, 2, r)

	if most != 2 || len(r.ran) != 6 {
		t.Errorf("%d ran at once and %d ran in all, want 2 at once and all 6", most, len(r.ran))
	}
}

func TestARunThatIsOverReachesNothingMore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &recorder{each: func(string) { cancel() }}

	All(ctx, []plan.Action{action("compile"), action("package", "compile")}, 1, r)

	if got := sorted(r.ran); got != "compile" || len(r.skipped) != 0 {
		t.Errorf("ran %q and skipped %q, want compile alone and nothing said of package", got, r.skipped)
	}
}
