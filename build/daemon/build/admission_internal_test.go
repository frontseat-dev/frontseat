package build

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
)

// A grid that is not up yet is asked again for a while. One that answers
// that it does not admit the daemon has answered: asking again for half a
// minute would say nothing new, and say it late.
func TestAGridThatDoesNotAdmitTheDaemonIsNotAskedAgain(t *testing.T) {
	refusing := &reapitest.Answering{Code: codes.Unauthenticated}
	began := time.Now()
	_, err := dial(t.Context(), Grid{Address: refusing.Serve(t)}, nil)
	if !reapi.NotAdmitted(err) || time.Since(began) > 10*time.Second {
		t.Errorf("after %s: %v", time.Since(began), err)
	}
	asked := refusing.Asked.Load()
	// Control: a grid that fails otherwise is asked again, until the
	// daemon is stopped.
	failing := &reapitest.Answering{Code: codes.Unavailable}
	ctx, stop := context.WithTimeout(t.Context(), 4*time.Second)
	defer stop()
	if _, err := dial(ctx, Grid{Address: failing.Serve(t)}, nil); err == nil || reapi.NotAdmitted(err) {
		t.Errorf("a grid that is not up: %v", err)
	}
	if again := failing.Asked.Load(); again <= asked {
		t.Errorf("a grid that is not up was asked %d times, one that refused %d", again, asked)
	}
}

// A grid that cannot be asked as the daemon was told to ask it, one that
// answers in the clear to a daemon told of TLS, is not waited for.
func TestAGridThatCannotBeAskedAsToldIsNotWaitedFor(t *testing.T) {
	clear := &reapitest.Answering{}
	ctx, stop := context.WithTimeout(t.Context(), 8*time.Second)
	defer stop()
	began := time.Now()
	_, err := dial(ctx, Grid{Address: clear.Serve(t), TLS: true}, nil)
	if err == nil || time.Since(began) > 5*time.Second || !strings.Contains(err.Error(), "could not be asked over TLS") {
		t.Errorf("after %s: %v", time.Since(began), err)
	}
	// Control: a grid that is not there is waited for, until the daemon
	// is stopped.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := listener.Addr().String()
	listener.Close()
	waiting, stopWaiting := context.WithTimeout(t.Context(), 3*time.Second)
	defer stopWaiting()
	began = time.Now()
	if _, err := dial(waiting, Grid{Address: gone}, nil); err == nil || time.Since(began) < 2*time.Second {
		t.Errorf("a grid that is not there, after %s: %v", time.Since(began), err)
	}
}
