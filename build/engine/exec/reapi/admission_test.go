package reapi_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/frontseat-dev/frontseat/build/engine/exec/access"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/testsupport/testauthority"
)

// A grid that asks who a daemon is and does not admit it is told apart
// from one that is not up: the error says so, and keeps the grid's status.
func TestAGridThatDoesNotAdmitTheDaemonSaysSo(t *testing.T) {
	for _, code := range []codes.Code{codes.Unauthenticated, codes.PermissionDenied} {
		grid := &reapitest.Answering{Code: code}
		_, err := reapi.Dial(t.Context(), reapi.Config{Address: grid.Serve(t)})
		if err == nil || status.Code(err) != code || !reapi.NotAdmitted(err) || !strings.Contains(err.Error(), "did not admit this daemon") || !strings.Contains(err.Error(), "the grid's own words") {
			t.Errorf("%s: %v", code, err)
		}
	}
	// Controls: a grid that fails otherwise is not said to have refused
	// the daemon, and one that admits it is dialled.
	failing := &reapitest.Answering{Code: codes.Internal}
	_, err := reapi.Dial(t.Context(), reapi.Config{Address: failing.Serve(t)})
	if err == nil || reapi.NotAdmitted(err) || strings.Contains(err.Error(), "did not admit") {
		t.Errorf("a grid that failed: %v", err)
	}
	admitting := &reapitest.Answering{}
	grid, err := reapi.Dial(t.Context(), reapi.Config{Address: admitting.Serve(t)})
	if err != nil {
		t.Fatal(err)
	}
	grid.Close()
}

