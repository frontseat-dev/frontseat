package drivertest

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// Watched is a connection to a port of a sandbox that keeps how it was
// used, for a test of what carries one: whether anything read or wrote
// it once it was closed, whether it was closed while something read or
// wrote it, how much reads or writes it now, and how much ever did. It
// is one end of a pipe; the test holds the other (Far), and speaks as
// the port.
type Watched struct {
	net.Conn
	// Far is the port's end.
	Far net.Conn
	// stream is whether it is a stream that takes no deadline, as a
	// driver's that reaches a port through a gateway: it can be told to
	// stop only by closing it, and takes a while to let go of what read
	// it.
	stream bool

	mu      sync.Mutex
	closed  bool
	under   int
	begun   int
	misused []string
}

// lets is how long a stream takes to let go of a read its closing
// ended: long enough that whoever does not wait for the read is seen
// not to have.
const lets = 50 * time.Millisecond

// Watch returns a connection that takes deadlines, as a socket does.
func Watch() *Watched {
	near, far := net.Pipe()
	return &Watched{Conn: near, Far: far}
}

// WatchStream returns a connection that takes no deadline (os.ErrNoDeadline),
// as a stream does.
func WatchStream() *Watched {
	w := Watch()
	w.stream = true
	return w
}

// Misused is each use of the connection that should not have been: a
// second closing, and, where it takes deadlines and so could have been
// told to stop first, a closing while something read or wrote it and a
// read or a write begun once it was closed. A stream is closed to stop
// what reads it, and what was about to read it then does, and fails:
// whoever carries one is held to have waited for that (Under, Begun).
func (w *Watched) Misused() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.misused...)
}

// Under is how many reads and writes of it are under way.
func (w *Watched) Under() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.under
}

// Begun is how many reads and writes of it were ever begun.
func (w *Watched) Begun() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.begun
}

// Closed is whether it was closed.
func (w *Watched) Closed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

// used counts a read or a write, from when it begins until what it
// returns is called.
func (w *Watched) used(what string) (done func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed && !w.stream {
		w.misused = append(w.misused, what+" began once it was closed")
	}
	w.under++
	w.begun++
	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.under--
	}
}

func (w *Watched) Read(p []byte) (int, error) {
	defer w.used("a read")()
	n, err := w.Conn.Read(p)
	if err != nil && w.stream && w.Closed() {
		time.Sleep(lets)
	}
	return n, err
}

func (w *Watched) Write(p []byte) (int, error) {
	defer w.used("a write")()
	return w.Conn.Write(p)
}

func (w *Watched) Close() error {
	w.mu.Lock()
	switch {
	case w.closed:
		w.misused = append(w.misused, "it was closed twice")
	case w.under > 0 && !w.stream:
		w.misused = append(w.misused, fmt.Sprintf("it was closed while %d read or wrote it", w.under))
	}
	w.closed = true
	w.mu.Unlock()
	return w.Conn.Close()
}

// SetDeadline is the one deadline a carrier sets. A socket whose far
// end has closed takes a deadline still, where a pipe refuses it: this
// is as a socket.
func (w *Watched) SetDeadline(at time.Time) error {
	if w.stream {
		return os.ErrNoDeadline
	}
	if err := w.Conn.SetDeadline(at); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	return nil
}
