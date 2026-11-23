package evaluate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"math"
	"sync"
	"testing"
	"time"
)

func TestRetainedLifetimeUsageCountsAliasesAndExpiredPinnedValues(t *testing.T) {
	a := &answer{response: []byte("same")}
	b := &answer{response: []byte("same")}
	c := &answer{response: []byte("pinned")}
	request := []byte("rq")
	p := &retainedEngine{answers: &Answers{}, trace: &retainedTrace{queries: []retainedQuery{
		{request: request, value: a}, {request: request, value: c},
		{request: []byte("req"), value: c}, {},
	}}}
	p.answers.kept = map[string]*answer{"a": a, "alias": a, "b": b}
	p.answers.repositories = map[string]*saying{"first": {}, "second": {}}
	// Three cache slots, two repository records, four queries. Response bytes
	// count a once, b separately despite equal contents, and pinned-only c once.
	// Aliased request slices still count their logical bytes for each query.
	if got := p.retainedUsage(); got != (retentionUsage{Records: 9, WireBytes: 21}) {
		t.Fatalf("alias accounting: %+v", got)
	}
	p.retention = RetentionLimits{Records: 9, WireBytes: 21}
	p.enforceRetention()
	if p.trace == nil || p.evictions != 0 {
		t.Fatal("exact accounting limit evicted")
	}
	p.answers.begin()
	if len(p.answers.kept) != 0 || len(p.answers.repositories) != 0 {
		t.Fatal("expiry control retained cache entries")
	}
	if got := p.retainedUsage(); got != (retentionUsage{Records: 4, WireBytes: 17}) {
		t.Fatalf("trace-pinned answers disappeared from accounting: %+v", got)
	}
	p.retention = RetentionLimits{Records: 4, WireBytes: 16}
	p.enforceRetention()
	assertEvaluationDropped(t, p)
	if p.evictions != 1 || string(a.response) != "same" || string(b.response) != "same" || string(c.response) != "pinned" || string(request) != "rq" {
		t.Fatal("release failed or mutated externally held wire values")
	}
}

func TestRetainedLifetimeLimitsCoverEveryProjection(t *testing.T) {
	for _, limits := range []RetentionLimits{{-1, 0}, {0, -1}, {-1, -1}, {math.MinInt64, math.MaxInt64}} {
		if p, err := retainedLimited(limits); err == nil || p != nil {
			t.Fatalf("negative policy accepted: %+v", limits)
		}
	}
	configured := RetentionLimits{Records: 7, WireBytes: 11}
	configuredEngine, err := retainedLimited(configured)
	if err != nil {
		t.Fatal(err)
	}
	configured = RetentionLimits{}
	if configuredEngine.retention != (RetentionLimits{Records: 7, WireBytes: 11}) {
		t.Fatal("policy borrows caller's mutable value")
	}
	root := retainedFixture(t, 2)
	profile, targets := typev1.Profile_PROFILE_DEVELOPMENT, []string{"m0001/"}
	fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
	for _, kind := range []projectionKind{projectFull, projectProjects, projectPlan} {
		control := newRetainedEngine()
		want := projectionWanted(t, fresh, kind, targets)
		if _, err := control.operation(t.Context(), retainedTake(t, &source.Keeper{}, root), profile, targets, kind); err != nil {
			t.Fatal(err)
		}
		usage := control.retainedUsage()
		if usage.Records < 2 || usage.WireBytes < 2 {
			t.Fatal("nonempty control missing")
		}
		for _, limits := range []RetentionLimits{
			{usage.Records, usage.WireBytes}, {usage.Records - 1, usage.WireBytes},
			{usage.Records, usage.WireBytes - 1}, {0, math.MaxInt64}, {math.MaxInt64, 0},
		} {
			p, err := retainedLimited(limits)
			if err != nil {
				t.Fatal(err)
			}
			files := &source.Keeper{}
			keep := limits.Records == usage.Records && limits.WireBytes == usage.WireBytes
			for range 2 {
				projectionMatches(t, p, retainedTake(t, files, root), profile, targets, kind, want)
				if p.valid != keep {
					t.Fatalf("kind=%d limits=%+v valid=%v", kind, limits, p.valid)
				}
				if !keep {
					assertEvaluationDropped(t, p)
				}
			}
			if keep && (p.evaluations != 1 || p.evictions != 0) || !keep && (p.evaluations != 2 || p.evictions != 2) {
				t.Fatal("retention control did not exercise its expected path")
			}
		}
	}
}