// A token that could not be read when a call was made is the daemon's
// own trouble, and nothing was sent: the grid is not said to have refused
// what it was never asked.
func TestATokenThatCouldNotBeReadIsNotTheGridsRefusal(t *testing.T) {
	known := testauthority.New(t)
	grid := &reapitest.Answering{Certificate: &known.Server}
	address := grid.Serve(t)
	file := filepath.Join(t.TempDir(), "token")
	_, err := reapi.Dial(t.Context(), reapi.Config{Address: address, Access: access.Config{Authorities: known.File, Token: file}})
	if err == nil || reapi.NotAdmitted(err) || !access.Unsent(err) || strings.Contains(err.Error(), "did not admit") || !strings.Contains(err.Error(), "was not sent to the grid") || !strings.Contains(err.Error(), file) {
		t.Errorf("a token file that is not there: %v", err)
	}
	if !reapi.Settled(err) {
		t.Errorf("asking again would read the same file: %v", err)
	}
	if grid.Asked.Load() != 0 {
		t.Error("the grid was asked all the same")
	}
	// Control: with the file there the same grid is dialled.
	if err := os.WriteFile(file, []byte("aaa.bbb.ccc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dialled, err := reapi.Dial(t.Context(), reapi.Config{Address: address, Access: access.Config{Authorities: known.File, Token: file}})
	if err != nil {
		t.Fatal(err)
	}
	dialled.Close()
}

// A grid that cannot be asked over TLS as the daemon was told to ask it
// has answered as far as it ever will: it is not one that is not up yet.
func TestAGridThatCannotBeAskedOverTLSIsNotOneThatIsNotUpYet(t *testing.T) {
	known, other, elsewhere := testauthority.New(t), testauthority.New(t), testauthority.Named(t, "another.example")
	for name, c := range map[string]struct {
		grid   *reapitest.Answering
		access access.Config
	}{
		"one that answers in the clear":            {&reapitest.Answering{}, access.Config{TLS: true}},
		"one of an authority the daemon knows not": {&reapitest.Answering{Certificate: &other.Server}, access.Config{Authorities: known.File}},
		"one known by another name":                {&reapitest.Answering{Certificate: &elsewhere.Server}, access.Config{Authorities: elsewhere.File}},
	} {
		_, err := reapi.Dial(t.Context(), reapi.Config{Address: c.grid.Serve(t), Access: c.access})
		if err == nil || !reapi.Settled(err) || !strings.Contains(err.Error(), "could not be asked over TLS") || reapi.NotAdmitted(err) {
			t.Errorf("%s: %v", name, err)
		}
		if c.grid.Asked.Load() != 0 {
			t.Errorf("%s: it was asked all the same", name)
		}
	}
	// Controls: a grid that is not there may yet be, in the clear and over
	// TLS alike; one that does not admit the daemon has answered.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := listener.Addr().String()
	listener.Close()
	for name, how := range map[string]access.Config{"in the clear": {}, "over TLS": {Authorities: known.File}} {
		if _, err := reapi.Dial(t.Context(), reapi.Config{Address: gone, Access: how}); err == nil || reapi.Settled(err) || strings.Contains(err.Error(), "could not be asked over TLS") {
			t.Errorf("a grid that is not there, %s: %v", name, err)
		}
	}
	refusing := &reapitest.Answering{Code: codes.Unauthenticated}
	if _, err := reapi.Dial(t.Context(), reapi.Config{Address: refusing.Serve(t)}); !reapi.Settled(err) {
		t.Errorf("a grid that does not admit the daemon: %v", err)
	}
}

// A grid over TLS that refuses the daemon's side of it, or does not speak
// gRPC's HTTP/2 there, cannot be asked as the daemon was told either: it
// asks for a certificate the daemon has none of, before its handshake ends
// (TLS 1.2) or after (TLS 1.3), or is a TLS front of another protocol. A
// front that cannot answer yet, its own certificate not loaded, is
// waited for. Each is known by what TLS said to the daemon.
func TestAGridThatRefusesTheDaemonsSideOfTLSIsNotWaitedFor(t *testing.T) {
	known := testauthority.New(t)
	for name, address := range map[string]string{
		"one that asks a certificate of the daemon, over TLS 1.3": (&reapitest.Answering{Certificate: &known.Server, ClientAuth: tls.RequireAnyClientCert}).Serve(t),
		"one that asks a certificate of the daemon, over TLS 1.2": (&reapitest.Answering{Certificate: &known.Server, ClientAuth: tls.RequireAnyClientCert, Version: tls.VersionTLS12}).Serve(t),
		"one that does not speak HTTP/2 over TLS":                 front(t, &tls.Config{Certificates: []tls.Certificate{known.Server}}),
	} {
		ctx, stop := context.WithTimeout(t.Context(), 8*time.Second)
		began := time.Now()
		_, err := reapi.Dial(ctx, reapi.Config{Address: address, Access: access.Config{Authorities: known.File}})
		stop()
		if err == nil || !reapi.Settled(err) || !strings.Contains(err.Error(), "could not be asked over TLS") || time.Since(began) > 5*time.Second {
			t.Errorf("%s, after %s: %v", name, time.Since(began), err)
		}
	}
	// Control: a front that says it failed itself (an internal_error
	// alert, as one with no certificate loaded yet) may yet answer.
	failing := front(t, &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return nil, errors.New("no certificate loaded yet")
	}})
	_, err := reapi.Dial(t.Context(), reapi.Config{Address: failing, Access: access.Config{Authorities: known.File}})
	if err == nil || reapi.Settled(err) || strings.Contains(err.Error(), "could not be asked over TLS") {
		t.Errorf("a front that failed itself: %v", err)
	}
}

// front is a TLS server on the loopback that speaks no gRPC: it shakes
// hands as it is configured to and reads what it is sent.
func front(t *testing.T, config *tls.Config) string {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatal(err)
	}
	var open sync.WaitGroup
	t.Cleanup(func() { listener.Close(); open.Wait() })
	open.Add(1)
	go func() {
		defer open.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			open.Add(1)
			go func() {
				defer open.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				if conn.(*tls.Conn).Handshake() == nil {
					_, _ = io.Copy(io.Discard, conn)
				}
			}()
		}
	}()
	return listener.Addr().String()
}

