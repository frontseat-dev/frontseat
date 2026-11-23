package evaluate

import (
	"context"
	"errors"
	"fmt"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/record"
	"google.golang.org/protobuf/proto"
)

type admissionKey struct{}

// Tried with one admitted and with the two a daemon admits: as many own
// source as are admitted, and the waiter at the head of the rest gives up.
func TestRetainedAdmissionExpiredHeadWaiterPreservesLiveContext(t *testing.T) {
	for _, admitted := range []int{1, 2} {
		t.Run(fmt.Sprint(admitted), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				var releaseOnce sync.Once
				calls := make(chan string, 8)
				failure := errors.New("controlled provider failure")
				s := newSession(t, nil, RetentionLimits{8192, 8 << 20}, admitted, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
					id, _ := ctx.Value(admissionKey{}).(string)
					calls <- id
					if id == "owner" {
						select {
						case <-release:
						case <-ctx.Done():
							return nil, nil, ctx.Err()
						}
					}
					return nil, nil, failure
				})
				defer s.Close()
				defer releaseOnce.Do(func() { close(release) })
				owner := make(chan error, admitted)
				for range admitted {
					go func() {
						_, err := s.Projects(context.WithValue(context.Background(), admissionKey{}, "owner"))
						owner <- err
					}()
				}
				synctest.Wait()
				for range admitted {
					if id := <-calls; id != "owner" {
						t.Fatalf("wrong active context: %q", id)
					}
				}
				expiring, cancel := context.WithTimeout(context.WithValue(context.Background(), admissionKey{}, "expired"), time.Second)
				defer cancel()
				expired, live := make(chan error, 1), make(chan error, 1)
				go func() { _, err := s.Facts(expiring); expired <- err }()
				synctest.Wait() // Establish the expiring request at the front of the queue.
				go func() {
					_, err := s.Facts(context.WithValue(context.Background(), admissionKey{}, "live"))
					live <- err
				}()
				synctest.Wait()
				if len(calls) != 0 {
					t.Fatal("queued caller acquired source")
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if err := <-expired; !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("deadline changed: %v", err)
				}
				if len(owner) != 0 || len(live) != 0 || len(calls) != 0 {
					t.Fatal("deadline disturbed a peer or invoked the expired provider")
				}
				releaseOnce.Do(func() { close(release) })
				synctest.Wait()
				for range admitted {
					if err := <-owner; !errors.Is(err, failure) {
						t.Fatalf("live operation lost its own error: %v", err)
					}
				}
				if err := <-live; !errors.Is(err, failure) {
					t.Fatalf("live operation lost its own error: %v", err)
				}
				if id := <-calls; id != "live" {
					t.Fatalf("permit transferred to wrong context: %q", id)
				}
				if _, err := s.Projects(context.WithValue(context.Background(), admissionKey{}, "after")); !errors.Is(err, failure) {
					t.Fatal(err)
				}
				if id := <-calls; id != "after" {
					t.Fatalf("permit was not reusable after failure: %q", id)
				}
				if len(s.active) != 0 {
					t.Fatal("completed calls remain registered")
				}
			})
		})
	}
}

// Tried with one admitted and with the two a daemon admits.
func TestRetainedAdmissionCloseJoinsProviderCleanupAndBothQueues(t *testing.T) {
	for _, admitted := range []int{1, 2} {
		t.Run(fmt.Sprint(admitted), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cleaned := make(chan struct{})
				var cleanOnce sync.Once
				var acquired atomic.Int32
				s := newSession(t, nil, RetentionLimits{8192, 8 << 20}, admitted, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
					acquired.Add(1)
					<-ctx.Done()
					<-cleaned // Cleanup continues to own its source dependency.
					return nil, nil, ctx.Err()
				})
				release, err := s.core.admit(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var releaseOnce sync.Once
				defer s.Close()
				defer releaseOnce.Do(release)
				defer cleanOnce.Do(func() { close(cleaned) })
				active := make(chan error, admitted)
				for range admitted {
					go func() { _, err := s.Projects(context.Background()); active <- err }()
				}
				synctest.Wait()
				const graphWaiters, buildWaiters = 16, 4
				results := make(chan error, graphWaiters+buildWaiters)
				for i := range graphWaiters {
					go func() {
						var err error
						if i%2 == 0 {
							_, err = s.Facts(context.Background())
						} else {
							_, err = s.Projects(context.Background())
						}
						results <- err
					}()
				}
				for range buildWaiters {
					go func() {
						_, err := s.Build(context.Background(), nil, typev1.Profile_PROFILE_DEVELOPMENT, nil, nil, nil)
						results <- err
					}()
				}
				synctest.Wait()
				if got := len(s.active); got != admitted+graphWaiters+buildWaiters {
					t.Fatalf("registered %d calls", got)
				}
				if int(acquired.Load()) != admitted {
					t.Fatal("waiting graph request acquired a source")
				}
				// One Close in this synctest bubble: concurrent sync.Once waiters
				// use mutex blocking, which synctest cannot classify as durable.
				// The existing real-Wasm lifecycle test covers concurrent Close.
				closes := make(chan struct{}, 1)
				go func() { s.Close(); closes <- struct{}{} }()
				synctest.Wait()
				for range graphWaiters + buildWaiters {
					if err := <-results; !errors.Is(err, context.Canceled) {
						t.Fatalf("queued call on Close: %v", err)
					}
				}
				releaseOnce.Do(release)
				synctest.Wait()
				if len(closes) != 0 || len(active) != 0 {
					t.Fatal("Close released dependencies before provider cleanup joined")
				}
				if s.graphSource.TryAcquire(1) {
					s.graphSource.Release(1)
					t.Fatal("source permit was released during provider cleanup")
				}
				if int(acquired.Load()) != admitted {
					t.Fatal("Close caused another provider acquisition")
				}
				if _, err := s.Projects(context.Background()); !errors.Is(err, ErrSessionClosed) {
					t.Fatalf("closed graph admission: %v", err)
				}
				if _, err := s.Build(context.Background(), nil, 0, nil, nil, nil); !errors.Is(err, ErrSessionClosed) {
					t.Fatalf("closed build admission: %v", err)
				}
				cleanOnce.Do(func() { close(cleaned) })
				synctest.Wait()
				for range admitted {
					if err := <-active; !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				}
				<-closes
				if len(s.active) != 0 {
					t.Fatal("Close left registered calls")
				}
				if !s.graphSource.TryAcquire(int64(admitted)) {
					t.Fatal("completed shutdown leaked a graph permit")
				}
				s.graphSource.Release(int64(admitted))
				assertForgotten(t, &s.core)
			})
		})
	}
}

