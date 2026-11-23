package access

import (
	"crypto/tls"
	"net"
	"syscall"
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testauthority"
)

// A connection the daemon's TLS watches is still the connection gRPC
// would have had: what gRPC asks of the socket beneath it (SyscallConn,
// for channelz's socket options) is answered as it would be unwatched.
func TestAWatchedConnectionKeepsItsSocket(t *testing.T) {
	known := testauthority.New(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{known.Server}, NextProtos: []string{"h2"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { _ = conn.(*tls.Conn).Handshake(); conn.Close() }()
		}
	}()
	_, shaken, err := Config{Authorities: known.File}.Options()
	if err != nil {
		t.Fatal(err)
	}
	options, err := Config{Authorities: known.File}.credentials(shaken)
	if err != nil {
		t.Fatal(err)
	}
	for name, plain := range map[string]bool{"watched": false, "control: unwatched": true} {
		creds := options
		if plain {
			creds = options.(watched).TransportCredentials
		}
		raw, err := net.Dial("tcp", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conn, _, err := creds.ClientHandshake(t.Context(), listener.Addr().String(), raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := conn.(syscall.Conn); !ok {
			t.Errorf("%s: %T gives no SyscallConn", name, conn)
		}
		conn.Close()
	}
}
