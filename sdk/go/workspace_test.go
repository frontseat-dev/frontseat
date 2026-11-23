package frontseat

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/coder/websocket"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// A workspace given no name is no workspace, and not the one Here is:
// whatever is asked of it fails before a server is asked, and says a
// name was not given, not that its source is fixed. The controls are a
// workspace that is named, for which the control plane is asked, and
// the one Here is, refused as itself.
func TestAWorkspaceGivenNoNameIsAskedNothing(t *testing.T) {
	server, asked := counting(t)
	ctx := context.Background()
	unnamed := client(t, server.URL).Workspace("")
	_, described := unnamed.Describe(ctx)
	_, built := unnamed.Build(ctx, BuildRequest{})
	_, shell := unnamed.Shell(ctx)
	_, port := unnamed.Forward(ctx, 8080)
	for what, err := range map[string]error{"describing": described, "building": built, "a shell": shell, "a port": port, "adding": unnamed.Add(ctx, WorkspaceSpec{}), "ending": unnamed.Delete(ctx)} {
		if !errors.Is(err, ErrWorkspaceUnnamed) || errors.Is(err, ErrHere) {
			t.Errorf("%s in a workspace given no name: %v, want it said that none was given", what, err)
		}
	}
	if got := asked.Load(); got != 0 {
		t.Errorf("a workspace given no name asked a server %d times", got)
	}
	if _, err := client(t, server.URL).Workspace("named").Describe(ctx); err == nil || errors.Is(err, ErrWorkspaceUnnamed) || asked.Load() != 1 {
		t.Errorf("the control: a workspace that is named: %v, asking %d times, want the control plane asked once", err, asked.Load())
	}
	if _, err := client(t, server.URL).Here().Describe(ctx); !errors.Is(err, ErrHere) {
		t.Errorf("the control: the workspace Here is, described: %v, want ErrHere", err)
	}
}

// A pattern that names no entity is refused before a control plane is
// asked; the control is one that does, which is asked.
func TestAPatternThatNamesNoEntityIsRefusedBeforeAnythingIsAsked(t *testing.T) {
	server, asked := counting(t)
	fs := client(t, server.URL)
	if _, err := fs.Open(context.Background(), WorkspaceSpec{Entities: []entity.Pattern{{Kind: "component", Name: "a b"}}}); err == nil || asked.Load() != 0 {
		t.Errorf("a pattern that is none: %v, asking %d times", err, asked.Load())
	}
	if _, err := fs.Open(context.Background(), WorkspaceSpec{Entities: []entity.Pattern{{Kind: "component", Name: "gateway"}}}); err == nil || asked.Load() != 1 {
		t.Errorf("the control: a pattern: %v, asking %d times", err, asked.Load())
	}
}

// counting is a server that answers nothing, and counts what it is
// asked.
func counting(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	asked := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return server, asked
}

// tunnelling is a control plane that opens a tunnel wherever it is
// asked for one, and says in it, on a line, the path it was asked at
// and what was presented to it, then says back what it is told; but
// beneath /workspaces/kept/ports/7601, which it refuses by name as a
// control plane does, and beneath /workspaces/starting, where it
// answers as one whose workspace is in no sandbox yet.
type tunnelling struct {
	mu    sync.Mutex
	asked []string
}

func (s *tunnelling) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.asked = append(s.asked, r.URL.EscapedPath())
	s.mu.Unlock()
	switch r.URL.Path {
	case "/workspaces/kept/ports/7601":
		refusal := connect.NewError(connect.CodeUnimplemented, "the test's control plane leads no one there")
		if named, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: "PORT_NOT_SERVED", Domain: DomainWorkspace}); err == nil {
			refusal = refusal.WithDetail(named)
		}
		_ = connecthttp.NewErrorWriter().Write(w, r, refusal)
		return
	case "/workspaces/starting/ports/8080", "/workspaces/starting/shell":
		http.Error(w, "the workspace is in no sandbox yet", http.StatusServiceUnavailable)
		return
	}
	socket, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer socket.CloseNow()
	said := []byte(r.URL.EscapedPath() + " " + r.Header.Get("Authorization") + "\n")
	for err == nil {
		if err = socket.Write(r.Context(), websocket.MessageBinary, said); err == nil {
			_, said, err = socket.Read(r.Context())
		}
	}
}

