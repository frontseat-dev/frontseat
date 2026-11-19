package schedule

import (
	"context"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

func traceRecorder(t *testing.T) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	return provider, recorder
}

// executing is a context of a span, "execute", that spans records; the
// span ends and the context is cancelled when the test ends, or at cancel.
func executing(t *testing.T) (context.Context, context.CancelFunc, trace.Span, *tracetest.SpanRecorder) {
	t.Helper()
	provider, spans := traceRecorder(t)
	ctx, parent := provider.Tracer("test").Start(t.Context(), "execute")
	t.Cleanup(func() { parent.End() })
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	return ctx, cancel, parent, spans
}

func taskAttribute(span sdktrace.ReadOnlySpan, key attribute.Key) string {
	for _, attr := range span.Attributes() {
		if attr.Key == key {
			return attr.Value.AsString()
		}
	}
	return ""
}

// Each supplied set must stand alone: a link into another attempt or command
// fails even if its target has the same action label.
func checkTaskLinks(t *testing.T, spans []sdktrace.ReadOnlySpan, actions []plan.Action, parent trace.SpanContext) map[string]sdktrace.ReadOnlySpan {
	t.Helper()
	byLabel := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range spans {
		if span.Name() != "action" {
			continue
		}
		label := taskAttribute(span, semconv.CICDPipelineTaskNameKey)
		if byLabel[label] != nil {
			t.Fatalf("duplicate task %q", label)
		}
		byLabel[label] = span
		if !span.Parent().Equal(parent) || span.SpanContext().TraceID() != parent.TraceID() {
			t.Errorf("%s does not belong to its scheduling context", label)
		}
		if taskAttribute(span, semconv.CICDPipelineTaskRunIDKey) != span.SpanContext().SpanID().String() {
			t.Errorf("%s has no task run identity", label)
		}
	}
	if len(byLabel) != len(actions) {
		t.Fatalf("got %d tasks, want %d", len(byLabel), len(actions))
	}
	for _, action := range actions {
		span := byLabel[action.Label.String()]
		if span == nil || len(span.Links()) != len(action.Needs) {
			t.Fatalf("%s has missing dependencies", action.Label)
		}
		for i, need := range action.Needs {
			dependency := byLabel[need.String()]
			if dependency == nil || !span.Links()[i].SpanContext.Equal(dependency.SpanContext()) {
				t.Errorf("%s has a missing or stale link to %s", action.Label, need)
			}
		}
	}
	return byLabel
}

func TestTraceKeepsFanInAndSkippedChainsInUnorderedPlans(t *testing.T) {
	for _, fail := range []bool{false, true} {
		provider, spans := traceRecorder(t)
		ctx, parent := provider.Tracer("test").Start(t.Context(), "execute")
		actions := []plan.Action{action("publish", "package"), action("package", "compile", "notice"), action("notice"), action("compile")}
		All(ctx, actions, 2, &recorder{fails: map[string]bool{"compile": fail}})
		got := checkTaskLinks(t, spans.Ended(), actions, parent.SpanContext())
		for _, name := range []string{"package", "publish"} {
			want := "success"
			if fail {
				want = "skip"
			}
			if result := taskAttribute(got[action(name).Label.String()], semconv.CICDPipelineTaskRunResultKey); result != want {
				t.Errorf("%s result = %s, want %s", name, result, want)
			}
		}
		parent.End()
	}
}

func TestRetriesHaveFreshTaskIdentitiesAndNoLinksIntoEarlierAttempts(t *testing.T) {
	provider, spans := traceRecorder(t)
	ctx, parent := provider.Tracer("test").Start(t.Context(), "execute")
	defer parent.End()
	actions := []plan.Action{action("compile", "fetch"), action("fetch")}
	All(ctx, actions, 2, instant{})
	first := spans.Ended()
	before := checkTaskLinks(t, first, actions, parent.SpanContext())
	All(ctx, actions, 2, instant{})
	after := checkTaskLinks(t, spans.Ended()[len(first):], actions, parent.SpanContext())
	for label, span := range after {
		if span.SpanContext().SpanID() == before[label].SpanContext().SpanID() {
			t.Errorf("%s reused an earlier attempt's task identity", label)
		}
	}
}

func TestConcurrentCommandsKeepTheirOwnDependencyLinks(t *testing.T) {
	provider, spans := traceRecorder(t)
	actions := []plan.Action{action("compile", "fetch"), action("fetch")}
	var parents [2]trace.SpanContext
	var running sync.WaitGroup
	for i := range parents {
		ctx, parent := provider.Tracer("test").Start(t.Context(), "execute", trace.WithNewRoot())
		parents[i] = parent.SpanContext()
		running.Go(func() { defer parent.End(); All(ctx, actions, 2, instant{}) })
	}
	running.Wait()
	for _, parent := range parents {
		var own []sdktrace.ReadOnlySpan
		for _, span := range spans.Ended() {
			if span.SpanContext().TraceID() == parent.TraceID() {
				own = append(own, span)
			}
		}
		checkTaskLinks(t, own, actions, parent)
	}
}

func TestCancellationAccountsForUnreachedTasksWithoutChangingCompletedResults(t *testing.T) {
	ctx, cancel, parent, spans := executing(t)
	actions := []plan.Action{action("compile"), action("package", "compile")}
	runner := &recorder{each: func(string) { cancel() }}
	All(ctx, actions, 1, runner)
	got := checkTaskLinks(t, spans.Ended(), actions, parent.SpanContext())
	if len(runner.ran) != 1 {
		t.Fatalf("ran %v, want compile alone", runner.ran)
	}
	for name, want := range map[string]string{"compile": "success", "package": "cancellation"} {
		span := got[action(name).Label.String()]
		if result := taskAttribute(span, semconv.CICDPipelineTaskRunResultKey); result != want {
			t.Errorf("%s result = %s, want %s", name, result, want)
		}
		if span.Status().Code != codes.Unset {
			t.Errorf("%s acquired an error status: %v", name, span.Status())
		}
	}
}
