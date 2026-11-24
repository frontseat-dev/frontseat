package sandbox_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coder/websocket"

	"github.com/frontseat-dev/frontseat/core/refusal"
	"github.com/frontseat-dev/frontseat/sandbox"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

// through opens a port of a sandbox through a server, as a WebSocket of
// binary messages, and returns all the port says before it closes.
func through(t *testing.T, server, sandbox string, port int) (string, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	socket, response, err := websocket.Dial(ctx, fmt.Sprintf("%s/sandboxes/%s/tunnels/%d", server, sandbox, port), nil)
	if err != nil {
		return "", response, err
	}
	conn := websocket.NetConn(ctx, socket, websocket.MessageBinary)
	defer conn.Close()
	said, err := io.ReadAll(conn)
	return string(said), response, err
}

// What is not HTTP reaches a port of a sandbox as bytes: SSH above all.
// The memory driver's ports say the sandbox's id and close, so that the
// bytes arrived, and that the tunnel ended when the port did, are both
// seen.
func TestAPortOfASandboxIsReachedAsBytesThroughTheServer(t *testing.T) {
	client, srv := serve(t)
	made := create(t, client, &sandboxv1.SandboxSpec{Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_ALL}, Image: "box", Ports: []int32{2222}})

	said, _, err := through(t, srv.URL, made.GetId(), 2222)

	if err != nil {
		t.Fatalf("the tunnel did not end as the port did: %v", err)
	}
	if strings.TrimSpace(said) != made.GetId() {
		t.Errorf("the port said %q through the tunnel, want the sandbox's id", said)
	}
}

// A server is behind whatever stands in front of it on a platform, and
// what stands there speaks HTTP: the tunnel is HTTP's own upgrade, so
// it passes.
func TestATunnelPassesWhatStandsInFrontOfTheServer(t *testing.T) {
	client, srv := serve(t)
	made := create(t, client, &sandboxv1.SandboxSpec{Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_ALL}, Image: "box", Ports: []int32{2222}})
	behind, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(httputil.NewSingleHostReverseProxy(behind))
	defer front.Close()

	said, _, err := through(t, front.URL, made.GetId(), 2222)

	if err != nil || strings.TrimSpace(said) != made.GetId() {
		t.Errorf("through what stands in front, the port said %q (%v), want the sandbox's id", said, err)
	}
}

func TestATunnelIsOpenedOnlyToAPortOfASandboxTheServerMade(t *testing.T) {
	client, srv := serve(t)
	made := create(t, client, &sandboxv1.SandboxSpec{Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_ALL}, Image: "box", Ports: []int32{2222}})

	for name, to := range map[string]struct {
		sandbox string
		port    int
		want    int
	}{
		"a sandbox nobody made":           {"sbx_nobody", 2222, http.StatusNotFound},
		"a port the sandbox never opened": {made.GetId(), 9, http.StatusBadRequest},
	} {
		_, response, err := through(t, srv.URL, to.sandbox, to.port)
		if err == nil || response == nil || response.StatusCode != to.want {
			t.Errorf("%s: %v (%v), want it refused with %d", name, err, response, to.want)
		}
	}
	// And what a port of a sandbox serves over HTTP is still HTTP there.
	response, err := http.Get(fmt.Sprintf("%s/sandboxes/%s/tunnels/2222", srv.URL, made.GetId()))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode < 400 {
		t.Errorf("a plain request for a tunnel was answered %s, want it refused", response.Status)
	}
}

// unreaching is a driver whose sandboxes' ports are not reached, but
// one, and which says why in words of its own.
type unreaching struct {
	*drivertest.Memory
	why error
}

func (d unreaching) Dial(ctx context.Context, id string, port int) (net.Conn, error) {
	if port == 2222 {
		return d.Memory.Dial(ctx, id, port)
	}
	return nil, d.why
}

