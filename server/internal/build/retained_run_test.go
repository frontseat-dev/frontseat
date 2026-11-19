package build

import (
	connect "connectrpc.com/connect/v2"
	"context"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/evaluate"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"testing"
	"time"
)

func retainedRun(t *testing.T, c clients, request *buildv1.CreateBuildRequest) *buildv1.Build {
	t.Helper()
	made, err := c.runs.CreateBuild(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	got := wait(t, c.runs, made.Build.Id)
	if got.GetExitCode().GetName() != "SUCCESS" {
		t.Fatalf("run failed: %v", got.GetFailure())
	}
	return got
}
func lastRetainedSpan(t *testing.T, recorder *tracetest.SpanRecorder, name string) bool {
	t.Helper()
	all := recorder.Ended()
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Name() == name {
			for _, a := range all[i].Attributes() {
				if a.Key == "retained" {
					return a.Value.AsBool()
				}
			}
			t.Fatalf("%s span omitted retained-work observation", name)
		}
	}
	t.Fatalf("missing %s span", name)
	return false
}
func TestRetainedRunSharesGraphEvaluationAndPlan(t *testing.T) {
	recorder, signals := recording(t)
	c, g := serveWithGraph(t, configuration{Workspace: oneModule(t), Mounted: true, Executor: scripted{}, Telemetry: signals})
	if _, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{}); err != nil {
		t.Fatal(err)
	}
	for round := range 2 {
		got := retainedRun(t, c, &buildv1.CreateBuildRequest{})
		if len(actions(t, c, got)) != 3 {
			t.Fatal("control did not plan module actions")
		}
		stats := sessionStats(t, c)
		if stats.Evaluations != 1 || stats.Plans != 1 {
			t.Fatalf("round %d: graph and runs did not reuse evaluation/plan: %+v", round, stats)
		}
		if cost := costOf(t, recorder); cost.asked != 0 || cost.again != 0 {
			t.Fatalf("retained value replayed old evaluation counters: %+v", cost)
		}
		if !lastRetainedSpan(t, recorder, "evaluate") || lastRetainedSpan(t, recorder, "plan") != (round > 0) {
			t.Fatal("stage reuse observation differs")
		}
	}
}

func TestRetainedRunShutdownClosesRunAdmissionBeforeJoiningGraph(t *testing.T) {
	c, g := serveWithGraph(t, configuration{Workspace: oneModule(t), Mounted: true, Executor: scripted{}})
	c.daemon.graph.session.Close()
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	session, err := evaluate.NewSession(c.daemon.runs.plugins, evaluate.RetentionLimits{Records: retainedRecords, WireBytes: retainedWireBytes}, graphRequestsAdmitted, &evaluate.Answers{}, func(ctx context.Context) (evaluate.Source, []record.Located, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		return nil, nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	c.daemon.graph.session, c.daemon.runs.evaluation = session, session
	query := make(chan error, 1)
	go func() { _, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{}); query <- err }()
	<-entered
	ended := make(chan struct{})
	go func() { c.daemon.Close(); close(ended) }()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("graph did not cancel")
	}
	_, err = c.runs.CreateBuild(t.Context(), &buildv1.CreateBuildRequest{})
	close(release)
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not join")
	}
	<-query
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("run admission remained open during shutdown: %v", err)
	}
}
