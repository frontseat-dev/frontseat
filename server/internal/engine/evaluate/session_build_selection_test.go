package evaluate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/telemetry"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/protobuf/proto"
)

func buildSession(t *testing.T, root string) (*Session, *source.Keeper) {
	t.Helper()
	plugins := retainedPlugins(t)
	files := &source.Keeper{}
	s := newSession(t, plugins, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, takesTree(files, root))
	t.Cleanup(s.Close)
	return s, files
}

// buildingSession is a session over this checkout's plugins that is only
// asked to build, and is closed when the test ends.
func buildingSession(t *testing.T) *Session {
	t.Helper()
	s := newSession(t, retainedPlugins(t), RetentionLimits{8192, 8 << 20}, 1, &Answers{}, takesNoSource)
	t.Cleanup(s.Close)
	return s
}

// sessionFixture is a fixture of modules, this checkout's plugins, and a
// keeper to take snapshots of the fixture through.
func sessionFixture(t *testing.T, modules int) (string, []*pluginhost.Plugin, *source.Keeper) {
	t.Helper()
	return retainedFixture(t, modules), retainedPlugins(t), &source.Keeper{}
}

// wholeBuild is the session's plan of the whole of root for development,
// over a snapshot taken through files; a refusal fails the test.
func wholeBuild(t *testing.T, s *Session, files *source.Keeper, root string) BuildPlan {
	t.Helper()
	got, err := s.Build(t.Context(), retainedTake(t, files, root), pluginv1.Profile_PROFILE_DEVELOPMENT, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// projectsMatch fails the test, with message, unless the session's
// projects are those a fresh evaluation of root for profile lists.
func projectsMatch(t *testing.T, s *Session, root string, profile pluginv1.Profile, message string) {
	t.Helper()
	projects, err := s.Projects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(projects, projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile).ProjectList()) {
		t.Fatal(message)
	}
}

func buildMatches(t *testing.T, s *Session, files *source.Keeper, root string, profile pluginv1.Profile, targets, only []string) BuildPlan {
	t.Helper()
	got, gotErr := s.Build(t.Context(), retainedTake(t, files, root), profile, nil, targets, only)
	fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
	needed, asked, wantErr := plan.Of(fresh.Actions, fresh.Entities, targets, only)
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatalf("retained=%v fresh=%v", gotErr, wantErr)
	}
	if wantErr == nil && !bytes.Equal(projectionBytes(t, selectedPlan{got.Needed, got.Asked, got.Entities}), projectionBytes(t, selectedPlan{needed, asked, fresh.Entities})) {
		t.Fatal("retained build differs from ordinary selection")
	}
	if wantErr != nil && (got.Needed != nil || got.Asked != nil || got.Entities != nil) {
		t.Fatal("refused build returned a partial selection")
	}
	return got
}

func TestRetainedBuildFilterKeysAndOwnedPlansAcrossGraphQueries(t *testing.T) {
	root := retainedFixture(t, 3)
	s, files := buildSession(t, root)
	profile := pluginv1.Profile_PROFILE_DEVELOPMENT
	if _, err := s.Projects(t.Context()); err != nil {
		t.Fatal(err)
	}
	targets, only := []string{"m0001/"}, []string{"compile"}
	first := buildMatches(t, s, files, root, profile, targets, only)
	if !first.Work.Retained || first.Work.PlanRetained {
		t.Fatal("graph-to-build control did not retain evaluation only")
	}
	before := projectionBytes(t, selectedPlan{first.Needed, first.Asked, first.Entities})
	targets[0], only[0] = "m0002/", "format"
	first.Asked[0].Declared.Name = "caller"
	if bytes.Equal(before, projectionBytes(t, selectedPlan{first.Needed, first.Asked, first.Entities})) {
		t.Fatal("owned plan mutation control ineffective")
	}
	if _, err := s.Facts(t.Context()); err != nil {
		t.Fatal(err)
	}
	warm := buildMatches(t, s, files, root, profile, []string{"m0001/"}, []string{"compile"})
	if warm.Work != (Work{Retained: true, PlanRetained: true}) {
		t.Fatalf("caller input/output mutations damaged reuse: %+v", warm.Work)
	}
	for _, selection := range []struct{ targets, only []string }{
		{targets, only}, {nil, []string{"lint", "compile"}}, {nil, []string{"compile", "lint"}},
		{[]string{"m0001/", "m0001/"}, []string{"compile", "compile"}},
		{nil, []string{""}}, {nil, []string{"unknown"}}, {nil, nil},
	} {
		buildMatches(t, s, files, root, profile, selection.targets, selection.only)
	}
	buildMatches(t, s, files, root, pluginv1.Profile_PROFILE_PRODUCTION, nil, []string{"compile"})
	if _, err := s.Projects(t.Context()); err != nil {
		t.Fatal(err)
	}
	buildMatches(t, s, files, root, profile, []string{"m0002/"}, []string{"format"})
}

