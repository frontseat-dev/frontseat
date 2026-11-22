package build

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/telemetry"
)

// cost is what a run cost in asking and reading, as its trace says: the
// plugins asked, the answers given again in place of asking, and the
// files read to know what they held, while evaluating and while
// running.
type cost struct{ asked, again, readEvaluating, readRunning int }

// costOf reads what the newest run cost from the spans recorded.
func costOf(t *testing.T, recorder *tracetest.SpanRecorder) cost {
	t.Helper()
	number := func(span sdktrace.ReadOnlySpan, name string) int {
		for _, each := range span.Attributes() {
			if each.Key == attribute.Key(name) {
				return int(each.Value.AsInt64())
			}
		}
		t.Fatalf("the span %s says nothing of %s", span.Name(), name)
		return 0
	}
	var out cost
	seen := map[string]bool{}
	ended := recorder.Ended()
	for i := len(ended) - 1; i >= 0; i-- {
		span := ended[i]
		if seen[span.Name()] {
			continue
		}
		seen[span.Name()] = true
		switch span.Name() {
		case "evaluate":
			out.asked, out.again, out.readEvaluating = number(span, "asked"), number(span, "again"), number(span, "read")
		case "execute":
			out.readRunning = number(span, "read")
		}
	}
	if !seen["evaluate"] || !seen["execute"] {
		t.Fatal("the run left no trace of evaluating and running")
	}
	return out
}

// twoModules is oneModule with a second module, other, beside hello.
func twoModules(t *testing.T) string {
	t.Helper()
	workspace := oneModule(t)
	write(t, workspace, "other/go.mod", "module example.com/other\n\ngo 1.27\n")
	write(t, workspace, "other/main.go", "package main\n\nfunc main() {}\n")
	return workspace
}

// recording is a recorder of spans, and the telemetry of a daemon whose
// spans it records.
func recording(t *testing.T) (*tracetest.SpanRecorder, *telemetry.Signals) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	return recorder, traceSignals(t, provider)
}

// costing serves the build API over a workspace whose grid answers as
// asking does, and returns what builds it, each run to a good end,
// returning what that run cost, and the recorder of the runs' spans.
func costing(t *testing.T, workspace string) (func() cost, *tracetest.SpanRecorder) {
	t.Helper()
	recorder, signals := recording(t)
	c := serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: asking{edited: &sync.Once{}}, Telemetry: signals})
	return func() cost {
		t.Helper()
		succeeded(t, c, create(t, c.runs))
		return costOf(t, recorder)
	}, recorder
}

// succeeded follows a run to its end and fails the test unless it ended
// well, saying how each of its actions ended where it did not.
func succeeded(t *testing.T, c clients, created *buildv1.Build) *buildv1.Build {
	t.Helper()
	run := wait(t, c.runs, created.GetId())
	if run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		for label, action := range actions(t, c, run) {
			t.Logf("%s: %s %s", label, action.GetOutcome(), action.GetMessage())
		}
		t.Fatalf("the run ended %v %s with %v: %s", run.GetState(), run.GetExitCode(), run.GetCounts(), run.GetFailure())
	}
	return run
}

// settled has every file of a workspace look as one written long ago
// does. What a file held is not kept while the file has only just been
// written (source.Keeper: for twenty milliseconds, or two seconds where
// its times are told in whole seconds), so a test that counts what a run
// read begins with files that are past that moment. Their times are set
// to a minute ago, off the whole second, and the moment in which that
// setting itself shows is waited out.
func settled(t *testing.T, workspace string) {
	t.Helper()
	old := time.Now().Add(-time.Minute).Truncate(time.Second).Add(123456789)
	err := filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		return os.Chtimes(path, old, old)
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
}