// tunnelRefused is how a server refuses the way to a port of a sandbox,
// as Connect's own client reads a refusal: by its code and its
// ErrorInfo. The way is asked for as a call is, since a refusal is
// answered in a WebSocket's place.
func tunnelRefused(server, id string, port int) error {
	to := rewritten(fmt.Sprintf("/sandboxes/%s/tunnels/%d", id, port))
	client := sandboxv1alpha1connect.NewSandboxServiceClient(connect.NewClient(connecthttp.NewTransport(&http.Client{Transport: to}, server)))
	_, err := client.DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: id})
	return err
}

// rewritten sends a request to a path of the server it was made for.
type rewritten string

func (to rewritten) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Path, r.URL.RawPath = string(to), ""
	return http.DefaultTransport.RoundTrip(r)
}

// A port of a sandbox that is not reached is refused by name,
// FAILED_PRECONDITION with PORT_UNREACHED, and a sandbox no one made
// NOT_FOUND with SANDBOX_NOT_FOUND, in the sandbox service's domain.
// What the driver said of it, which is whatever stands between the
// service and the sandbox cared to say, is told to no caller: it is
// logged. The control is a port that is reached, which is refused
// neither way and says what the port says.
func TestAPortThatIsNotReachedIsRefusedByNameAndNothingOfTheDriversWordsIsSaid(t *testing.T) {
	const word = "t0ken-of-the-gateway"
	var logged bytes.Buffer
	before := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(before) })
	client, srv := serveOver(t, unreaching{drivertest.NewMemory(), errors.New("gateway: reaching the port with " + word + ": refused")})
	made := create(t, client, &sandboxv1.SandboxSpec{Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_ALL}, Image: "box", Ports: []int32{2222}})

	err := tunnelRefused(srv.URL, made.GetId(), 9)
	if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeFailedPrecondition || reason != "PORT_UNREACHED" || domain != refusal.Sandbox {
		t.Errorf("a port that is not reached: %v (%s, %s), want failed precondition, PORT_UNREACHED, %s", err, reason, domain, refusal.Sandbox)
	}
	if err == nil || strings.Contains(err.Error(), word) || strings.Contains(err.Error(), "gateway") {
		t.Errorf("the refusal says the driver's words: %v", err)
	}
	_, response, err := through(t, srv.URL, made.GetId(), 9)
	if err == nil || response == nil {
		t.Fatalf("a tunnel to a port that is not reached: %v, want it refused", err)
	}
	if said, _ := io.ReadAll(response.Body); strings.Contains(string(said), word) || strings.Contains(err.Error(), word) {
		t.Errorf("the answer in the tunnel's place says the driver's words: %s (%v)", said, err)
	}
	if !strings.Contains(logged.String(), word) {
		t.Errorf("what the driver said is not logged: %q", logged.String())
	}
	err = tunnelRefused(srv.URL, "sbx_nobody", 9)
	if reason, domain := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeNotFound || reason != "SANDBOX_NOT_FOUND" || domain != refusal.Sandbox {
		t.Errorf("a port of a sandbox no one made: %v (%s, %s), want not found, SANDBOX_NOT_FOUND", err, reason, domain)
	}
	// The control: the port that is reached.
	if err := tunnelRefused(srv.URL, made.GetId(), 2222); connect.CodeOf(err) == connect.CodeFailedPrecondition || connect.CodeOf(err) == connect.CodeNotFound {
		t.Errorf("the control: a port that is reached is refused: %v", err)
	}
	if said, _, err := through(t, srv.URL, made.GetId(), 2222); err != nil || strings.TrimSpace(said) != made.GetId() {
		t.Errorf("the control: the port said %q (%v), want the sandbox's id", said, err)
	}
}

