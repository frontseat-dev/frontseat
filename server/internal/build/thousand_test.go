package build

import (
	"fmt"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
	"github.com/frontseat-dev/frontseat/server/testsupport/testworkload"
)

// thousand is a workspace of a thousand Go modules, in groups of ten:
// the first of each group is required by the nine after it. Each has
// three files of Go.
func thousand(t testing.TB) string {
	t.Helper()
	workspace := t.TempDir()
	for i := range 1000 {
		name := fmt.Sprintf("m%04d", i)
		mod := "module example.com/" + name + "\n\ngo 1.27\n"
		if first := i - i%10; first != i {
			mod += fmt.Sprintf("\nrequire example.com/m%04d v0.0.0\n", first)
		}
		write(t, workspace, name+"/go.mod", mod)
		write(t, workspace, name+"/main.go", "package main\n\nfunc main() { println(word()) }\n")
		write(t, workspace, name+"/word.go", "package main\n\nfunc word() string { return \""+name+"\" }\n")
		write(t, workspace, name+"/word_test.go", "package main\n\nimport \"testing\"\n\nfunc TestWord(t *testing.T) {\n\tif word() == \"\" {\n\t\tt.Fatal()\n\t}\n}\n")
	}
	testpins.Go(t, workspace)
	return workspace
}

// phases is how long each part of the newest run took, by the name of
// its span, and how many spans of each name there were.
func phases(recorder *tracetest.SpanRecorder, from int) (took map[string]time.Duration, count map[string]int) {
	took, count = map[string]time.Duration{}, map[string]int{}
	for _, span := range recorder.Ended()[from:] {
		took[span.Name()] += span.EndTime().Sub(span.StartTime())
		count[span.Name()]++
	}
	return took, count
}

// A build of a workspace of a thousand modules, through the daemon and
// its plugin, with a grid that asks what every input held and runs
// nothing. Each benchmark reports where the time went, from the run's
// trace: the first build of a daemon, a build with nothing changed, and
// a build with one file changed.

// daemonOver serves a workspace, recording the spans of its runs.
func daemonOver(b testing.TB, workspace string) (clients, *tracetest.SpanRecorder) {
	b.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	b.Cleanup(func() { _ = provider.Shutdown(b.Context()) })
	return serveAs(b, configuration{Workspace: workspace, Mounted: true, Executor: asking{edited: &sync.Once{}}, Telemetry: traceSignals(b, provider)}), recorder
}

// build builds the workspace and holds the run to having passed.
func build(b testing.TB, c clients) {
	b.Helper()
	run := wait(b, c.runs, create(b, c.runs).GetId())
	if run.GetExitCode().GetName() != "SUCCESS" {
		b.Fatalf("the run ended %v %s: %s", run.GetState(), run.GetExitCode().GetName(), run.GetFailure())
	}
}

// report says where the time of the runs since a span went, for each of
// so many runs.
func report(b *testing.B, recorder *tracetest.SpanRecorder, from, runs int) {
	b.Helper()
	took, count := phases(recorder, from)
	for _, phase := range []string{"snapshot", "evaluate", "plugin", "plan", "execute", "stage"} {
		b.ReportMetric(float64(took[phase].Microseconds())/1000/float64(runs), phase+"-ms/op")
	}
	b.ReportMetric(float64(count["plugin"])/float64(runs), "asked/op")
	b.ReportMetric(float64(count["action"])/float64(runs), "actions/op")
}

// BenchmarkAThousandModulesFirst is the first build of a daemon: every
// plugin is asked and every file read.
func BenchmarkAThousandModulesFirst(b *testing.B) {
	workspace := thousand(b)
	var recorder *tracetest.SpanRecorder
	from := 0
	for b.Loop() {
		b.StopTimer()
		var c clients
		c, recorder = daemonOver(b, workspace)
		from = len(recorder.Ended())
		b.StartTimer()
		build(b, c)
	}
	// The last daemon's run stands for each.
	report(b, recorder, from, 1)
}

// BenchmarkAThousandModulesUnchanged is a build with nothing changed
// since the build before.
func BenchmarkAThousandModulesUnchanged(b *testing.B) {
	c, recorder := daemonOver(b, thousand(b))
	build(b, c)
	from := len(recorder.Ended())
	for b.Loop() {
		build(b, c)
	}
	report(b, recorder, from, b.N)
}

// BenchmarkAThousandModulesOneFileChanged is a build with one file of
// one module changed since the build before.
func BenchmarkAThousandModulesOneFileChanged(b *testing.B) {
	workspace := thousand(b)
	c, recorder := daemonOver(b, workspace)
	build(b, c)
	from := len(recorder.Ended())
	edit := 0
	for b.Loop() {
		b.StopTimer()
		edit++
		write(b, workspace, "m0500/word.go", fmt.Sprintf("package main\n\nfunc word() string { return \"edit %d\" }\n", edit))
		b.StartTimer()
		build(b, c)
	}
	report(b, recorder, from, b.N)
}

