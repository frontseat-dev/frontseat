package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// listening listens on a free port of the loopback, and closes the
// listener when the test ends.
func listening(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}

// Cancellation must reach ordinary and hijacked requests, and the serving
// function must join their cleanup before its caller releases dependencies.
func TestStoppingJoinsRequestCleanup(t *testing.T) {
	for _, tc := range []struct{ hijack, stopListener bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		t.Run(fmt.Sprintf("hijack=%t/listener=%t", tc.hijack, tc.stopListener), func(t *testing.T) {
			listener := listening(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started, cancelled, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var release sync.Once
			unblock := func() { release.Do(func() { close(released) }) }
			defer unblock()
			finished := make(chan error, 1)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.hijack {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
				}
				close(started)
				select {
				case <-r.Context().Done():
					close(cancelled)
				case <-released:
					return
				}
				<-released
			})
			go func() { finished <- Serve(ctx, listener, handler) }()
			conn, err := net.Dial("tcp", listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			await(t, started, "request to start")
			// Control: an active server must keep serving while the request runs.
			select {
			case err := <-finished:
				t.Fatalf("server ended before cancellation: %v", err)
			default:
			}
			if tc.stopListener {
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			await(t, cancelled, "request context cancellation")
			select {
			case err := <-finished:
				t.Fatalf("server returned before handler cleanup: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			unblock()
			select {
			case err := <-finished:
				if tc.stopListener {
					if !errors.Is(err, net.ErrClosed) {
						t.Fatalf("listener failure was lost: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("server did not join completed request")
			}
		})
	}
}

func TestAnAlreadyCancelledServerClosesItsListener(t *testing.T) {
	listener := listening(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	finished := make(chan error, 1)
	go func() { finished <- Serve(ctx, listener, http.NotFoundHandler()) }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("already cancelled server did not stop")
	}
	if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener was not closed: %v", err)
	}
}

func TestRequestsAfterStoppingDoNotUseReleasedDependencies(t *testing.T) {
	calls := 0
	requests := Track(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	requests.ServeHTTP(httptest.NewRecorder(), request)
	if calls != 1 {
		t.Fatal("control request did not reach handler")
	}
	<-requests.Close()
	response := httptest.NewRecorder()
	requests.ServeHTTP(response, request)
	if calls != 1 || response.Code != http.StatusServiceUnavailable {
		t.Fatalf("late request: calls=%d status=%d", calls, response.Code)
	}
}

func await(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}
