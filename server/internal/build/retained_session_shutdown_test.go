package build

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/evaluate"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRetainedSessionDaemonJoinsGraphBeforeReleasingDependencies(t *testing.T) {
	c, g := serveWithGraph(t, configuration{Workspace: oneModule(t), Mounted: true, Executor: scripted{}})
	c.daemon.graph.session.Close()
	entered, cancelled, cleanup, released := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var providerCalls atomic.Int32
	session, err := evaluate.NewSession(c.daemon.runs.plugins, evaluate.RetentionLimits{Records: retainedRecords, WireBytes: retainedWireBytes}, graphRequestsAdmitted, &evaluate.Answers{}, func(ctx context.Context) (evaluate.Source, []record.Located, error) {
		providerCalls.Add(1)
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-cleanup
		return nil, nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	c.daemon.graph.session = session
	// Observe the daemon's actual dependency-release hook; shared fixture
	// plugins are not destroyed by this test.
	c.daemon.release = func() { close(released) }
	var cleanupOnce sync.Once
	defer cleanupOnce.Do(func() { close(cleanup) })
	result := make(chan error, 1)
	go func() { _, err := g.ListEntities(t.Context(), &buildv1.ListEntitiesRequest{}); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Connect request did not acquire source")
	}
	closed := []chan struct{}{make(chan struct{}), make(chan struct{})}
	for _, done := range closed {
		go func() { c.daemon.Close(); close(done) }()
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon close did not cancel graph source")
	}
	select {
	case <-released:
		t.Fatal("dependencies released before source cleanup")
	default:
	}
	for _, done := range closed {
		select {
		case <-done:
			t.Fatal("daemon Close returned before source cleanup")
		default:
		}
	}
	if _, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("closing admission: %v", err)
	}
	if providerCalls.Load() != 1 {
		t.Fatal("closed request reacquired source")
	}
	cleanupOnce.Do(func() { close(cleanup) })
	for _, done := range closed {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("daemon close did not join")
		}
	}
	select {
	case <-released:
	default:
		t.Fatal("joined daemon did not release dependencies")
	}
	if err := <-result; connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("active RPC: %v", err)
	}
}

func TestRetainedSessionSourceStageErrorsKeepCanonicalCodeAndDetails(t *testing.T) {
	c, g := serveWithGraph(t, configuration{Workspace: oneModule(t), Mounted: true, Executor: scripted{}})
	c.daemon.graph.session.Close()
	detailed, err := status.New(codes.PermissionDenied, "denied at source").WithDetails(&errdetails.ErrorInfo{Reason: "SOURCE_CONTROL", Domain: "review"})
	if err != nil {
		t.Fatal(err)
	}
	var failure error
	var failureMu sync.Mutex
	session, err := evaluate.NewSession(c.daemon.runs.plugins, evaluate.RetentionLimits{Records: retainedRecords, WireBytes: retainedWireBytes}, graphRequestsAdmitted, &evaluate.Answers{}, func(ctx context.Context) (evaluate.Source, []record.Located, error) {
		failureMu.Lock()
		current := failure
		failureMu.Unlock()
		if current != nil {
			return nil, nil, current
		}
		return c.daemon.graph.source(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	c.daemon.graph.session = session
	if _, err := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		err      error
		fallback codes.Code
	}{
		{errors.New("workspace is not ready"), codes.FailedPrecondition},
		{errors.New("snapshot unavailable"), codes.Internal},
		{context.Canceled, codes.Internal},
		{context.DeadlineExceeded, codes.Internal},
		{detailed.Err(), codes.Internal},
	} {
		failureMu.Lock()
		failure = &graphSourceError{error: tc.err, code: tc.fallback}
		failureMu.Unlock()
		_, projects := g.ListProjects(t.Context(), &buildv1.ListProjectsRequest{})
		_, entities := g.ListEntities(t.Context(), &buildv1.ListEntitiesRequest{})
		reason := ""
		if tc.fallback == codes.FailedPrecondition {
			reason = "WORKSPACE_NOT_READY"
		}
		want := failedRPC(tc.err, tc.fallback, reason)
		sameGraphError(t, projects, want)
		sameGraphError(t, entities, want)
	}
	failureMu.Lock()
	failure = nil
	failureMu.Unlock()
	if _, err := g.ListEntities(t.Context(), &buildv1.ListEntitiesRequest{}); err != nil {
		t.Fatalf("source repair: %v", err)
	}
	stats, err := session.Stats(t.Context())
	if err != nil || stats.Evaluations != 1 || stats.Plans != 0 {
		t.Fatalf("source errors poisoned valid reuse: %+v %v", stats, err)
	}
}