type lifetimeReadGate struct {
	Source
	once             sync.Once
	started, release chan struct{}
}

func (s *lifetimeReadGate) Read(ctx context.Context, repository, path string) ([]byte, bool, error) {
	if path == "m0000/go.mod" {
		s.once.Do(func() {
			close(s.started)
			select {
			case <-s.release:
			case <-ctx.Done():
			}
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return s.Source.Read(ctx, repository, path)
}

func TestRetainedLifetimeRefusalAndCancellationEnforceLimits(t *testing.T) {
	root := retainedFixture(t, 2)
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	p, err := retainedLimited(RetentionLimits{Records: 1, WireBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
	_, wantErr := projectionWant(t, fresh, projectPlan, []string{"missing/"})
	if wantErr == nil {
		t.Fatal("refusal control accepted missing target")
	}
	_, gotErr := p.selectedPlan(t.Context(), retainedTake(t, &source.Keeper{}, root), profile, []string{"missing/"})
	if gotErr == nil || gotErr.Error() != wantErr.Error() {
		t.Fatalf("refusal changed: %v versus %v", gotErr, wantErr)
	}
	assertEvaluationDropped(t, p)
	if p.evictions != 1 {
		t.Fatal("error cleanup did not apply policy to accumulated answers")
	}
	p, err = retainedLimited(RetentionLimits{Records: 0, WireBytes: math.MaxInt64})
	if err != nil {
		t.Fatal(err)
	}
	gate := &lifetimeReadGate{Source: retainedTake(t, &source.Keeper{}, root), started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	var wait sync.WaitGroup
	defer func() { cancel(); wait.Wait() }()
	done := make(chan error, 1)
	wait.Go(func() { _, err := p.projectList(ctx, gate, profile); done <- err })
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("cold evaluation never reached source gate")
	}
	cancel()
	cancelledWithin(t, done, "cancelled", "cancelled evaluation did not join")
	assertForgotten(t, p)
	if p.evictions != 1 {
		t.Fatal("cancellation skipped retention cleanup")
	}
	got, err := p.projectList(t.Context(), retainedTake(t, &source.Keeper{}, root), profile)
	if err != nil {
		t.Fatal(err)
	}
	projectionEqual(t, testProjection{projects: got}, testProjection{projects: fresh.ProjectList()}, projectProjects)
	assertForgotten(t, p)
}

func TestRetainedLifetimeForgetWaitsForColdPublication(t *testing.T) {
	root := retainedFixture(t, 2)
	p, files := newRetainedEngine(), &source.Keeper{}
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	gate := &lifetimeReadGate{Source: retainedTake(t, files, root), started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	var wait sync.WaitGroup
	defer func() { cancel(); wait.Wait() }()
	type returned struct {
		result selectedPlan
		err    error
	}
	done, forgotten, attempted := make(chan returned, 1), make(chan error, 1), make(chan struct{})
	wait.Go(func() { r, err := p.selectedPlan(ctx, gate, profile, []string{"m0001/"}); done <- returned{r, err} })
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("cold evaluation never blocked")
	}
	wait.Go(func() { close(attempted); forgotten <- p.forget(ctx) })
	<-attempted
	select {
	case err := <-forgotten:
		t.Fatalf("forget returned during evaluation: %v", err)
	default:
	}
	close(gate.release)
	var first returned
	select {
	case first = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("evaluation hung")
	}
	if first.err != nil {
		t.Fatal(first.err)
	}
	select {
	case err := <-forgotten:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("forget hung")
	}
	assertForgotten(t, p)
	fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
	want := projectionWanted(t, fresh, projectPlan, []string{"m0001/"})
	projectionEqual(t, testProjection{plan: first.result}, want, projectPlan)
	before := projectionBytes(t, first.result)
	first.result.Needed[0].Declared.Name = "caller after forget"
	if bytes.Equal(before, projectionBytes(t, first.result)) {
		t.Fatal("returned mutation control ineffective")
	}
	next := lifetimeOracle(t, p, files, root)
	if p.evaluations != 2 || !bytes.Equal(before, projectionBytes(t, next)) {
		t.Fatal("release allowed stale publication or damaged reconstruction")
	}
}

func TestRetainedLifetimeCancelledQueuedForgetChangesNothing(t *testing.T) {
	root := retainedFixture(t, 2)
	p, files := newRetainedEngine(), &source.Keeper{}
	lifetimeOracle(t, p, files, root)
	usage, trace := p.retainedUsage(), p.trace
	release, err := p.admit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- p.forget(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			release()
			t.Fatalf("queued forget: %v", err)
		}
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("queued forget did not cancel")
	}
	if !p.valid || p.trace != trace || p.retainedUsage() != usage {
		release()
		t.Fatal("cancelled queued release changed the session")
	}
	release()
	lifetimeOracle(t, p, files, root)
	if p.evaluations != 1 {
		t.Fatal("cancelled forget discarded reuse")
	}
	if err := p.forget(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertForgotten(t, p)
}

func TestRetainedLifetimeReleasePreservesIndependentOwners(t *testing.T) {
	root := retainedFixture(t, 2)
	files := &source.Keeper{}
	p, peer := newRetainedEngine(), newRetainedEngine()
	first := lifetimeOracle(t, p, files, root)
	other := lifetimeOracle(t, peer, files, root)
	before, peerBefore := projectionBytes(t, first), projectionBytes(t, other)
	usage, trace := peer.retainedUsage(), peer.trace
	snapshot := retainedTake(t, files, root)
	if err := p.forget(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertForgotten(t, p)
	if !bytes.Equal(before, projectionBytes(t, first)) || !bytes.Equal(peerBefore, projectionBytes(t, other)) || peer.retainedUsage() != usage || peer.trace != trace {
		t.Fatal("session release changed an independent owner")
	}
	content, found, err := snapshot.Read(t.Context(), "", "m0000/main.go")
	if err != nil || !found || string(content) != "package main\nfunc main() {}\n" {
		t.Fatalf("source snapshot damaged: found=%v err=%v", found, err)
	}
	lifetimeOracle(t, peer, files, root)
	if peer.evaluations != 1 {
		t.Fatal("release discarded peer reuse")
	}
	first.Needed[0].Declared.Name = "caller"
	if bytes.Equal(before, projectionBytes(t, first)) {
		t.Fatal("caller mutation control ineffective")
	}
	lifetimeEdit(t, root, 1)
	lifetimeOracle(t, peer, files, root)
	if peer.evaluations != 2 {
		t.Fatal("changed source did not exercise still-live plugin owner")
	}
	lifetimeOracle(t, p, files, root)
}

func TestRetainedLifetimeConcurrentForgetAndOperations(t *testing.T) {
	root := retainedFixture(t, 3)
	p, err := retainedLimited(RetentionLimits{Records: 1000, WireBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	files := &source.Keeper{}
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	fresh := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), profile)
	type returned struct {
		result, want testProjection
		kind         projectionKind
		err          error
		forgetting   bool
	}
	results := make(chan returned, 16)
	var wait sync.WaitGroup
	for i := range 16 {
		if i%4 == 0 {
			wait.Go(func() { results <- returned{err: p.forget(t.Context()), forgetting: true} })
			continue
		}
		kind, targets := projectionKind(i%3), []string{fmt.Sprintf("m%04d/", i%3)}
		want := projectionWanted(t, fresh, kind, targets)
		snapshot := retainedTake(t, files, root)
		wait.Go(func() {
			got, err := p.operation(t.Context(), snapshot, profile, targets, kind)
			results <- returned{result: got, want: want, kind: kind, err: err}
		})
	}
	wait.Wait()
	close(results)
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if !got.forgetting {
			projectionEqual(t, got.result, got.want, got.kind)
		}
	}
	if usage := p.retainedUsage(); usage.Records > p.retention.Records || usage.WireBytes > p.retention.WireBytes {
		t.Fatal("concurrent operations escaped policy")
	}
	if err := p.forget(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertForgotten(t, p)
}
