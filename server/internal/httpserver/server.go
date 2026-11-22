// Package httpserver owns the HTTP listener and request lifetime of a server
// command. It knows nothing of the API served or its dependencies.
package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// headerWithin is how long a request's header may take to arrive, and
// idleFor how long a connection that asks nothing is kept. Neither
// bounds a request's body or an answer: a stream lasts as long as its
// handler does.
var headerWithin, idleFor = 10 * time.Second, 2 * time.Minute

// Serve serves HTTP/1 and unencrypted HTTP/2 until the context is cancelled or
// the listener fails. It owns and closes the listener, cancels request contexts,
// and joins admitted handlers before returning. Handlers must honor cancellation
// and join any work they start before returning, including hijacked connections.
// A peer that does not end a request's header in time, or keeps a connection
// that asks nothing, is let go.
func Serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	requests := Track(handler)
	serving, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := &http.Server{
		Handler: requests, BaseContext: func(net.Listener) context.Context { return serving },
		ReadHeaderTimeout: headerWithin, IdleTimeout: idleFor,
	}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)
	closed := make(chan error, 1)
	context.AfterFunc(serving, func() {
		joined := requests.Close()
		err := srv.Close()
		<-joined
		closed <- err
	})
	err := srv.Serve(listener)
	// A listener error also cancels and joins requests. Closing the listener
	// alone would otherwise leave their contexts and dependencies alive.
	cancel()
	closeErr := <-closed
	if ctx.Err() != nil && errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return errors.Join(err, closeErr)
}

// Track owns admission to a handler. Closing it prevents new calls, then joins
// admitted handlers before their dependencies may be released. The owner must
// cancel or otherwise bound requests; Close does not cancel their contexts.
func Track(handler http.Handler) *Requests { return &Requests{next: handler} }

// Requests tracks calls to one handler, including hijacked requests. The
// underlying ResponseWriter is passed through unchanged. Construct it with Track.
type Requests struct {
	next    http.Handler
	mu      sync.Mutex
	stopped bool
	active  int
	done    chan struct{}
}

// Close atomically refuses further requests and returns a channel closed once
// every admitted handler has returned. Repeating Close returns the same channel.
func (r *Requests) Close() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done == nil {
		r.done = make(chan struct{})
	}
	if !r.stopped {
		r.stopped = true
		if r.active == 0 {
			close(r.done)
		}
	}
	return r.done
}

func (r *Requests) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		http.Error(w, "server is stopping", http.StatusServiceUnavailable)
		return
	}
	r.active++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.active--
		if r.stopped && r.active == 0 {
			close(r.done)
		}
	}()
	r.next.ServeHTTP(w, request)
}