// A daemon keeps what its plugins answered and what its files held. A
// run after the first asks a plugin only where what the plugin read has
// changed, and reads a file only where the file looks different.
func TestARunAsksAndReadsOnlyWhatChanged(t *testing.T) {
	workspace := oneModule(t)
	write(t, workspace, "hello/notes.txt", "notes\n")
	settled(t, workspace)
	built, recorder := costing(t, workspace)

	first := built()
	if first.asked == 0 || first.again != 0 || first.readEvaluating == 0 || first.readRunning == 0 {
		t.Fatalf("the first run cost %+v; want plugins asked and files read, and nothing given again", first)
	}

	// Nothing has changed: nothing is asked, and nothing read.
	if second := built(); second != (cost{}) || !lastRetainedSpan(t, recorder, "evaluate") {
		t.Errorf("with nothing changed a run cost %+v; want retained evaluation with no query replay", second)
	}

	// A file no plugin read has changed: it alone is read, and no plugin
	// is asked.
	write(t, workspace, "hello/notes.txt", "more notes\n")
	if third := built(); third.asked != 0 || third.again != 0 || third.readEvaluating != 0 || third.readRunning > 1 {
		t.Errorf("with a file no plugin read changed, a run cost %+v; want no plugin asked and at most that file read", third)
	}

	// A file a plugin read has changed: the plugin is asked again.
	write(t, workspace, "hello/go.mod", "module example.com/hello\n\ngo 1.27\n\n// a word more\n")
	if fourth := built(); fourth.asked == 0 || fourth.readEvaluating == 0 {
		t.Errorf("with a file a plugin read changed, a run cost %+v; want the plugin asked and the file read", fourth)
	}

	// A file has come that a plugin's patterns match: the plugin is asked
	// again, though nothing it read has changed.
	write(t, workspace, "hello/more.go", "package main\n")
	if fifth := built(); fifth.asked == 0 {
		t.Errorf("with a file come that a plugin looks for, a run cost %+v; want the plugin asked", fifth)
	}
}

// A plugin is asked about each module on its own, so that what it
// answered for one holds whatever becomes of another: with one module
// changed, it is asked about that one and no other.
func TestOnlyTheModuleThatChangedIsAskedAbout(t *testing.T) {
	workspace := twoModules(t)
	built, recorder := costing(t, workspace)

	if first := built(); first.asked != 2 || first.again != 0 {
		t.Fatalf("the first run cost %+v; want the plugin asked about each of the two modules", first)
	}

	write(t, workspace, "hello/go.mod", "module example.com/hello\n\ngo 1.27\n\n// a word more\n")

	if second := built(); second.asked != 1 || second.again != 0 || !lastRetainedSpan(t, recorder, "evaluate") {
		t.Errorf("with one module changed a run cost %+v; want one equal answer checked and the derived evaluation retained", second)
	}
}

// A daemon keeps what the grid answered for an action. A run after the
// first asks the grid only about the actions that are no longer what
// they were: with nothing changed about none, and with a file changed
// about the actions that read it.
func TestARunAsksTheGridOnlyAboutTheActionsThatChanged(t *testing.T) {
	workspace := twoModules(t)
	asked := &ran{}
	c := serve(t, asking{edited: &sync.Once{}, ran: asked}, workspace, true)
	built := func() *buildv1.Build {
		t.Helper()
		return succeeded(t, c, create(t, c.runs))
	}

	built()
	if first := asked.since(); len(first) != 6 {
		t.Fatalf("the first run asked the grid about %v; want the three actions of each of the two modules", first)
	}

	second := built()
	if again := asked.since(); len(again) != 0 {
		t.Errorf("with nothing changed a run asked the grid about %v; want nothing asked", again)
	}
	for label, action := range actions(t, c, second) {
		if action.GetOutcome() != buildv1.Outcome_OUTCOME_PASSED || action.GetProvenance() != buildv1.Provenance_PROVENANCE_GRID_CACHE {
			t.Errorf("%s ended %s, from %s; want it passed, as the grid had answered", label, action.GetOutcome(), action.GetProvenance())
		}
	}
	if cost := second.GetCost(); cost.GetActionsExecuted() != 0 {
		t.Errorf("the run says %d actions were run; want none", cost.GetActionsExecuted())
	}

	write(t, workspace, "hello/main.go", "package main\n\nfunc main() { println() }\n")
	built()
	want := []string{"//hello:go.compile", "//hello:go.format", "//hello:go.lint"}
	if third := asked.since(); !reflect.DeepEqual(third, want) {
		t.Errorf("with a file of one module changed a run asked the grid about %v; want %v", third, want)
	}
}