// Measured 2026-11-06 on an Apple M4 Max, three runs of `go test -run
// TestBudgetOfAThousandModules -v ./internal/server/`, a thousand
// modules, four thousand files and three thousand actions:
//
//	the first build      337 to 344 ms   2,234,000 allocations
//	nothing changed      94 to 97 ms     1,319,000
//	one file changed     95 to 96 ms     1,321,000
//
// The time allowed is ten times that, the allocations a tenth more.
//
// Measured again 2026-11-08 on the same machine, three runs of
// `go test -run '^$' -bench BenchmarkAThousand -benchmem -count=3
// ./server/internal/build/`, once a module had four actions, a format check
// beside lint, compile and test, and each check that ended was asked
// what it found (Report), its answer kept with the run's others:
//
//	the first build      380 to 423 ms   3,477,000 to 3,480,000 allocations
//	nothing changed      103 to 174 ms   1,740,000 to 1,744,000
//	one file changed     102 to 104 ms   1,741,000 to 1,745,000
//
// Four thousand actions in place of three thousand, and two thousand
// checks read for their findings in the first build, are what the
// allocations more are; the 174 ms was one run among slower ones on a
// loaded machine. The budgets were raised to ten times and a tenth more
// of these in the change that gave a module its fourth action.
//
// Before a run asked once for what confines its actions and for each
// tool, looked for a module's files only beneath the module, and looked
// at a file it knew without opening it, the three took 4.96 s, 3.65 s
// and 4.08 s (`go test -run '^$' -bench BenchmarkAThousandModules
// -benchtime 3x ./internal/server/`, at 5b2c963).
//
// While the plugin was asked once about all the modules of a
// repository, they took 410, 150 and 377 ms, and the first build made
// 1,484,000 allocations: with one file changed the plugin was asked
// about all thousand modules, which was 244 ms of the 377. It is asked
// about each module on its own, several at once, and so about one
// module where one changed. The first build asks 1,900 times in place
// of 901, each an instance of the plugin and a request of its own,
// which is the 333,000 allocations more.
//
// The grid here is asked what an action is before it is asked to run
// it, as the daemon asks a grid now so as not to ask about an action it
// was answered for: 1,817,000, 1,010,000 and 1,010,000 allocations
// before, the rest being this grid's own way of saying what an action
// is, a hundred allocations an action. It answers at once either way,
// so these timings do not measure a remote grid request.
//
// A run's events are written to its record through a buffer, where each
// was a write of its own. These builds do not show it: with each event
// written on its own they take 105 ms with nothing changed, which is
// within what one run of them differs from the next. What holds it is
// TestEventsAreWrittenTogether.

func TestBudgetOfAThousandModulesFirst(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 4300 * time.Millisecond, Allocs: 3_830_000}, BenchmarkAThousandModulesFirst)
}

func TestBudgetOfAThousandModulesUnchanged(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 1750 * time.Millisecond, Allocs: 1_920_000}, BenchmarkAThousandModulesUnchanged)
}

func TestBudgetOfAThousandModulesOneFileChanged(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 1050 * time.Millisecond, Allocs: 1_920_000}, BenchmarkAThousandModulesOneFileChanged)
}

// BenchmarkAThousandEntitiesUnchanged is a build, with nothing changed,
// of the workspace frontseat is measured beside other tools on: a
// thousand entities whose catalog files give each its three actions.
func BenchmarkAThousandEntitiesUnchanged(b *testing.B) {
	workspace := b.TempDir()
	if err := testworkload.Frontseat(workspace, testworkload.Shape{Modules: 1000, Group: 10, Salt: "budget"}, ""); err != nil {
		b.Fatal(err)
	}
	c, recorder := daemonOver(b, workspace)
	build(b, c)
	from := len(recorder.Ended())
	for b.Loop() {
		build(b, c)
	}
	report(b, recorder, from, b.N)
}

// Measured 2026-11-06 on an Apple M4 Max, three runs of `go test -run
// TestBudgetOfAThousandEntitiesUnchanged -v ./internal/server/`: 64 to
// 66 ms and 590,000 allocations. The time allowed is ten times that,
// the allocations a tenth more.
//
// It took 170 ms while every event of the run was a write to its
// record and every catalog file was read and parsed for every build.
// The allocations hold the second: with the catalog files read again
// for every build, a build makes 1,027,000.
//
// Measured again 2026-11-09 with `go test -run
// TestBudgetOfAThousandEntitiesUnchanged -v ./build/` in server/: 617,200
// before an action's inputs named what it reads of others, and 633,000
// after, for 2,700 references: each is resolved among the workspace's
// actions and kept as what is read. It was 671,900 while what an action
// is handed was made anew for every action of every build; the budget
// is unchanged.
func TestBudgetOfAThousandEntitiesUnchanged(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 660 * time.Millisecond, Allocs: 650_000}, BenchmarkAThousandEntitiesUnchanged)
}
