package httpserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// arriving is how long a request's header may take to arrive in a test.
const arriving = 200 * time.Millisecond

// served serves a handler on a listener of the test's, with a short
// time for a request's header to arrive (arriving), and returns its address.
func served(t *testing.T, handler http.Handler) string {
	t.Helper()
	listener := listening(t)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- serve(ctx, listener, handler, arriving) }()
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	return listener.Addr().String()
}

// A peer that begins a request and never ends its header holds a
// connection, a goroutine and a descriptor for as long as the server
// lets it: the server lets it only so long, and closes the connection.
func TestARequestWhoseHeaderNeverEndsIsLetGo(t *testing.T) {
	address := served(t, http.NotFoundHandler())
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: frontseat\r\nX-Slow: ")); err != nil {
		t.Fatal(err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(conn)

	if timeout, is := err.(net.Error); is && timeout.Timeout() {
		t.Error("the connection of a request whose header never ends was held open")
	}
}

// The control: what bounds a request's arrival bounds no answer. A
// stream that goes on for longer than a header may take is read whole.
func TestAnAnswerMayLastLongerThanARequestsHeaderMayTake(t *testing.T) {
	address := served(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("first "))
		w.(http.Flusher).Flush()
		time.Sleep(3 * arriving)
		_, _ = w.Write([]byte("last"))
	}))

	response, err := http.Get("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	said, err := io.ReadAll(response.Body)

	if err != nil || string(said) != "first last" {
		t.Errorf("a stream was read as %q, %v", said, err)
	}
}
