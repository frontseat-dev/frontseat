package evaluate

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/frontseat-dev/frontseat/common/record"
)

// Hold the real evaluator gate, so each source a graph request acquires would
// remain owned while it waits. Durable blocking makes this independent of
// sleeps, filesystem timing and the goroutine scheduler's chosen order.
// However many wait, as many own source as the session admits and no more:
// tried at one, at the two a daemon admits, and at four.
func TestQueuedGraphRequestsOwnAsManySourcesAsAreAdmitted(t *testing.T) {
	for _, admitted := range []int{1, 2, 4} {
		t.Run(fmt.Sprint(admitted), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var acquisitions atomic.Int32
				s := newSession(t, nil, RetentionLimits{8192, 8 << 20}, admitted, &Answers{}, func(context.Context) (Source, []record.Located, error) {
					acquisitions.Add(1)
					return nil, nil, nil // Never read: the core remains held until cancellation.
				})
				ctx, cancel, done := heldCore(t, s)
				defer done()
				const requests = 64
				results := make(chan error, requests)
				for i := range requests {
					go func() {
						var err error
						if i%2 == 0 {
							_, err = s.Projects(ctx)
						} else {
							_, err = s.Facts(ctx)
						}
						results <- err
					}()
				}
				synctest.Wait()
				if got := acquisitions.Load(); int(got) != admitted {
					t.Errorf("queued graph requests acquired %d sources; want the %d admitted", got, admitted)
				}
				s.mu.Lock()
				registered := len(s.active)
				s.mu.Unlock()
				if registered != requests {
					t.Errorf("only %d of %d requests reached session admission", registered, requests)
				}
				cancel()
				synctest.Wait()
				for range requests {
					if err := <-results; !errors.Is(err, context.Canceled) {
						t.Errorf("queued cancellation: %v", err)
					}
				}
			})
		})
	}
}

// A session that admits no graph request at all is refused, beside the
// one above that admits one.
func TestASessionAdmitsAtLeastOneGraphRequest(t *testing.T) {
	for _, admitted := range []int{0, -1} {
		if _, err := NewSession(nil, RetentionLimits{8192, 8 << 20}, admitted, &Answers{}, func(context.Context) (Source, []record.Located, error) { return nil, nil, nil }); err == nil {
			t.Errorf("a session that admits %d graph requests was made", admitted)
		}
	}
}

func TestQueuedGraphCancellationLeavesLiveSourcesAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var acquisitions atomic.Int32
		ready := make(chan struct{})
		sourceFailure := errors.New("controlled source failure")
		s := newSession(t, nil, RetentionLimits{8192, 8 << 20}, 1, &Answers{}, func(ctx context.Context) (Source, []record.Located, error) {
			acquisitions.Add(1)
			select {
			case <-ready:
				return nil, nil, sourceFailure
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		})
		defer s.Close()
		first := make(chan error, 1)
		go func() { _, err := s.Projects(context.Background()); first <- err }()
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancelled, live := make(chan error, 1), make(chan error, 1)
		go func() { _, err := s.Facts(ctx); cancelled <- err }()
		go func() { _, err := s.Facts(context.Background()); live <- err }()
		synctest.Wait()
		if got := acquisitions.Load(); got != 1 {
			t.Errorf("waiting callers acquired source: %d", got)
		}
		cancel()
		synctest.Wait()
		if err := <-cancelled; !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled waiter: %v", err)
		}
		if len(first) != 0 {
			t.Error("active source ended with its peer")
		}
		if len(live) != 0 {
			t.Error("live waiter ended with its peer")
		}
		close(ready)
		synctest.Wait()
		for _, result := range []<-chan error{first, live} {
			if err := <-result; !errors.Is(err, sourceFailure) {
				t.Errorf("live source: %v", err)
			}
		}
		if got := acquisitions.Load(); got != 2 {
			t.Errorf("cancelled waiter called provider: acquisitions=%d, want 2", got)
		}
		if _, err := s.Projects(context.Background()); !errors.Is(err, sourceFailure) {
			t.Errorf("source failure held admission: %v", err)
		}
		if got := acquisitions.Load(); got != 3 {
			t.Errorf("source was not reacquired after failure: %d", got)
		}
	})
}

// Existing real-Wasm lifecycle tests need to know a caller registered for
// cancellation, rather than requiring it to acquire a snapshot while queued.
// heldCore holds the session's evaluator, inside a synctest bubble, so
// that graph requests wait; it returns a context for them and its cancel,
// and done, which cancels them, waits for the bubble to settle, lets the
// evaluator go and closes the session.
func heldCore(t *testing.T, s *Session) (context.Context, context.CancelFunc, func()) {
	t.Helper()
	release, err := s.core.admit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return ctx, cancel, func() { cancel(); synctest.Wait(); release(); s.Close() }
}

func sessionAdmissionWaitRegistered(t *testing.T, s *Session, want int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s.mu.Lock()
		got := len(s.active)
		s.mu.Unlock()
		if got == want {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("session registered %d callers; want %d", got, want)
		}
	}
}
