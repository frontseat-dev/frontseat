package evaluate

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
)

func TestGraphSourceAdmissionDoesNotBlockRunOwnedEvaluation(t *testing.T) {
	root, plugins, files := sessionFixture(t, 2)
	entered := make(chan struct{})
	var acquisitions atomic.Int32
	s := newSession(t, plugins, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
		if acquisitions.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return nil, nil, ctx.Err()
		}
		src, err := files.Take(ctx, source.Tree{Root: root, Paths: map[string]string{"": "."}})
		return src, nil, err
	})
	defer s.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	graph := make(chan error, 1)
	go func() { _, err := s.Projects(ctx); graph <- err }()
	sessionSignal(t, entered, "graph did not begin source acquisition")
	// The build already owns its source. A slow graph snapshot must not hold
	// the evaluator gate and prevent this real Wasm evaluation from finishing.
	building, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	got, err := s.Build(building, retainedTake(t, files, root), typev1.Profile_PROFILE_DEVELOPMENT, nil, nil, []string{"compile"})
	if err != nil {
		t.Fatalf("build blocked by graph source acquisition: %v", err)
	}
	if len(got.Needed) == 0 || got.Work.Retained || got.Work.Asked == 0 {
		t.Fatalf("build did not perform real cold evaluation: %+v", got.Work)
	}
	if len(graph) != 0 {
		t.Fatal("graph source unexpectedly finished before cancellation")
	}
	cancel()
	if err := <-graph; !errors.Is(err, context.Canceled) {
		t.Fatalf("graph cancellation: %v", err)
	}
	projectsMatch(t, s, root, typev1.Profile_PROFILE_DEVELOPMENT, "graph differs from ordinary evaluation after source cancellation")
	stats, err := s.Stats(t.Context())
	if err != nil || stats.Evaluations != 1 {
		t.Fatalf("cancelled acquisition poisoned the build's retained evaluation: %+v %v", stats, err)
	}
}

func TestGraphSourceAdmissionPreservesTheRunsStartingSnapshot(t *testing.T) {
	root := retainedFixture(t, 2)
	s, files := buildSession(t, root)
	profile := typev1.Profile_PROFILE_DEVELOPMENT
	if _, err := s.Projects(t.Context()); err != nil {
		t.Fatal(err)
	}
	old := retainedTake(t, files, root)
	// A build's existing snapshot must not silently be replaced when it
	// reaches admission after source has changed.
	retainedWrite(t, root, "m0001/go.mod", "module example.com/new-identity\n\ngo 1.27\n")
	_, err := s.Build(t.Context(), old, profile, nil, nil, []string{"compile"})
	var changed *source.Changed
	if !errors.As(err, &changed) {
		t.Fatalf("old run snapshot was replaced or accepted: %v", err)
	}
	projectsMatch(t, s, root, profile, "graph did not reacquire source after the old run was refused")
	buildMatches(t, s, files, root, profile, nil, []string{"compile"})
}