// losing is a grid that lets something go: once told to, it answers the
// next action it is asked about by saying that what the action was
// handed is no longer kept.
type losing struct {
	asking
	lost *atomic.Bool
}

func (l losing) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if !installs(action) && l.lost.CompareAndSwap(true, false) {
		l.ran.note(action.Label)
		return exec.Result{}, fmt.Errorf("%s: %w", action.Label, exec.ErrNotKept)
	}
	return l.asking.Execute(ctx, action)
}

// What a daemon keeps of the grid's answers names content the grid may
// have let go since. A run that finds it has starts over, and then goes
// by nothing that was kept: every action is asked of the grid again.
func TestARunThatStartsOverAsksTheGridAboutEverything(t *testing.T) {
	workspace := twoModules(t)
	asked, lost := &ran{}, &atomic.Bool{}
	c := serve(t, losing{asking: asking{edited: &sync.Once{}, ran: asked}, lost: lost}, workspace, true)
	if run := wait(t, c.runs, create(t, c.runs).GetId()); run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		t.Fatalf("the first run ended %s", run.GetExitCode())
	}
	asked.since()

	write(t, workspace, "hello/main.go", "package main\n\nfunc main() { println() }\n")
	lost.Store(true)
	run := succeeded(t, c, create(t, c.runs))

	if over, _ := startedOver(t, c, run.GetId()); len(over) != 1 {
		t.Errorf("the run started over %d times; want once", len(over))
	}
	// An action under way when the grid was found to have let go may be
	// asked about twice: once then, and once when the run starts over.
	others := map[string]bool{}
	for _, label := range asked.since() {
		if strings.HasPrefix(label, "//other:") {
			others[label] = true
		}
	}
	if len(others) != 3 {
		t.Errorf("the grid was asked about %d actions of the module that had not changed; want all three, nothing kept being gone by", len(others))
	}
}

// crowded is a grid that says how many actions it was asked about at
// once. Each waits for the others a run could ask about beside it, for
// a moment, before it answers.
type crowded struct {
	scripted
	mu       sync.Mutex
	with     int
	most     int
	together chan struct{}
	want     int
}

func (c *crowded) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if installs(action) {
		return c.scripted.Execute(ctx, action)
	}
	c.mu.Lock()
	c.with++
	c.most = max(c.most, c.with)
	if c.with == c.want {
		close(c.together)
	}
	c.mu.Unlock()
	select {
	case <-c.together:
	case <-time.After(2 * time.Second):
	}
	c.mu.Lock()
	c.with--
	c.mu.Unlock()
	return c.scripted.Execute(ctx, action)
}

// A run asks the grid about every action that is ready, and the grid
// decides how many it runs: the daemon does none of the work, and
// holding actions back from a grid that could take them only makes a
// build longer.
func TestARunAsksTheGridAboutEveryActionThatIsReady(t *testing.T) {
	workspace := oneModule(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		write(t, workspace, name+"/go.mod", "module example.com/"+name+"\n\ngo 1.27\n")
		write(t, workspace, name+"/main.go", "package main\n\nfunc main() {}\n")
	}
	// Five modules of three actions, none needing another.
	grid := &crowded{together: make(chan struct{}), want: 15}
	c := serve(t, grid, workspace, true)

	if run := wait(t, c.runs, create(t, c.runs).GetId()); run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		t.Fatalf("the run ended %s: %s", run.GetExitCode(), run.GetFailure())
	}

	if grid.most != 15 {
		t.Errorf("the grid was asked about %d actions at once at most; want all fifteen, none waiting for another", grid.most)
	}
}