// What a grid says in its own words is the grid's to choose. A grid asked
// in the clear that answers, as one relaying a backend's trouble does,
// with the words of a TLS handshake that failed is a grid not up yet: the
// daemon was told of no TLS, and says nothing of it.
func TestAGridsOwnWordsAreNotTakenForTheDaemonsTransport(t *testing.T) {
	relaying := &reapitest.Answering{Code: codes.Unavailable, Words: `Backend "storage-0": connection error: desc = "transport: authentication handshake failed: tls: first record does not look like a TLS handshake"`}
	_, err := reapi.Dial(t.Context(), reapi.Config{Address: relaying.Serve(t)})
	if err == nil || reapi.Settled(err) || strings.Contains(err.Error(), "could not be asked over TLS") || strings.Contains(err.Error(), "grid.tls") {
		t.Errorf("a grid asked in the clear that speaks of a handshake: %v", err)
	}
	// Over TLS too, what an admitted grid says in its words is not the
	// transport's: the words stand at the start of what the transport says.
	known := testauthority.New(t)
	saying := &reapitest.Answering{Certificate: &known.Server, Code: codes.Unavailable, Words: relaying.Words}
	_, err = reapi.Dial(t.Context(), reapi.Config{Address: saying.Serve(t), Access: access.Config{Authorities: known.File}})
	if err == nil || reapi.Settled(err) || strings.Contains(err.Error(), "could not be asked over TLS") {
		t.Errorf("a grid asked over TLS that speaks of a handshake: %v", err)
	}
	// Nor where they stand first, word for word as gRPC writes a failed
	// handshake of its own: a relay that passes its backend's failure on
	// says so, over a handshake of the daemon's that succeeded. That the
	// daemon's own handshake failed is the daemon's to know.
	verbatim := &reapitest.Answering{Certificate: &known.Server, Code: codes.Unavailable,
		Words: `connection error: desc = "transport: authentication handshake failed: tls: failed to verify certificate: x509: certificate has expired or is not yet valid"`}
	_, err = reapi.Dial(t.Context(), reapi.Config{Address: verbatim.Serve(t), Access: access.Config{Authorities: known.File}})
	if err == nil || reapi.Settled(err) || strings.Contains(err.Error(), "could not be asked over TLS") || verbatim.Asked.Load() == 0 {
		t.Errorf("a grid asked over TLS that answers in gRPC's words of a handshake, asked %d times: %v", verbatim.Asked.Load(), err)
	}
	// A grid that answers "nothing was sent" was asked, and did not admit
	// the daemon: that nothing was sent is the daemon's to know.
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("aaa.bbb.ccc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lying := &reapitest.Answering{Certificate: &known.Server, Code: codes.Unauthenticated, Words: "nothing was sent to the grid: go away"}
	_, err = reapi.Dial(t.Context(), reapi.Config{Address: lying.Serve(t), Access: access.Config{Authorities: known.File, Token: file}})
	if err == nil || access.Unsent(err) || !reapi.NotAdmitted(err) || !strings.Contains(err.Error(), "did not admit this daemon") || !strings.Contains(err.Error(), file) {
		t.Errorf("a grid that says nothing was sent: %v", err)
	}
	if lying.Asked.Load() == 0 {
		t.Error("the grid was not asked")
	}
}

// A door that takes a connection and lets it go, or takes it and says
// nothing, is a grid that is not up yet behind what stands in front of
// it: it is asked again, and not said to answer in the clear.
func TestADoorThatIsNotAnsweringYetIsAGridNotUpYet(t *testing.T) {
	known := testauthority.New(t)
	for name, silent := range map[string]bool{"one that closes what it takes": false, "one that says nothing": true} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		held := make(chan net.Conn, 64)
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					close(held)
					return
				}
				if silent {
					held <- conn
					continue
				}
				conn.Close()
			}
		}()
		t.Cleanup(func() {
			listener.Close()
			for conn := range held {
				conn.Close()
			}
		})
		ctx, stop := context.WithTimeout(t.Context(), 4*time.Second)
		_, err = reapi.Dial(ctx, reapi.Config{Address: listener.Addr().String(), Access: access.Config{Authorities: known.File}})
		stop()
		if err == nil || reapi.Settled(err) || strings.Contains(err.Error(), "could not be asked over TLS") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
