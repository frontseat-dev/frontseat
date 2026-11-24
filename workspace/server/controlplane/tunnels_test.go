package controlplane

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
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
// refused the shell, and so is no one. The port of the supervisor's source
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
	for _, port := range []int{8080, 1, 65535, 443, ports.Source - 1, ports.Source + 1} {
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

// adasOwn is Ada's workspace, as its owner reaches it through the SDK,
// of the control plane at an address.
func adasOwn(t *testing.T, address string) *frontseat.Workspace {
	t.Helper()
	client, err := frontseat.New(address, frontseat.WithToken(owner))
	if err != nil {
		t.Fatal(err)
	}
	return client.Workspace("adas")
}

// refusedAs is whether a call was refused by a code and a reason of the
// workspace service.
func refusedAs(err error, code connect.Code, reason string) bool {
	said, domain := frontseat.Reason(err)
	return connect.CodeOf(err) == code && said == reason && domain == refusal.Workspace
}

// A port of a workspace's sandbox that is not reached is refused by
// name, FAILED_PRECONDITION with PORT_UNREACHED, and its shell too: the
// caller reads the reason, and nothing of what the sandbox service said
// of it, which is whatever stands between the control plane and the
// sandbox cared to say, a token among it. That is logged. The control
// is the same port once it is reached, which is carried.
func TestAPortThatIsNotReachedIsRefusedByNameAndNothingOfWhatWasSaidOfItIsToldTheCaller(t *testing.T) {
	const word = "t0ken-of-the-gateway"
	said := logged(t)
	s := over(t)
	s.speaks = true
	s.unreached = errors.New("openshell: reaching port 8080 of sbx_1: the gateway said " + word)
	_, address := adas(t, s)
	ctx := context.Background()

	for what, carried := range map[string]func() (net.Conn, error){
		"a port":    func() (net.Conn, error) { return adasOwn(t, address).Forward(ctx, 8080) },
		"the shell": func() (net.Conn, error) { return adasOwn(t, address).Shell(ctx) },
	} {
		_, err := carried()
		if !refusedAs(err, connect.CodeFailedPrecondition, "PORT_UNREACHED") {
			why, in := frontseat.Reason(err)
			t.Errorf("%s that is not reached: %v (%s, %s), want failed precondition, PORT_UNREACHED, %s", what, err, why, in, refusal.Workspace)
		}
		if err == nil || strings.Contains(err.Error(), word) || strings.Contains(err.Error(), "openshell") || strings.Contains(err.Error(), "sbx_1") {
			t.Errorf("%s that is not reached: the caller is told what was said of it: %v", what, err)
		}
	}
	status, body := asked(t, http.MethodGet, address+"/workspaces/adas/ports/8080", owner, "")
	if strings.Contains(body, word) || strings.Contains(body, "openshell") {
		t.Errorf("the answer says what was said of the port: %d %s", status, body)
	}
	if !strings.Contains(said(), word) {
		t.Errorf("what was said of the port is not logged: %q", said())
	}
	// The control: the port, reached.
	s.mu.Lock()
	s.unreached = nil
	s.mu.Unlock()
	conn, err := adasOwn(t, address).Forward(ctx, 8080)
	if err != nil {
		t.Fatalf("the control: the port, reached: %v", err)
	}
	if leads, back := spoken(t, conn, "a word"); leads != "sbx_1:8080\n" || back != "a word\n" {
		t.Errorf("the control: the port led to %q and said back %q", leads, back)
	}
}

// A workspace that is in no sandbox yet is refused its shell and a port
// by name, as any workspace that cannot be worked in yet:
// FAILED_PRECONDITION with WORKSPACE_NOT_READY, which the SDK reads. The
// control is the same workspace once it is in one.
func TestAWorkspaceInNoSandboxYetIsRefusedItsPortsByName(t *testing.T) {
	s := over(t)
	s.speaks = true
	s.refuses = "no sandbox is made just now"
	workspaces, _, address := servedToPeopleAt(t, s)
	ctx := context.Background()
	if _, err := workspaces(owner).CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "adas", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}

	_, forwarded := adasOwn(t, address).Forward(ctx, 8080)
	_, shell := adasOwn(t, address).Shell(ctx)
	for what, err := range map[string]error{"a port": forwarded, "the shell": shell} {
		if !refusedAs(err, connect.CodeFailedPrecondition, "WORKSPACE_NOT_READY") || !errors.Is(err, frontseat.ErrWorkspaceNotReady) {
			why, in := frontseat.Reason(err)
			t.Errorf("%s of a workspace in no sandbox yet: %v (%s, %s), want failed precondition, WORKSPACE_NOT_READY, %s", what, err, why, in, refusal.Workspace)
		}
	}
	if got := len(s.carried()); got != 0 {
		t.Errorf("a workspace in no sandbox was led into one %d times", got)
	}
	// The control: once it is in a sandbox.
	s.mu.Lock()
	s.refuses = ""
	s.mu.Unlock()
	if _, err := workspaces(owner).CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "adas", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	waited(t, workspaces(owner), "adas")
	conn, err := adasOwn(t, address).Forward(ctx, 8080)
	if err != nil {
		t.Fatalf("the control: a port, once the workspace is in a sandbox: %v", err)
	}
	if leads, _ := spoken(t, conn, "a word"); leads != "sbx_1:8080\n" {
		t.Errorf("the control: the port led to %q", leads)
	}
}