func TestRetainedBuildGlobalCycleIsNotHiddenByTargetsOrFilters(t *testing.T) {
	root := retainedFixture(t, 2)
	writeCycle(t, root, "outside")
	s, files := buildSession(t, root)
	if _, err := s.Facts(t.Context()); err != nil {
		t.Fatalf("graph query must allow plan-only cycle: %v", err)
	}
	got := buildMatches(t, s, files, root, pluginv1.Profile_PROFILE_DEVELOPMENT, []string{"m0001/"}, []string{"compile"})
	// The evaluation is kept, as the graph query above was answered from
	// it; no plan of it is, since none could be made.
	if got.Needed != nil || s.core.planValid {
		t.Fatal("global cycle left usable selected state")
	}
	retainedWrite(t, root, "m0000/catalog-info.yaml", `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: outside
spec:
  type: service
  lifecycle: experimental
  owner: team
`)
	if repaired := buildMatches(t, s, files, root, pluginv1.Profile_PROFILE_DEVELOPMENT, []string{"m0001/"}, []string{"compile"}); len(repaired.Needed) == 0 {
		t.Fatal("repair control did not execute selection")
	}
}

func TestRetainedBuildWorkMatchesSuccessfulPluginSpansOnSuccessAndFailure(t *testing.T) {
	root := retainedFixture(t, 2)
	s, files := buildSession(t, root)
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.Background())
	seen := map[string]bool{}
	call := func(name string, only []string, wantError, wantPlan bool) BuildPlan {
		t.Helper()
		ctx, parent := telemetry.Root(t.Context(), provider, name)
		got, err := s.Build(ctx, retainedTake(t, files, root), pluginv1.Profile_PROFILE_DEVELOPMENT, nil, nil, only)
		parent.End()
		if (err != nil) != wantError {
			t.Fatalf("%s: %v", name, err)
		}
		var evaluation, planning sdktrace.ReadOnlySpan
		successfulPlugins := 0
		for _, span := range recorder.Ended() {
			if span.SpanContext().TraceID() != parent.SpanContext().TraceID() {
				continue
			}
			switch span.Name() {
			case "evaluate":
				if evaluation != nil {
					t.Fatal("duplicate evaluate span")
				}
				evaluation = span
			case "plan":
				if planning != nil {
					t.Fatal("duplicate plan span")
				}
				planning = span
			case "plugin":
				if span.Status().Code != otelcodes.Error {
					successfulPlugins++
				}
			}
		}
		if evaluation == nil || (planning != nil) != wantPlan {
			t.Fatalf("%s omitted expected stage spans", name)
		}
		if got.Work.Asked != successfulPlugins {
			t.Fatalf("%s Work.Asked=%d successful plugin spans=%d", name, got.Work.Asked, successfulPlugins)
		}
		for _, span := range []sdktrace.ReadOnlySpan{evaluation, planning} {
			if span == nil {
				continue
			}
			id := span.SpanContext().SpanID().String()
			if seen[id] {
				t.Fatal("retained a previous stage span")
			}
			seen[id] = true
			if span.Parent().SpanID() != parent.SpanContext().SpanID() {
				t.Fatal("stage span escaped this request")
			}
			attrs := map[attribute.Key]attribute.Value{}
			for _, a := range span.Attributes() {
				attrs[a.Key] = a.Value
			}
			if _, ok := attrs["retained"]; !ok {
				t.Fatal("missing retained attribute")
			}
			if span == evaluation && (attrs["asked"].AsInt64() != int64(got.Work.Asked) || attrs["again"].AsInt64() != int64(got.Work.Again) || attrs["retained"].AsBool() != got.Work.Retained) {
				t.Fatal("stage counters differ from this operation")
			}
			if span == planning && attrs["retained"].AsBool() != got.Work.PlanRetained {
				t.Fatal("plan retained attribute differs")
			}
		}
		failed := evaluation
		if planning != nil {
			failed = planning
		}
		if (failed.Status().Code == otelcodes.Error) != wantError {
			t.Fatalf("%s stage error status differs", name)
		}
		if err != nil && (got.Needed != nil || got.Asked != nil) {
			t.Fatal("failed Build returned partial plan")
		}
		return got
	}
	if cold := call("cold", nil, false, true); cold.Work.Asked == 0 || cold.Work.Again != 0 {
		t.Fatalf("cold control: %+v", cold.Work)
	}
	if warm := call("warm", nil, false, true); warm.Work != (Work{Retained: true, PlanRetained: true}) {
		t.Fatalf("warm: %+v", warm.Work)
	}
	retainedWrite(t, root, "m0000/go.mod", "module example.com/m0000\n\ngo 1.27\n// equal response\n")
	if equal := call("equal", nil, false, true); equal.Work != (Work{Asked: 1, Retained: true, PlanRetained: true}) {
		t.Fatalf("equal: %+v", equal.Work)
	}
	if refused := call("filter refusal", []string{"unknown"}, true, true); refused.Work.Asked != 0 || !refused.Work.Retained || refused.Work.PlanRetained {
		t.Fatalf("filter error: %+v", refused.Work)
	}
	retainedWrite(t, root, "m0001/go.mod", "invalid module\n")
	if failed := call("partial successful queries", nil, true, false); failed.Work.Asked+failed.Work.Again == 0 {
		t.Fatal("failed evaluation lost successful-query observations")
	}
	retainedWrite(t, root, "m0001/go.mod", "module example.com/m0001\n\ngo 1.27\n\nrequire example.com/m0000 v0.0.0\n")
	// Put back as it was evaluated: what was kept through the failure is
	// held to the source again, and is what a fresh evaluation says (call).
	if repair := call("repair", nil, false, true); repair.Work != (Work{Retained: true, PlanRetained: true}) {
		t.Fatalf("repair: %+v", repair.Work)
	}
	if last := call("later warm", nil, false, true); last.Work != (Work{Retained: true, PlanRetained: true}) {
		t.Fatalf("cost leaked from earlier error: %+v", last.Work)
	}
}