// carrying serves a tunnel to a connection, on a server whose requests
// end with a context, and returns a connection through it and what
// tells that the handler has returned, with how many reads and writes
// of the connection were then under way.
func carrying(t *testing.T, serving context.Context, inside *drivertest.Watched) (net.Conn, <-chan int) {
	t.Helper()
	returned := make(chan int, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sandbox.Carry(w, r, inside)
		returned <- inside.Under()
	}))
	srv.Config.BaseContext = func(net.Listener) context.Context { return serving }
	srv.Start()
	t.Cleanup(srv.Close)
	t.Cleanup(func() { _ = inside.Far.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	socket, _, err := websocket.Dial(ctx, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	outside := websocket.NetConn(ctx, socket, websocket.MessageBinary)
	t.Cleanup(func() { _ = outside.Close() })
	// It carries, both ways, before anything ends.
	go func() { _, _ = outside.Write([]byte("in\n")) }()
	word := make([]byte, 3)
	if _, err := io.ReadFull(inside.Far, word); err != nil || string(word) != "in\n" {
		t.Fatalf("the port read %q (%v), want what was written to the tunnel", word, err)
	}
	go func() { _, _ = inside.Far.Write([]byte("out")) }()
	if _, err := io.ReadFull(outside, word); err != nil || string(word) != "out" {
		t.Fatalf("the tunnel read %q (%v), want what the port wrote", word, err)
	}
	return outside, returned
}

// A tunnel ends both ways when either end does, or when the server
// does, and has ended when its handler returns: what read the port and
// what wrote it have both stopped, and only then is the connection to
// the port closed, where it can be told to stop first; one that takes
// no deadline, a stream of a driver's, is closed to stop what reads it,
// and that is waited for too. Nothing reads or writes it afterwards.
// When the port ends, the caller reads its end, and not a connection
// that broke.
func TestATunnelHasEndedBothWaysWhenItsHandlerReturns(t *testing.T) {
	for kind, watch := range map[string]func() *drivertest.Watched{"a socket": drivertest.Watch, "a stream that takes no deadline": drivertest.WatchStream} {
		for what, end := range map[string]func(t *testing.T, outside net.Conn, inside *drivertest.Watched, stop context.CancelFunc){
			"the caller ends": func(t *testing.T, outside net.Conn, _ *drivertest.Watched, _ context.CancelFunc) {
				_ = outside.Close()
			},
			"the port ends": func(t *testing.T, outside net.Conn, inside *drivertest.Watched, _ context.CancelFunc) {
				_ = inside.Far.Close()
				if rest, err := io.ReadAll(outside); err != nil || len(rest) != 0 {
					t.Errorf("the caller read %q (%v) when the port ended, want the port's end", rest, err)
				}
			},
			"the server ends": func(_ *testing.T, _ net.Conn, _ *drivertest.Watched, stop context.CancelFunc) {
				stop()
			},
			// Neither way reads the socket then, so that only the
			// request's own ending says it is over.
			"the server ends while the port takes nothing": func(t *testing.T, outside net.Conn, inside *drivertest.Watched, stop context.CancelFunc) {
				go func() { _, _ = outside.Write([]byte("more than the port takes")) }()
				for begun := time.Now(); inside.Under() != 2; time.Sleep(time.Millisecond) {
					if time.Since(begun) > 10*time.Second {
						t.Fatal("the port was not written to while it was read")
					}
				}
				stop()
			},
		} {
			t.Run(kind+", "+what, func(t *testing.T) {
				serving, stop := context.WithCancel(context.Background())
				defer stop()
				inside := watch()
				outside, returned := carrying(t, serving, inside)

				end(t, outside, inside, stop)

				select {
				case under := <-returned:
					if under != 0 || !inside.Closed() {
						t.Errorf("when the handler returned, %d read or wrote the connection to the port, and it was closed: %v; want none, and closed", under, inside.Closed())
					}
				case <-time.After(15 * time.Second):
					t.Fatal("the handler did not return")
				}
				// Whatever was not waited for would use it by now.
				begun := inside.Begun()
				time.Sleep(100 * time.Millisecond)
				if misused, since := inside.Misused(), inside.Begun()-begun; len(misused) != 0 || since != 0 {
					t.Errorf("the connection to the port: %v, and %d reads or writes of it began once the handler had returned", misused, since)
				}
			})
		}
	}
}