// A workspace is carried so many connections at once and no more: one
// beyond that is refused by name, RESOURCE_EXHAUSTED with
// TUNNELS_EXHAUSTED, and nothing is asked of its sandbox for it, until
// one of those carried has ended, when another is carried. It is each
// workspace's own: another's is carried the while.
func TestAWorkspaceIsCarriedSoManyConnectionsAtOnceAndNoMore(t *testing.T) {
	s := over(t)
	s.speaks = true
	workspaces, address := adas(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := workspaces(owner).CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "other", Spec: of("https://forge.example/acme/other.git")}); err != nil {
		t.Fatal(err)
	}
	waited(t, workspaces(owner), "other")
	var held []net.Conn
	for i := range most {
		conn, err := adasOwn(t, address).Forward(ctx, 8080)
		if err != nil {
			t.Fatalf("the connection %d of %d: %v", i+1, most, err)
		}
		held = append(held, conn)
	}

	_, err := adasOwn(t, address).Forward(ctx, 8080)
	if !refusedAs(err, connect.CodeResourceExhausted, "TUNNELS_EXHAUSTED") {
		why, in := frontseat.Reason(err)
		t.Errorf("one connection more than %d: %v (%s, %s), want resource exhausted, TUNNELS_EXHAUSTED, %s", most, err, why, in, refusal.Workspace)
	}
	if _, err := adasOwn(t, address).Shell(ctx); !refusedAs(err, connect.CodeResourceExhausted, "TUNNELS_EXHAUSTED") {
		t.Errorf("its shell, one connection more than %d: %v, want it refused the same", most, err)
	}
	if got := len(s.carried()); got != most {
		t.Errorf("its sandbox was connected to %d times, want %d: the one refused asks nothing of it", got, most)
	}
	// Another workspace is carried the while.
	client, err := frontseat.New(address, frontseat.WithToken(owner))
	if err != nil {
		t.Fatal(err)
	}
	if conn, err := client.Workspace("other").Forward(ctx, 8080); err != nil {
		t.Errorf("another workspace, while this one is carried as many as it may be: %v", err)
	} else {
		_ = conn.Close()
	}
	// One ends, and another is carried, once the control plane has seen
	// it end.
	_ = held[0].Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := adasOwn(t, address).Forward(ctx, 8080)
		if err == nil {
			_ = conn.Close()
			break
		}
		if !refusedAs(err, connect.CodeResourceExhausted, "TUNNELS_EXHAUSTED") || time.Now().After(deadline) {
			t.Fatalf("once one had ended, another: %v, want it carried", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A control plane that is closed ends every connection it carries, and
// has when Close returns: each has ended both ways, nothing reads or
// writes the connection to the port any more, and it is closed. The
// caller reads its end. One asked for afterwards is refused.
func TestAControlPlaneThatIsClosedHasEndedWhatItCarried(t *testing.T) {
	s := over(t)
	s.speaks = true
	client, address, plane := served(t, s, t.TempDir(), nil)
	ctx := context.Background()
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "adas", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "adas")
	var held []net.Conn
	for range 3 {
		conn, err := adasOwn(t, address).Forward(ctx, 8080)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		// It is carried: the port has said where it is.
		if leads, err := bufio.NewReader(conn).ReadString('\n'); err != nil || leads != "sbx_1:8080\n" {
			t.Fatalf("the port led to %q (%v)", leads, err)
		}
		held = append(held, conn)
	}

	closed := make(chan struct{})
	go func() { plane.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(20 * time.Second):
		t.Fatal("the control plane did not close while it carried connections: it ended none")
	}

	inside := s.carried()
	if len(inside) != 3 {
		t.Fatalf("the sandbox was connected to %d times, want 3", len(inside))
	}
	for i, each := range inside {
		if under, misused := each.Under(), each.Misused(); !each.Closed() || under != 0 || len(misused) != 0 {
			t.Errorf("once the control plane was closed, the connection %d to the port was closed: %v, with %d reading or writing it, %v; want it closed, and none", i, each.Closed(), under, misused)
		}
	}
	for i, conn := range held {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadAll(conn); errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("the caller of the connection %d did not read its end: %v", i, err)
		}
	}
	if _, err := adasOwn(t, address).Forward(ctx, 8080); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("a connection asked of a control plane that is closed: %v, want it unavailable", err)
	}
	if len(s.carried()) != 3 {
		t.Error("a control plane that is closed connected to a sandbox")
	}
}
