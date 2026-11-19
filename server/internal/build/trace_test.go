package build

import (
	"sort"
	"strings"
	"testing"

	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/telemetry"
	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// traced serves the build API over a workspace, recording the spans of
// its runs.
func traced(t *testing.T, workspace string, mounted bool) (clients, *tracetest.SpanRecorder) {
	t.Helper()
	recorder, signals := recording(t)
	return serveAs(t, configuration{Workspace: workspace, Mounted: mounted, Executor: scripted{}, Telemetry: signals}), recorder
}

// counted is how many spans ended, by name.
func countSpans(spans []sdktrace.ReadOnlySpan) string {
	counts := map[string]int{}
	for _, span := range spans {
		counts[span.Name()]++
	}
	var out []string
	for name, count := range counts {
		out = append(out, name+" "+strings.Repeat("|", count))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// Where a run's time went is read from its trace: one trace for a run,
// a span for each thing it did, each beneath what it was done for.
func TestARunIsOneTraceOfWhatItDid(t *testing.T) {
	c, recorder := traced(t, oneModule(t), true)

	run := wait(t, c.runs, create(t, c.runs).GetId())

	if run.GetExitCode().GetName() != "SUCCESS" {
		t.Fatalf("the run ended %v %s", run.GetState(), run.GetExitCode().GetName())
	}
	all := recorder.Ended()
	var pipeline sdktrace.ReadOnlySpan
	for _, span := range all {
		if span.Name() == "BUILD build" {
			pipeline = span
		}
	}
	if pipeline == nil {
		t.Fatal("no pipeline span")
	}
	var spans []sdktrace.ReadOnlySpan
	for _, span := range all {
		if span.SpanContext().TraceID() == pipeline.SpanContext().TraceID() {
			spans = append(spans, span)
		}
	}
	// One module: its files are listed once, the plugin is asked for it
	// once and, once each of its two checks has ended, what the check
	// found; its three actions are each staged and run. The tool the
	// actions name is fetched once (what confines them is the worker's
	// own); and mise is asked once what it puts in their environment.
	if got, want := countSpans(spans), "BUILD build |, POST |, action |||, environment |, evaluate |, execute |, plan |, plugin |||, snapshot |, stage |||, tool |"; got != want {
		t.Errorf("the run's spans are %s, want %s", got, want)
	}
	byID := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range spans {
		byID[span.SpanContext().SpanID().String()] = span
	}
	for _, span := range spans {
		if span.SpanContext().TraceID() != spans[0].SpanContext().TraceID() {
			t.Errorf("%s is in a trace of its own", span.Name())
		}
		above := byID[span.Parent().SpanID().String()]
		want := map[string]string{"POST": "", "BUILD build": "POST", "snapshot": "BUILD build", "evaluate": "BUILD build", "plan": "BUILD build", "execute": "BUILD build", "plugin": "evaluate", "action": "execute", "stage": "action", "tool": "stage", "environment": "stage"}[span.Name()]
		if got := ""; above != nil {
			got = above.Name()
			// The plugin is asked under evaluate, and, what a check found,
			// under the check's action.
			if span.Name() == "plugin" && got == "action" {
				want = "action"
			}
			if got != want {
				t.Errorf("%s is beneath %q, want %q", span.Name(), got, want)
			}
		} else if want != "" {
			t.Errorf("%s is beneath nothing, want %q", span.Name(), want)
		}
	}
}

// A plugin is asked about each module of a repository on its own, so
// that what it answered for one holds whatever becomes of another, and
// about a module that was handed another once more.
func TestAPluginIsAskedAboutEachModuleOfARepository(t *testing.T) {
	forge := testforge.Make(t)
	c, recorder := traced(t, t.TempDir(), false)
	populated(t, c, &workspacev1.WorkspaceSpec{Repositories: []*workspacev1.RepositorySource{{Remote: forge.Remote, Ref: "main"}}})

	wait(t, c.runs, create(t, c.runs).GetId())

	var asked []string
	for _, span := range recorder.Ended() {
		if span.Name() != "plugin" {
			continue
		}
		for _, attribute := range span.Attributes() {
			if attribute.Key == "method" {
				asked = append(asked, attribute.Value.AsString())
			}
		}
	}
	sort.Strings(asked)
	// services holds two modules, and hello requires greet: each is
	// found by a call of its own, hello's actions are told of greet by
	// one, and each module's two checks are asked what they found.
	if got := strings.Join(asked, " "); got != "Analyze Analyze Arrange Report Report Report Report" {
		t.Errorf("the plugin was asked %q, want an Analyze for each of the two modules, one Arrange for the module that was handed one, and a Report for each of the four checks", got)
	}
}

func traceSignals(t testing.TB, provider *sdktrace.TracerProvider) *telemetry.Signals {
	t.Helper()
	signals, err := telemetry.New(provider, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return signals
}
