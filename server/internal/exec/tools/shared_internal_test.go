package tools

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// work is what a test of shared has done: it counts its runs, holds each
// until it is let go or stopped, counting the stops, and answers its run
// or fails as it is told.
type work struct {
	runs, stops atomic.Int32
	started     chan struct{}
	let         chan struct{}
	fail        atomic.Bool
}

func newWork() *work {
	return &work{started: make(chan struct{}, 64), let: make(chan struct{})}
}

func (w *work) do(ctx context.Context) (int, error) {
	run := w.runs.Add(1)
	w.started <- struct{}{}
	select {
	case <-w.let:
	case <-ctx.Done():
		w.stops.Add(1)
		return 0, ctx.Err()
	}
	if w.fail.Load() {
		return 0, errors.New("the work failed")
	}
	return int(run), nil
}

// answer is what one asker of shared was told, once it was told.
type answer struct {
	given int
	err   error
}

// ask asks s on ctx and tells what it was told on the channel returned.
func ask(ctx context.Context, s *shared[int], w *work) <-chan answer {
	told := make(chan answer, 1)
	go func() {
		given, err := s.do(ctx, "key", w.do)
		told <- answer{given, err}
	}()
	return told
}

// told returns what an asker was told, failing the test where it was
// told nothing in time.
func told(t *testing.T, who string, from <-chan answer) answer {
	t.Helper()
	select {
	case a := <-from:
		return a
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was told nothing", who)
		return answer{}
	}
}

// waiting returns once n wait on the work under the key.
func waiting(t *testing.T, s *shared[int], n int) {
	t.Helper()
	for begun := time.Now(); ; time.Sleep(time.Millisecond) {
		s.mu.Lock()
		f := s.going["key"]
		there := f != nil && f.waiting == n
		s.mu.Unlock()
		if there {
			return
		}
		if time.Since(begun) > 5*time.Second {
			t.Fatalf("%d never waited on the work", n)
		}
	}
}

// anew fails the test where the next to ask is not answered by a run of
// its own, the second.
func anew(t *testing.T, s *shared[int], w *work) {
	t.Helper()
	if a := told(t, "the next", ask(context.Background(), s, w)); a.err != nil || a.given != 2 {
		t.Errorf("the next was told %d, %v; want a run of its own", a.given, a.err)
	}
}

// What several wait on is done once for all of them, on no waiter's
// context: the first to ask may give up and the others are answered;
// when the last gives up the work is stopped and nothing of it kept,
// and the next to ask has it done anew; work that fails is not kept,
// and the next to ask has it done anew; an answer is kept.
func TestWorkSeveralWaitOnIsDoneOnceForAll(t *testing.T) {
	for name, run := range map[string]func(t *testing.T, s *shared[int], w *work){
		"the first to ask gives up, and the second is answered": func(t *testing.T, s *shared[int], w *work) {
			first, giveUp := context.WithCancel(context.Background())
			gaveUp := ask(first, s, w)
			<-w.started
			second := ask(context.Background(), s, w)
			waiting(t, s, 2)
			giveUp()
			if a := told(t, "the first", gaveUp); !errors.Is(a.err, context.Canceled) {
				t.Errorf("the first was told %v, want that it gave up", a.err)
			}
			close(w.let)
			if a := told(t, "the second", second); a.err != nil || a.given != 1 {
				t.Errorf("the second was told %d, %v; want the one run's answer", a.given, a.err)
			}
			if n := w.stops.Load(); n != 0 {
				t.Errorf("the work another waited on was stopped %d times", n)
			}
		},
		"the last to wait gives up, the work is stopped, and the next asks anew": func(t *testing.T, s *shared[int], w *work) {
			impatient, giveUp := context.WithCancel(context.Background())
			gaveUp := ask(impatient, s, w)
			<-w.started
			giveUp()
			told(t, "the one that gave up", gaveUp)
			for begun := time.Now(); w.stops.Load() == 0; time.Sleep(time.Millisecond) {
				if time.Since(begun) > 5*time.Second {
					t.Fatal("the work went on with none waiting for it")
				}
			}
			close(w.let)
			anew(t, s, w)
		},
		"the work fails, and the next asks anew": func(t *testing.T, s *shared[int], w *work) {
			w.fail.Store(true)
			close(w.let)
			if a := told(t, "the first", ask(context.Background(), s, w)); a.err == nil {
				t.Fatal("the first was told no failure")
			}
			w.fail.Store(false)
			anew(t, s, w)
		},
		"fifty ask at once, the work runs once, and its answer is kept": func(t *testing.T, s *shared[int], w *work) {
			var asking sync.WaitGroup
			for range 50 {
				asking.Go(func() {
					if given, err := s.do(context.Background(), "key", w.do); err != nil || given != 1 {
						t.Errorf("an asker was told %d, %v; want the one run's answer", given, err)
					}
				})
			}
			waiting(t, s, 50)
			close(w.let)
			asking.Wait()
			if n := w.runs.Load(); n != 1 {
				t.Errorf("the work ran %d times, want once", n)
			}
			if given, kept := s.known("key"); !kept || given != 1 {
				t.Errorf("kept %d, %t; want the one run's answer", given, kept)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			run(t, &shared[int]{}, newWork())
		})
	}
}
