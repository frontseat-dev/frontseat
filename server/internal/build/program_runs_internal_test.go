package build

import (
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"strings"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

func aRun() *programRun {
	return &programRun{changed: make(chan struct{}), stop: make(chan struct{}), ended: make(chan struct{}), state: &buildv1.Run{}}
}

// What a program printed is kept by its newest lines, each by its place
// among all it printed: a line let go is answered from the first kept.
func TestTheNewestLinesAreKeptEachByItsPlace(t *testing.T) {
	l := aRun()
	_, _, _, changed := l.since(0)
	for i := range linesKept + 5 {
		l.printed(i%2 == 1, []byte{byte('a' + i%26)}, nil)
	}
	select {
	case <-changed:
	default:
		t.Error("whoever waited for a line was not told of one")
	}
	lines, next, ended, _ := l.since(0)
	if len(lines) != linesKept || next != linesKept+5 || ended {
		t.Fatalf("from the start: %d lines, next %d, ended %v", len(lines), next, ended)
	}
	if lines[0].GetText() != string(rune('a'+5%26)) {
		t.Errorf("the first kept is %q, want the sixth printed", lines[0].GetText())
	}
	lines, next, _, _ = l.since(linesKept + 3)
	if len(lines) != 2 || next != linesKept+5 || lines[1].GetText() != string(rune('a'+(linesKept+4)%26)) {
		t.Errorf("from the last two: %d lines, next %d", len(lines), next)
	}
	// Past the end is nothing, and the place asked for.
	if lines, next, _, _ := l.since(linesKept + 50); len(lines) != 0 || next != linesKept+50 {
		t.Errorf("past the end: %d lines, next %d", len(lines), next)
	}
}

// Long lines are kept to a budget of bytes, not a count alone.
func TestWhatIsKeptOfLongLinesIsBounded(t *testing.T) {
	l := aRun()
	long := []byte(strings.Repeat("x", 64<<10))
	for range 400 {
		l.printed(false, long, nil)
	}
	lines, next, _, _ := l.since(0)
	if next != 400 || l.bytes > bytesKept || len(lines) >= 400 || len(lines) < bytesKept/(len(long)+64) {
		t.Errorf("of 400 lines of 64 KiB, %d are kept in %d bytes, next %d", len(lines), l.bytes, next)
	}
	// One line past the budget is still kept: the newest always is.
	huge := aRun()
	huge.printed(false, []byte(strings.Repeat("y", bytesKept+1)), nil)
	if lines, _, _, _ := huge.since(0); len(lines) != 1 {
		t.Errorf("a line past the budget: %d kept", len(lines))
	}
}

// A run says which action stopped its build.
func TestARunSaysWhichActionStoppedItsBuild(t *testing.T) {
	passed := &buildv1.ActionResult{Label: "//m:go.fetch", Outcome: buildv1.Outcome_OUTCOME_PASSED}
	for want, results := range map[string][]*buildv1.ActionResult{
		"":                          {passed},
		": //m:go.compile failed":   {passed, {Label: "//m:go.compile", Outcome: buildv1.Outcome_OUTCOME_FAILED}, {Label: "//m:go.run/x", Outcome: buildv1.Outcome_OUTCOME_SKIPPED}},
		": //m:go.run/x: no such p": {passed, {Label: "//m:go.run/x", Outcome: buildv1.Outcome_OUTCOME_ERROR, Message: "no such p"}},
	} {
		if got := stoppedBy(results); got != want {
			t.Errorf("stopped by %q, want %q", got, want)
		}
	}
}

// Runs that ended are let go beyond the newest; one that has not
// ended never is.
func TestRunsThatEndedAreLetGoBeyondTheNewest(t *testing.T) {
	ls := &programRuns{}
	going := aRun()
	going.state.Id = "going"
	ls.made = append(ls.made, going)
	for range runsKept + 4 {
		over := aRun()
		close(over.ended)
		ls.made = append(ls.made, over)
	}
	ls.forget()
	if len(ls.made) != runsKept+1 || ls.made[0] != going {
		t.Errorf("%d are remembered, the first %q", len(ls.made), ls.made[0].state.GetId())
	}
}

// A daemon has only so many runs that have not ended: one more is
// refused before anything is built, and one that ended makes room.
func TestADaemonHasOnlySoManyRunsAtOnce(t *testing.T) {
	ls := &programRuns{}
	for range runsAtOnce {
		ls.made = append(ls.made, aRun())
	}
	if _, err := ls.create(t.Context(), "development", pluginv1.Profile_PROFILE_DEVELOPMENT, &buildv1.CreateRunRequest{Target: "//m:go.run/x"}); err != errTooManyRuns {
		t.Fatalf("one run more: %v", err)
	}
	if len(ls.made) != runsAtOnce {
		t.Errorf("a run that was refused is remembered: %d", len(ls.made))
	}
	// The daemon ending refuses too, and says that.
	ls.stopping = true
	if _, err := ls.create(t.Context(), "development", pluginv1.Profile_PROFILE_DEVELOPMENT, &buildv1.CreateRunRequest{Target: "//m:go.run/x"}); err != errStopped {
		t.Errorf("a run of a daemon that is ending: %v", err)
	}
}

// A run is made with the run that builds its program, and says which
// at once: whoever made it follows the build without asking again.
func TestARunIsMadeWithItsBuild(t *testing.T) {
	c := serve(t, printing(), oneModule(t), true)
	ls := c.daemon.programs
	made, err := ls.create(t.Context(), "development", pluginv1.Profile_PROFILE_DEVELOPMENT, &buildv1.CreateRunRequest{Target: "//hello:go.run/none"})
	if err != nil {
		t.Fatal(err)
	}
	run := made.snapshot().GetBuildId()
	if run == "" {
		t.Fatal("the run was made with no run")
	}
	built, err := ls.runs.get(run)
	if err != nil || built.snapshot().GetTargets()[0] != "//hello:go.run/none" {
		t.Errorf("the run names the run %s, which is %v (%v); want the build of its program", run, built, err)
	}
	<-made.ended
}
