package evaluate

import (
	"bytes"
	"context"
	"errors"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"google.golang.org/protobuf/proto"
)

type sessionGate struct {
	Source
	warm                        bool
	once                        sync.Once
	entered, cancelled, cleanup chan struct{}
}

func (s *sessionGate) block(ctx context.Context) error {
	s.once.Do(func() { close(s.entered); <-ctx.Done(); close(s.cancelled); <-s.cleanup })
	return ctx.Err()
}
func (s *sessionGate) Read(ctx context.Context, repository, path string) ([]byte, bool, error) {
	if !s.warm && path == "m0000/go.mod" {
		return nil, false, s.block(ctx)
	}
	return s.Source.Read(ctx, repository, path)
}
func (s *sessionGate) Digest(ctx context.Context, repository, path string) (string, bool, error) {
	if s.warm {
		return "", false, s.block(ctx)
	}
	return s.Source.Digest(ctx, repository, path)
}

func sessionSignal(t *testing.T, channel <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(5 * time.Second):
		t.Fatal(what)
	}
}

// cancelledWithin waits for what a cancelled call returned, and fails
// the test where it is not context.Canceled, saying what the call was,
// or where it takes more than five seconds, with hung.
func cancelledWithin(t testing.TB, result <-chan error, what, hung string) {
	t.Helper()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal(hung)
	}
}

func TestRetainedSessionCloseJoinsColdWarmQueuedAndConcurrentClose(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold", true: "warm"}[warm], func(t *testing.T) {
			root, plugins, files := sessionFixture(t, 2)
			gate := &sessionGate{Source: retainedTake(t, files, root), warm: warm, entered: make(chan struct{}), cancelled: make(chan struct{}), cleanup: make(chan struct{})}
			var armed atomic.Bool
			var acquisitions atomic.Int32
			s := newSession(t, plugins, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
				acquisitions.Add(1)
				if armed.Load() {
					return gate, nil, nil
				}
				snapshot, err := files.Take(ctx, source.Tree{Root: root, Paths: map[string]string{"": "."}})
				return snapshot, nil, err
			})
			// Unblock delayed cleanup before cleanup Close if an assertion fails.
			var releaseOnce sync.Once
			defer s.Close()
			defer releaseOnce.Do(func() { close(gate.cleanup) })
			if warm {
				if _, err := s.Projects(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			armed.Store(true)
			active, queued := make(chan error, 1), make(chan error, 1)
			go func() { _, err := s.Facts(t.Context()); active <- err }()
			sessionSignal(t, gate.entered, "evaluation did not reach gate")
			beforeQueued := acquisitions.Load()
			go func() { _, err := s.Projects(t.Context()); queued <- err }()
			sessionAdmissionWaitRegistered(t, s, 2)
			closed := []chan struct{}{make(chan struct{}), make(chan struct{})}
			for _, done := range closed {
				go func() { s.Close(); close(done) }()
			}
			sessionSignal(t, gate.cancelled, "Close did not cancel evaluation")
			for _, done := range closed {
				select {
				case <-done:
					t.Fatal("Close returned before delayed evaluator cleanup")
				default:
				}
			}
			cancelledWithin(t, queued, "queued", "queued evaluation not cancelled")
			before := acquisitions.Load()
			if before != beforeQueued {
				t.Fatal("queued caller acquired source before being cancelled")
			}
			if _, err := s.Projects(t.Context()); !errors.Is(err, ErrSessionClosed) {
				t.Fatalf("admission during Close: %v", err)
			}
			if acquisitions.Load() != before {
				t.Fatal("closed admission called source provider")
			}
			releaseOnce.Do(func() { close(gate.cleanup) })
			for _, done := range closed {
				sessionSignal(t, done, "concurrent Close did not join")
			}
			if err := <-active; !errors.Is(err, context.Canceled) {
				t.Fatalf("active: %v", err)
			}
			stats, err := s.Stats(t.Context())
			if err != nil || stats.Records != 0 || stats.WireBytes != 0 {
				t.Fatalf("state after Close: %+v %v", stats, err)
			}
			assertForgotten(t, &s.core)
		})
	}
}

