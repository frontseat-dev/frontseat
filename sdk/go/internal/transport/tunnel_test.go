package transport_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
	"github.com/frontseat-dev/frontseat/sdk/go/internal/transport"
)

// echoes is a server that opens a port of a sandbox as the control
// plane does, to something that says back what it is told. closed is
// told when the tunnel ends on the server's side.
func echoes(closed chan<- struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn := websocket.NetConn(r.Context(), socket, websocket.MessageBinary)
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
		if closed != nil {
			closed <- struct{}{}
		}
	})
}

// said writes to a tunnel and returns what comes back.
func said(t *testing.T, conn net.Conn, word string) string {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(word)); err != nil {
		t.Fatal(err)
	}
	back := make([]byte, len(word))
	if _, err := io.ReadFull(conn, back); err != nil {
		t.Fatal(err)
	}
	return string(back)
}

// A control plane is anywhere: on this machine without TLS, or on a
// platform behind it, where a server offers HTTP/2 as well. The tunnel
// speaks what the server's address says.
func TestATunnelSpeaksWhatTheServersAddressSays(t *testing.T) {
	plain := httptest.NewServer(echoes(nil))
	defer plain.Close()
	secure := httptest.NewUnstartedServer(echoes(nil))
	secure.EnableHTTP2 = true
	secure.StartTLS()
	defer secure.Close()

	for name, server := range map[string]*httptest.Server{"without TLS": plain, "with TLS": secure} {
		conn, err := transport.Tunnel(context.Background(), server.URL+"/tunnel", transport.Reach{HTTP: server.Client()})
		if err != nil {
			t.Errorf("a server %s: %v", name, err)
			continue
		}
		if got := said(t, conn, "ping"); got != "ping" {
			t.Errorf("a server %s said back %q, want what it was told", name, got)
		}
		conn.Close()
	}
	// And TLS is checked: a server nobody vouches for is not spoken to.
	if conn, err := transport.Tunnel(context.Background(), secure.URL+"/tunnel", transport.Reach{}); err == nil {
		conn.Close()
		t.Error("a server whose certificate nobody vouches for was spoken to")
	}
}

// What SSH writes at once is more than a WebSocket takes by default.
func TestATunnelCarriesAsMuchAsIsWrittenAtOnce(t *testing.T) {
	server := httptest.NewServer(echoes(nil))
	defer server.Close()
	conn, err := transport.Tunnel(context.Background(), server.URL+"/tunnel", transport.Reach{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	much := make([]byte, 256<<10)
	for i := range much {
		much[i] = byte(i)
	}

	if got := said(t, conn, string(much)); got != string(much) {
		t.Errorf("%d bytes written at once came back as %d", len(much), len(got))
	}
}

// A tunnel ends, both ways, when either end does.
func TestATunnelEndsWhenEitherEndDoes(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewServer(echoes(closed))
	defer server.Close()
	conn, err := transport.Tunnel(context.Background(), server.URL+"/tunnel", transport.Reach{})
	if err != nil {
		t.Fatal(err)
	}
	if got := said(t, conn, "ping"); got != "ping" {
		t.Fatalf("the tunnel said back %q before it was closed", got)
	}

	conn.Close()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Error("the server's end was still open five seconds after this end closed")
	}
}

// Whoever asked for a tunnel can go away: while a server says nothing,
// and once the tunnel is open.
func TestATunnelEndsWhenWhoeverAskedGoesAway(t *testing.T) {
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	go func() {
		for {
			conn, err := silent.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	waiting, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	if conn, err := transport.Tunnel(waiting, "http://"+silent.Addr().String()+"/tunnel", transport.Reach{}); err == nil {
		conn.Close()
		t.Error("a tunnel was opened through a server that said nothing")
	}
	if waited := time.Since(started); waited > 5*time.Second {
		t.Errorf("the tunnel waited %v on a server that said nothing after whoever asked went away", waited)
	}

	server := httptest.NewServer(echoes(nil))
	defer server.Close()
	asking, gone := context.WithCancel(context.Background())
	conn, err := transport.Tunnel(asking, server.URL+"/tunnel", transport.Reach{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	gone()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Error("an open tunnel was still read from after whoever asked went away")
	}
}

// A tunnel that would carry a token in clear to another machine is not
// opened, and nothing is dialled. The control is a tunnel to this
// machine in clear, opened and presented the token
// (TestATokenIsPresentedWhereOneIsGiven), and one to another machine
// with no token, which is dialled.
func TestATunnelCarriesNoTokenInClearToAnotherMachine(t *testing.T) {
	dialled := &countingTransport{}
	reach := transport.Reach{HTTP: &http.Client{Transport: unreached{dialled}}, Token: "a-token", Origin: "http://frontseat.example"}
	conn, err := transport.Tunnel(context.Background(), "ws://frontseat.example/shell", reach)
	if err == nil {
		conn.Close()
	}
	if !errors.Is(err, bearer.ErrInClear) {
		t.Errorf("a tunnel in clear to another machine: %v, want it refused", err)
	}
	if made := dialled.made.Load(); made != 0 {
		t.Errorf("another machine was dialled %d times with a token to present", made)
	}
	reach.Token = ""
	if conn, err := transport.Tunnel(context.Background(), "ws://frontseat.example/shell", reach); err == nil {
		conn.Close()
	} else if errors.Is(err, bearer.ErrInClear) {
		t.Errorf("the control: a tunnel with no token was refused: %v", err)
	}
	if made := dialled.made.Load(); made != 1 {
		t.Errorf("the control: a tunnel with no token dialled %d times, want once", made)
	}
}

// unreached counts what would be dialled, and reaches nothing.
type unreached struct{ counted *countingTransport }

func (u unreached) RoundTrip(*http.Request) (*http.Response, error) {
	u.counted.made.Add(1)
	return nil, errors.New("nothing is reached from this test")
}
