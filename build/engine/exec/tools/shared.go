package tools

import (
	"context"
	"sync"
)

// shared is work several may wait on, done once for all of them, and
// what it answered, kept by a key. The work runs on no waiter's context:
// one that gives up is told so at once and leaves the others their
// answer, and the work is stopped when none waits for it any more, with
// nothing of it kept, so that one that asks later asks anew. An answer
// is kept before the waiters are told: one that comes once the work is
// over finds it kept, and does not ask again. No lock is held while the
// work runs, so work under one key holds up none under another.
//
// Work told to stop has stopped only once it returns, which may be a
// while after: whoever gave up is not kept waiting for it. Whoever is to
// close what the work uses joins it first (join).
type shared[T any] struct {
	mu    sync.Mutex
	kept  map[string]T
	going map[string]*flight[T]
	// running is the work that has not returned yet, stopped or not.
	running sync.WaitGroup
}

// join stops the work that is going and returns once all of it has
// returned, whoever waits for it being told it was stopped. It is called
// when nothing asks any more: work begun while it waits is not waited
// for.
func (s *shared[T]) join() {
	s.mu.Lock()
	for _, f := range s.going {
		f.stop()
	}
	s.mu.Unlock()
	s.running.Wait()
}

// flight is work being done, and those that wait for it.
type flight[T any] struct {
	done    chan struct{}
	stop    context.CancelFunc
	waiting int
	given   T
	err     error
}

// known returns what was answered for a key and kept, if anything was,
// so that a caller makes the work it would hand do only where nothing
// was.
func (s *shared[T]) known(key string) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	given, there := s.kept[key]
	return given, there
}

// do returns what the work answers for a key: kept, or being done, or
// done now by work, which is given a context of its own.
func (s *shared[T]) do(ctx context.Context, key string, work func(context.Context) (T, error)) (T, error) {
	s.mu.Lock()
	if given, there := s.kept[key]; there {
		s.mu.Unlock()
		return given, nil
	}
	f := s.going[key]
	if f == nil {
		doing, stop := context.WithCancel(context.WithoutCancel(ctx))
		f = &flight[T]{done: make(chan struct{}), stop: stop}
		if s.going == nil {
			s.going = map[string]*flight[T]{}
		}
		s.going[key] = f
		s.running.Add(1)
		go func() {
			defer s.running.Done()
			defer stop()
			given, err := work(doing)
			s.mu.Lock()
			if err == nil {
				if s.kept == nil {
					s.kept = map[string]T{}
				}
				s.kept[key] = given
			}
			if s.going[key] == f {
				delete(s.going, key)
			}
			f.given, f.err = given, err
			s.mu.Unlock()
			close(f.done)
		}()
	}
	f.waiting++
	s.mu.Unlock()
	select {
	case <-f.done:
		return f.given, f.err
	case <-ctx.Done():
		s.mu.Lock()
		defer s.mu.Unlock()
		if f.waiting--; f.waiting == 0 {
			// None waits for it: it is stopped, and forgotten, so that one
			// that asks later asks anew.
			f.stop()
			if s.going[key] == f {
				delete(s.going, key)
			}
		}
		var none T
		return none, ctx.Err()
	}
}