func (s *tunnelling) was() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
}

// A port of a workspace's sandbox is asked of the control plane that
// keeps the workspace, by the workspace's name and the port's number,
// with the token the Client presents there, and what comes back is a
// connection that carries bytes both ways. A port that is none is
// refused before anything is asked, and the SDK names no port of a
// sandbox itself: a shell is asked for as a shell. The control is a
// port at each end of what one is.
func TestAPortOfAWorkspaceIsAskedOfItsControlPlaneByItsNumber(t *testing.T) {
	plane := &tunnelling{}
	server := httptest.NewServer(plane)
	t.Cleanup(server.Close)
	ctx := context.Background()
	kept := client(t, server.URL, WithToken("a-token")).Workspace("kept")
	for _, port := range []int{0, -1, 65536, -8080, 70000} {
		conn, err := kept.Forward(ctx, port)
		if err == nil {
			conn.Close()
		}
		if !errors.Is(err, ErrPortInvalid) {
			t.Errorf("the port %d: %v, want it refused as none", port, err)
		}
	}
	if asked := plane.was(); len(asked) != 0 {
		t.Fatalf("for a port that is none, the control plane was asked %v", asked)
	}
	for port, path := range map[int]string{1: "/workspaces/kept/ports/1", 8080: "/workspaces/kept/ports/8080", 65535: "/workspaces/kept/ports/65535"} {
		conn, err := kept.Forward(ctx, port)
		if err != nil {
			t.Fatalf("the port %d: %v", port, err)
		}
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		read := bufio.NewReader(conn)
		if led, err := read.ReadString('\n'); err != nil || led != path+" Bearer a-token\n" {
			t.Errorf("the port %d was asked for as %q (%v), want %s with the token", port, led, err, path)
		}
		if _, err := conn.Write([]byte("a word\n")); err != nil {
			t.Fatal(err)
		}
		if back, err := read.ReadString('\n'); err != nil || back != "a word\n" {
			t.Errorf("the port %d said back %q (%v)", port, back, err)
		}
		conn.Close()
	}
	shell, err := kept.Shell(ctx)
	if err != nil {
		t.Fatal(err)
	}
	shell.Close()
	if asked := plane.was(); len(asked) != 4 || asked[3] != "/workspaces/kept/shell" {
		t.Errorf("the control plane was asked %v, want the three ports and then the shell, as a shell", asked)
	}
}

// What a control plane refuses in place of a tunnel is read as any
// refusal is: by its code and its reason, as an Error, for a port and
// for a shell alike. The control is an answer that is no refusal by
// name, which is said by its status and is no Error.
func TestATunnelRefusedByNameIsReadByItsReason(t *testing.T) {
	server := httptest.NewServer(&tunnelling{})
	t.Cleanup(server.Close)
	ctx := context.Background()
	fs := client(t, server.URL)
	_, err := fs.Workspace("kept").Forward(ctx, 7601)
	var refused *Error
	if !errors.As(err, &refused) || refused.Code != connect.CodeUnimplemented || refused.Reason != ReasonPortNotServed || refused.Domain != DomainWorkspace {
		t.Errorf("a port refused by name: %v, want an Error of unimplemented, %s, %s", err, ReasonPortNotServed, DomainWorkspace)
	}
	_, port := fs.Workspace("starting").Forward(ctx, 8080)
	_, shell := fs.Workspace("starting").Shell(ctx)
	for what, err := range map[string]error{"a port": port, "a shell": shell} {
		if err == nil || errors.As(err, &refused) || !strings.Contains(err.Error(), "503") {
			t.Errorf("%s of a workspace in no sandbox yet: %v, want it said by its status, as no refusal by name", what, err)
		}
	}
}