func TestRetainedBuildCancellationAndCloseJoinRunOwnedSource(t *testing.T) {
	root := retainedFixture(t, 2)
	s, files := buildSession(t, root)
	gate := &sessionGate{Source: retainedTake(t, files, root), entered: make(chan struct{}), cancelled: make(chan struct{}), cleanup: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var release sync.Once
	defer release.Do(func() { close(gate.cleanup) })
	type reply struct {
		plan BuildPlan
		err  error
	}
	active := make(chan reply, 1)
	go func() {
		out, err := s.Build(ctx, gate, pluginv1.Profile_PROFILE_DEVELOPMENT, nil, nil, []string{"compile"})
		active <- reply{out, err}
	}()
	sessionSignal(t, gate.entered, "Build did not use supplied source")
	peer := make(chan error, 1)
	go func() { _, err := s.Projects(t.Context()); peer <- err }()
	cancel()
	sessionSignal(t, gate.cancelled, "Build context did not cancel source")
	release.Do(func() { close(gate.cleanup) })
	first := <-active
	if !errors.Is(first.err, context.Canceled) || first.plan.Needed != nil {
		t.Fatalf("cancelled Build: %+v %v", first.plan.Work, first.err)
	}
	select {
	case err := <-peer:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled Build poisoned graph peer")
	}
	warm := &sessionGate{Source: retainedTake(t, files, root), warm: true, entered: make(chan struct{}), cancelled: make(chan struct{}), cleanup: make(chan struct{})}
	var releaseWarm sync.Once
	defer releaseWarm.Do(func() { close(warm.cleanup) })
	go func() {
		out, err := s.Build(t.Context(), warm, pluginv1.Profile_PROFILE_DEVELOPMENT, nil, nil, nil)
		active <- reply{out, err}
	}()
	sessionSignal(t, warm.entered, "warm Build did not validate source")
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	sessionSignal(t, warm.cancelled, "Close did not cancel run-owned validation")
	select {
	case <-closed:
		t.Fatal("Close did not join run-owned source cleanup")
	default:
	}
	releaseWarm.Do(func() { close(warm.cleanup) })
	sessionSignal(t, closed, "Close did not finish")
	last := <-active
	if !errors.Is(last.err, context.Canceled) || last.plan.Needed != nil {
		t.Fatal("closed Build returned a selection")
	}
	assertForgotten(t, &s.core)
	if _, err := s.Build(t.Context(), retainedTake(t, files, root), pluginv1.Profile_PROFILE_DEVELOPMENT, nil, nil, nil); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Build after Close: %v", err)
	}
}