func TestRetainedAdmissionBlockedSessionDoesNotBlockAnother(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var acquired atomic.Int32
		first := newSession(t, nil, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
			acquired.Add(1)
			<-ctx.Done()
			return nil, nil, ctx.Err()
		})
		defer first.Close()
		failure := errors.New("independent owner")
		second := newSession(t, nil, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(context.Context) (Source, []record.Located, error) { return nil, nil, failure })
		defer second.Close()
		result := make(chan error, 1)
		go func() { _, err := first.Facts(context.Background()); result <- err }()
		synctest.Wait()
		if acquired.Load() != 1 {
			t.Fatal("first owner never reached acquisition")
		}
		if _, err := second.Projects(context.Background()); !errors.Is(err, failure) {
			t.Fatalf("independent owner blocked or failed: %v", err)
		}
		if len(result) != 0 {
			t.Fatal("independent owner disturbed active source")
		}
		first.Close()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := second.Facts(context.Background()); !errors.Is(err, failure) {
			t.Fatalf("closing one owner poisoned another: %v", err)
		}
	})
}

func TestRetainedAdmissionQueuedGraphTakesFreshSourceAfterRefusal(t *testing.T) {
	root, plugins, files := sessionFixture(t, 2)
	captured := make(chan struct{})
	var acquired atomic.Int32
	s := newSession(t, plugins, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
		src, err := files.Take(ctx, source.Tree{Root: root, Paths: map[string]string{"": "."}})
		if acquired.Add(1) == 1 {
			close(captured)
		}
		return src, nil, err
	})
	defer s.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	release, err := s.core.admit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	defer releaseOnce.Do(release)
	first := make(chan error, 1)
	go func() { _, err := s.Projects(ctx); first <- err }()
	sessionSignal(t, captured, "first graph did not take its source")
	type answer struct {
		projects *buildv1.ListProjectsResponse
		err      error
	}
	second := make(chan answer, 1)
	go func() { got, err := s.Projects(ctx); second <- answer{got, err} }()
	sessionAdmissionWaitRegistered(t, s, 2)
	if acquired.Load() != 1 {
		t.Fatal("queued caller captured source before admission")
	}
	retainedWrite(t, root, "m0001/go.mod", "module example.com/changed-while-queued\n\ngo 1.27\n")
	releaseOnce.Do(release)
	var changed *source.Changed
	if err := <-first; !errors.As(err, &changed) {
		t.Fatalf("pre-edit snapshot was silently accepted or replaced: %v", err)
	}
	got := <-second
	if got.err != nil {
		t.Fatalf("live queued caller did not recover after refusal: %v", got.err)
	}
	if acquired.Load() != 2 {
		t.Fatalf("expected one fresh acquisition per admitted request, got %d", acquired.Load())
	}
	want := projectionFresh(t, retainedTake(t, &source.Keeper{}, root), typev1.Profile_PROFILE_DEVELOPMENT).ProjectList()
	if !proto.Equal(got.projects, want) {
		t.Fatal("queued caller returned pre-admission facts")
	}
	if len(got.projects.GetProjects()) == 0 {
		t.Fatal("mutation control requires a nonempty result")
	}
	got.projects.Projects[0].Ref.Root = "caller-mutated-root"
	if proto.Equal(got.projects, want) {
		t.Fatal("mutation control did not change returned facts")
	}
	again, err := s.Projects(ctx)
	if err != nil || !proto.Equal(again, want) {
		t.Fatalf("refusal or caller mutation poisoned later reuse: %v", err)
	}
	stats, err := s.Stats(ctx)
	if err != nil || stats.Evaluations != 2 {
		t.Fatalf("refused, rebuilt, reused control: %+v %v", stats, err)
	}
}
