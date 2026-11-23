package controlplane

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/core/refusal"
	"github.com/frontseat-dev/frontseat/sdk/go"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
)

// spoken is what a tunnel of the tests' sandboxes says of where it
// leads, and what it says back of a word it is told.
func spoken(t *testing.T, conn net.Conn, word string) (string, string) {
	t.Helper()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	read := bufio.NewReader(conn)
	leads, err := read.ReadString('\n')
	if err != nil {
		t.Fatalf("the tunnel said nothing of where it leads: %v", err)
	}
	if _, err := conn.Write([]byte(word + "\n")); err != nil {
		t.Fatal(err)
	}
	back, err := read.ReadString('\n')
	if err != nil {
		t.Fatalf("the tunnel to %s said nothing back: %v", leads, err)
	}
	return leads, back
}

// sentTo answers a call sent to a path of a control plane with a token,
// as Connect's own client reads the answer: a refusal by its code and
// its ErrorInfo.
type sentTo struct{ path, token string }

func (s sentTo) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Path, r.URL.RawPath = s.path, ""
	return bearer{s.token}.RoundTrip(r)
}

func (s sentTo) refused(address string) error {
	client := workspacev1alpha1connect.NewWorkspaceServiceClient(connect.NewClient(connecthttp.NewTransport(&http.Client{Transport: s}, address)))
	_, err := client.DescribeWorkspace(context.Background(), &workspacev1.DescribeWorkspaceRequest{Name: "adas"})
	return err
}

// A port of a workspace's sandbox is carried to the workspace's owner as
// bytes, both ways, by the workspace's name and the port's number, and
// the control plane knows nothing of what speaks there; its shell is
// carried the same way, as it was. Another is refused it as they are
// refused the shell, and so is no one. The port of the daemon's source
// API, which is the control plane's alone, is carried to nobody, the
// owner neither, and what is written where a port goes and is no port,
// written one way, is refused: each PORT_NOT_SERVED, and nothing of the
// sandbox is asked. The controls are the ports beside the source API's,
// which are carried.
func TestAPortOfAWorkspacesSandboxIsCarriedToItsOwnerAlone(t *testing.T) {
	s := over(t)
	s.speaks = true
	_, address := adas(t, s)
	ctx := context.Background()
	as := func(token string) *frontseat.Workspace {
		var options []frontseat.Option
		if token != "" {
			options = append(options, frontseat.WithToken(token))
		}
		client, err := frontseat.New(address, options...)
		if err != nil {
			t.Fatal(err)
		}
		return client.Workspace("adas")
	}
	led := func() []string {
		s.mu.Lock()
		defer s.mu.Unlock()
		taken := s.led
		s.led = nil
		return taken
	}

	// Its owner is carried to a port, and what is written comes back.
	for _, port := range []int{8080, 1, 65535, ports.Daemon, ports.Source - 1, ports.Source + 1} {
		conn, err := as(owner).Forward(ctx, port)
		if err != nil {
			t.Fatalf("the port %d, for its owner: %v", port, err)
		}
		want := "sbx_1:" + strconv.Itoa(port) + "\n"
		if leads, back := spoken(t, conn, "a word"); leads != want || back != "a word\n" {
			t.Errorf("the port %d led to %q and said back %q, want %q and the word", port, leads, back, want)
		}
	}
	// The control of what follows, and what was there before: its shell.
	conn, err := as(owner).Shell(ctx)
	if err != nil {
		t.Fatalf("its shell, for its owner: %v", err)
	}
	if leads, back := spoken(t, conn, "a word"); leads != "sbx_1:"+strconv.Itoa(ports.Shell)+"\n" || back != "a word\n" {
		t.Errorf("its shell led to %q and said back %q", leads, back)
	}
	if got := len(led()); got != 7 {
		t.Fatalf("the sandbox was led to %d times, want the six ports and the shell", got)
	}

	// Another, and no one, are refused a port as they are the shell.
	for what, c := range map[string]struct {
		token  string
		code   connect.Code
		reason string
		domain string
	}{
		"another":  {another, connect.CodePermissionDenied, "NOT_OWNER", refusal.Workspace},
		"no token": {"", connect.CodeUnauthenticated, "IDENTITY_MISSING", refusal.ControlPlane},
	} {
		_, forwarded := as(c.token).Forward(ctx, 8080)
		_, shell := as(c.token).Shell(ctx)
		for route, err := range map[string]error{"a port": forwarded, "the shell": shell} {
			if why, in := frontseat.Reason(err); connect.CodeOf(err) != c.code || why != c.reason || in != c.domain {
				t.Errorf("%s, with %s: %v (%s, %s), want %s, %s, %s", route, what, err, why, in, c.code, c.reason, c.domain)
			}
		}
	}
	if got := led(); len(got) != 0 {
		t.Errorf("someone who is not its owner was led into its sandbox: %v", got)
	}

	// The source API's port is the control plane's alone.
	_, err = as(owner).Forward(ctx, ports.Source)
	if !refusedAsNotServed(err, "PORT_NOT_SERVED") {
		why, in := frontseat.Reason(err)
		t.Errorf("the source API's port, for its owner: %v (%s, %s), want unimplemented, PORT_NOT_SERVED, %s", err, why, in, refusal.Workspace)
	}
	// What is no port, or one written another way, the source API's so
	// written among them.
	for _, written := range []string{"0", "65536", "+80", "080", "80x", "", "-1", "8080/", "8080/more", "0" + strconv.Itoa(ports.Source), "+" + strconv.Itoa(ports.Source), strconv.Itoa(ports.Source) + "/", " 8080", "shell"} {
		err := sentTo{"/workspaces/adas/ports/" + written, owner}.refused(address)
		if !refusedAsNotServed(err, "PORT_NOT_SERVED") {
			why, in := frontseat.Reason(err)
			t.Errorf("the port written %q: %v (%s, %s), want unimplemented, PORT_NOT_SERVED, %s", written, err, why, in, refusal.Workspace)
		}
	}
	if got := led(); len(got) != 0 {
		t.Errorf("a port that is not served was led to: %v", got)
	}
	// The control of the reader of refusals: a port written as one is
	// led to, asked the same way.
	if err := (sentTo{"/workspaces/adas/ports/8080", owner}).refused(address); refusedAsNotServed(err, "PORT_NOT_SERVED") {
		t.Errorf("the control: a port written as one is: %v", err)
	}
	if got := led(); !slices.Equal(got, []string{"sbx_1:8080/a tunnel"}) {
		t.Errorf("the control: a port written as one is led to %v", got)
	}
}
