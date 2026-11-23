package build

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
)

// answering is an executor that answers each action of a build as a
// test says a grid did.
type answering struct {
	scripted
	as func(*exec.Result)
}

func (a answering) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	result, err := a.scripted.Execute(ctx, action)
	if err == nil && !installs(action) {
		a.as(&result)
	}
	return result, err
}

// What a run says of an action's time is what the worker that ran it
// said: when it started there, and how long it worked. An action that
// was answered from a cache started nowhere and cost no worker anything.
func TestAnActionsTimesAreTheWorkersAndACachedOneHasNone(t *testing.T) {
	started := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	ran := serve(t, answering{as: func(r *exec.Result) { r.Started, r.Worked, r.Duration = started, 2*time.Second, time.Minute }}, oneModule(t), true)
	run := wait(t, ran.runs, create(t, ran.runs).GetId())
	listed, err := ran.runs.ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{BuildId: run.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetActions()) != 3 {
		t.Fatalf("the run has %d actions, want the module's three", len(listed.GetActions()))
	}
	for _, action := range listed.GetActions() {
		if !action.GetStartTime().AsTime().Equal(started) {
			t.Errorf("%s started at %v, want when its worker says, %v", action.GetLabel(), action.GetStartTime().AsTime(), started)
		}
	}
	// Three actions, two seconds each on a worker. That each took a
	// minute to be answered, sent and waited for, is no worker's time.
	if got := run.GetCost().GetWorkerTime().AsDuration(); got != 6*time.Second {
		t.Errorf("the workers' time is %v, want the six seconds they worked", got)
	}

	kept := serve(t, answering{as: func(r *exec.Result) { r.Cached = true }}, oneModule(t), true)
	run = wait(t, kept.runs, create(t, kept.runs).GetId())
	listed, err = kept.runs.ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{BuildId: run.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range listed.GetActions() {
		if action.GetStartTime() != nil {
			t.Errorf("%s was answered from a cache and started at %v, want no start", action.GetLabel(), action.GetStartTime().AsTime())
		}
	}
	if run.GetCost().GetWorkerTime().AsDuration() != 0 || run.GetCost().GetActionsExecuted() != 0 {
		t.Errorf("a run answered from a cache cost %v of workers' time for %d actions, want none", run.GetCost().GetWorkerTime().AsDuration(), run.GetCost().GetActionsExecuted())
	}
}

// What an action printed is returned whole: a line of any length, and
// every line after it.
func TestALineOfAnyLengthIsReturnedWithAllThatFollowsIt(t *testing.T) {
	long := strings.Repeat("x", 3<<19)
	c := serve(t, answering{as: func(r *exec.Result) { r.Stdout = []byte("first\n" + long + "\nlast\nno newline at the end") }}, oneModule(t), true)
	run := wait(t, c.runs, create(t, c.runs).GetId())

	logs, err := logsOf(c.runs, run.GetId(), "//hello:go.lint")

	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range logs {
		got = append(got, line.GetText())
	}
	if len(got) != 4 || got[0] != "first" || got[1] != long || got[2] != "last" || got[3] != "no newline at the end" {
		lengths := make([]int, len(got))
		for i := range got {
			lengths[i] = len(got[i])
		}
		t.Errorf("the output came back as %d lines of lengths %v, want its four, the second %d long", len(got), lengths, len(long))
	}
}

// refusing is a grid that runs no action of a build.
type refusing struct{ scripted }

func (r refusing) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if !installs(action) {
		return exec.Result{}, errors.New("the grid is away")
	}
	return r.scripted.Execute(ctx, action)
}

// actions returns a run's actions by label.
func actions(t *testing.T, c clients, run *buildv1.Build) map[string]*buildv1.ActionResult {
	t.Helper()
	listed, err := c.runs.ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{BuildId: run.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*buildv1.ActionResult{}
	for _, action := range listed.GetActions() {
		by[action.GetLabel()] = action
	}
	return by
}

// What the daemon does itself is an action of the run, and says the
// daemon did it: no worker ran it, and what a run cost its workers is
// counted from what a worker ran.
func TestWhatTheDaemonDoesItselfIsNoWorkersWork(t *testing.T) {
	forge := testforge.Make(t)
	forge.Write(t, "platform", "gateway/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: gateway\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  dependsOn: [component:greet]\n")
	c := serve(t, answering{as: func(r *exec.Result) { r.Worked = time.Second }}, t.TempDir(), false)
	run := ranGateway(t, c, forge)

	own, ran := 0, 0
	for label, action := range actions(t, c, run) {
		want := buildv1.Provenance_PROVENANCE_EXECUTED
		if strings.Contains(label, ":catalog.") {
			want = buildv1.Provenance_PROVENANCE_DAEMON
			own++
		} else {
			ran++
		}
		if action.GetOutcome() != buildv1.Outcome_OUTCOME_PASSED || action.GetProvenance() != want {
			t.Errorf("%s ended %s, from %s; want it passed, from %s", label, action.GetOutcome(), action.GetProvenance(), want)
		}
	}
	if own == 0 || ran == 0 {
		t.Fatalf("the run has %d actions of the daemon's own and %d a worker runs; want some of each", own, ran)
	}
	if got := run.GetCost(); int(got.GetActionsExecuted()) != ran || got.GetWorkerTime().AsDuration() != time.Duration(ran)*time.Second {
		t.Errorf("the run cost %d actions and %v of workers' time; want the %d a worker ran, a second each", got.GetActionsExecuted(), got.GetWorkerTime().AsDuration(), ran)
	}
}

// An action that could not be run has no result, and came from nowhere:
// no worker ran it.
func TestAnActionThatCouldNotBeRunWasRunByNoWorker(t *testing.T) {
	c := serve(t, refusing{}, oneModule(t), true)

	run := wait(t, c.runs, create(t, c.runs).GetId())

	by := actions(t, c, run)
	if len(by) != 3 {
		t.Fatalf("the run has %d actions, want the module's three", len(by))
	}
	for label, action := range by {
		if action.GetOutcome() == buildv1.Outcome_OUTCOME_ERRORED && action.GetProvenance() != buildv1.Provenance_PROVENANCE_UNSPECIFIED {
			t.Errorf("%s could not be run, and its result is said to be from %s", label, action.GetProvenance())
		}
	}
	if run.GetCounts().GetErrored() == 0 {
		t.Error("no action of the run could not be run; want the ones the grid refused")
	}
	if got := run.GetCost().GetActionsExecuted(); got != 0 {
		t.Errorf("%d actions are counted as run by a worker; none was", got)
	}
}