func TestRetainedSessionSourceCancellationDoesNotFailIndependentAcquisition(t *testing.T) {
	root, plugins, files := sessionFixture(t, 2)
	entered := make(chan struct{})
	var calls atomic.Int32
	s := newSession(t, plugins, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return nil, nil, ctx.Err()
		}
		snapshot, err := files.Take(ctx, source.Tree{Root: root, Paths: map[string]string{"": "."}})
		return snapshot, nil, err
	})
	defer s.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := s.Facts(ctx); first <- err }()
	sessionSignal(t, entered, "first source acquisition missing")
	peer := make(chan *buildv1.ListProjectsResponse, 1)
	peerErr := make(chan error, 1)
	go func() { got, err := s.Projects(t.Context()); peer <- got; peerErr <- err }()
	sessionAdmissionWaitRegistered(t, s, 2)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	got := <-peer
	if err := <-peerErr; err != nil {
		t.Fatal(err)
	}
	want := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), typev1.Profile_PROFILE_DEVELOPMENT).ProjectList()
	if !proto.Equal(got, want) {
		t.Fatal("independent acquisition changed result")
	}
	if _, err := s.Facts(t.Context()); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats(t.Context())
	if err != nil || stats.Evaluations != 1 || stats.Plans != 0 {
		t.Fatalf("cancelled source poisoned peer reuse: %+v %v", stats, err)
	}
}

func TestRetainedSessionSuppliedEntitiesAndProviderErrorsMatchFresh(t *testing.T) {
	root, plugins, files := sessionFixture(t, 2)
	entities := []record.Located{{Ref: entity.New("component", "default", "first"), Root: "m0000"}}
	var failure error
	s := newSession(t, plugins, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
		if failure != nil {
			return nil, nil, failure
		}
		snapshot, err := files.Take(ctx, source.Tree{Root: root, Paths: map[string]string{"": "."}})
		return snapshot, slices.Clone(entities), err
	})
	defer s.Close()
	variants := [][]record.Located{
		entities,
		{{Ref: entity.New("component", "other", "first"), Root: "m0000"}},
		{{Ref: entity.New("component", "other", "first"), Root: "m0001"}},
		{{Ref: entity.New("component", "other", "first"), Repository: "absent", Root: "m0001"}},
		{{Ref: entity.New("component", "default", "a"), Root: "m0000"}, {Ref: entity.New("component", "default", "b"), Root: "m0000"}},
		nil,
	}
	for i, variant := range variants {
		entities = slices.Clone(variant)
		got, gotErr := s.Facts(t.Context())
		want, wantErr := Evaluate(t.Context(), retainedTake(t, &source.Keeper{}, root), plugins, &Answers{}, typev1.Profile_PROFILE_DEVELOPMENT, slices.Clone(variant))
		if (gotErr == nil) != (wantErr == nil) || gotErr != nil && gotErr.Error() != wantErr.Error() {
			t.Fatalf("variant %d: retained=%v fresh=%v", i, gotErr, wantErr)
		}
		if wantErr == nil {
			a, b := retainedSemantics(t, got, want)
			if !bytes.Equal(a, b) || got.Asked != 0 || got.Again != 0 {
				t.Fatalf("variant %d changed facts or returned run counters", i)
			}
			if len(entities) > 0 {
				entities[0].Ref.Name = "caller"
				if got.Entities[0].Ref.Name == "caller" {
					t.Fatal("facts borrow supplied records")
				}
			}
		}
	}
	failure = errors.New("source unavailable before admission")
	if _, err := s.Facts(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("source error changed: %v", err)
	}
	failure = nil
	if _, err := s.Projects(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedSessionFactsOwnDataAndFixedPluginSlice(t *testing.T) {
	root := retainedFixture(t, 2)
	plugins := retainedPlugins(t)
	passed := slices.Clone(plugins)
	files := &source.Keeper{}
	s := newSession(t, passed, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, takesTree(files, root))
	defer s.Close()
	clear(passed)
	first, err := s.Facts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if s.core.result.evaluation.Asked == 0 || first.Asked != 0 || first.Again != 0 {
		t.Fatal("counter-reset control missing")
	}
	want, err := retainedSemantic(first)
	if err != nil {
		t.Fatal(err)
	}
	first.Actions[0].Declared.Name = "caller"
	first.Projects[0].Provides[0].Sources[0] = "caller"
	first.Listed[""][0] = "caller"
	changed, err := retainedSemantic(first)
	if err != nil || bytes.Equal(changed, want) {
		t.Fatal("mutation control ineffective")
	}
	second, err := s.Facts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got, err := retainedSemantic(second)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("facts mutation reached session")
	}
	stats, err := s.Stats(t.Context())
	if err != nil || stats.Evaluations != 1 || stats.Plans != 0 {
		t.Fatalf("facts unexpectedly rebuilt or planned: %+v %v", stats, err)
	}
	s.Close()
	after, err := retainedSemantic(second)
	if err != nil || !bytes.Equal(after, want) {
		t.Fatal("Close damaged caller-owned facts")
	}
}
