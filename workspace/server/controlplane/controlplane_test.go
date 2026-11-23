package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/frontseat-dev/frontseat/common/told"
	sourcev1 "github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect"
	"github.com/frontseat-dev/frontseat/common/vcs"
	"github.com/frontseat-dev/frontseat/common/vcs/chosen"
	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/core/refusal"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/sdk/go"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"github.com/frontseat-dev/frontseat/testsupport/backstagetest"
)

// managing is the daemon of one sandbox of the tests, as its source
// API answers: it holds what it was told to, at the generation told,
// says so, and fails to bring it where the test has it fail.
type managing struct {
	mu         sync.Mutex
	spec       *sourcev1.SourceSpec
	told       int
	fails      string
	silent     bool
	generation int64
	// failed is why what it was last told to bring failed.
	failed string
	// refuses is the refusal it answers what it is told to bring with,
	// while it is set.
	refuses error
	// bringing is whether it says it is still bringing what it was told.
	bringing bool
	// stalls is whether it answers nothing, and keeps whoever asks
	// waiting for as long as they will wait, as a daemon too busy to
	// answer does.
	stalls atomic.Bool
	// only, set before stalls, is the one call that stalls; "" for any.
	only string
	// asked is told, by its name, each call that begins to stall, where
	// it is made.
	asked chan string
}

// was is how often it was told, and what it was last told to hold, as a
// workspace's spec says it.
func (m *managing) was() (int, *workspacev1.WorkspaceSpec) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.spec == nil {
		return m.told, nil
	}
	out := &workspacev1.WorkspaceSpec{}
	for _, r := range m.spec.GetRepositories() {
		out.Repositories = append(out.Repositories, &workspacev1.RepositorySource{Remote: r.GetRemote(), Ref: r.GetRef()})
	}
	return m.told, out
}

// restarted is the daemon started again: it holds what it brought, and
// was told nothing since, as a daemon keeps no spec.
func (m *managing) restarted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation = 0
}

func (m *managing) now() *sourcev1.Source {
	out := &sourcev1.Source{State: sourcev1.Source_STATE_READY, Generation: m.generation}
	if m.bringing {
		out.State = sourcev1.Source_STATE_POPULATING
	}
	if m.failed != "" {
		out.State, out.Failure = sourcev1.Source_STATE_FAILED, m.failed
	} else {
		for _, repository := range m.spec.GetRepositories() {
			out.Repositories = append(out.Repositories, &sourcev1.Repository{Name: "held", Remote: repository.GetRemote(), Commit: "0123abcd"})
		}
	}
	return out
}

// stalled is, where it stalls, why a call answered nothing: it says
// the call began to stall, where it is asked to, and keeps it until
// its caller gives up. Nil where it answers.
func (m *managing) stalled(ctx context.Context, call string) error {
	if !m.stalls.Load() || m.only != "" && m.only != call {
		return nil
	}
	if m.asked != nil {
		select {
		case m.asked <- call:
		default:
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func (m *managing) WaitSource(ctx context.Context, _ *sourcev1.WaitSourceRequest) (*sourcev1.WaitSourceResponse, error) {
	if err := m.stalled(ctx, "WaitSource"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.silent {
		return nil, connect.NewError(connect.CodeUnavailable, "the test's daemon says nothing")
	}
	return &sourcev1.WaitSourceResponse{Source: m.now()}, nil
}

func (m *managing) SetSource(ctx context.Context, req *sourcev1.SetSourceRequest) (*sourcev1.SetSourceResponse, error) {
	if err := m.stalled(ctx, "SetSource"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.refuses != nil {
		return nil, m.refuses
	}
	m.spec, m.told, m.failed, m.generation = req.GetSpec(), m.told+1, m.fails, req.GetSpec().GetGeneration()
	return &sourcev1.SetSourceResponse{Source: m.now()}, nil
}

// sandboxes is a sandbox service of the tests: it makes sandboxes that
// are records and a daemon each, and keeps what it was asked.
type sandboxes struct {
	sandboxv1alpha1connect.UnimplementedSandboxServiceHandler
	t *testing.T
	// starts is the state a sandbox is made in.
	starts sandboxv1.Sandbox_State
	// kept are the credentials kept, by id: the host each is for.
	kept map[string]string
	// inUse are the credentials a sandbox is let through with, by id.
	inUse map[string]bool
	// keepsNot is what SetCredential refuses with, where it is set.
	keepsNot error
	// fails, silent and refusing are what each daemon made from now on
	// does.
	fails    string
	silent   bool
	refusing error
	// variable is what a kept credential's stand-in is said to be under;
	// empty for a plain one.
	variable string
	// refuses is why a sandbox is not made, while it is said.
	refuses string

	mu      sync.Mutex
	made    map[string]*sandboxv1.CreateSandboxRequest // by sandbox
	named   map[string]string                          // request to sandbox
	order   []string
	state   map[string]sandboxv1.Sandbox_State
	daemons map[string]*managing
	servers map[string]*httptest.Server
	deleted []string
	resumed []string
	led     []string
	// speaks is whether a tunnel is opened, and not only kept as led on:
	// it says which sandbox and port it leads to, on a line, and then
	// says back whatever it is told.
	speaks bool
}

func over(t *testing.T) *sandboxes {
	return &sandboxes{t: t, starts: sandboxv1.Sandbox_STATE_RUNNING, kept: map[string]string{}, inUse: map[string]bool{}, made: map[string]*sandboxv1.CreateSandboxRequest{}, named: map[string]string{}, state: map[string]sandboxv1.Sandbox_State{}, daemons: map[string]*managing{}, servers: map[string]*httptest.Server{}}
}

func (s *sandboxes) CreateSandbox(_ context.Context, asked *sandboxv1.CreateSandboxRequest) (*sandboxv1.CreateSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refuses != "" {
		return nil, connect.NewError(connect.CodeUnavailable, s.refuses)
	}
	id, again := s.named[asked.GetRequestId()]
	if !again {
		id = fmt.Sprintf("sbx_%d", len(s.order)+1)
		s.made[id], s.named[asked.GetRequestId()], s.state[id] = asked, id, s.starts
		s.order = append(s.order, id)
		daemon := &managing{fails: s.fails, silent: s.silent, refuses: s.refusing}
		api := connect.NewServer()
		sourcev1alpha1connect.RegisterSourceServiceHandler(api, daemon)
		mux := http.NewServeMux()
		connecthttp.Mount(mux, api)
		server := httptest.NewServer(mux)
		s.t.Cleanup(server.Close)
		s.daemons[id], s.servers[id] = daemon, server
	}
	return &sandboxv1.CreateSandboxResponse{Sandbox: &sandboxv1.Sandbox{Id: id, Spec: asked.GetSpec()}}, nil
}

func (s *sandboxes) DescribeSandbox(_ context.Context, asked *sandboxv1.DescribeSandboxRequest) (*sandboxv1.DescribeSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.made[asked.GetSandboxId()] == nil {
		return nil, connect.NewError(connect.CodeNotFound, "no such sandbox")
	}
	return &sandboxv1.DescribeSandboxResponse{Sandbox: &sandboxv1.Sandbox{Id: asked.GetSandboxId(), State: s.state[asked.GetSandboxId()]}}, nil
}

func (s *sandboxes) ListSandboxes(_ context.Context, asked *sandboxv1.ListSandboxesRequest) (*sandboxv1.ListSandboxesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := &sandboxv1.ListSandboxesResponse{}
	for _, id := range s.order {
		made := s.made[id]
		labelled := made != nil
		for label, value := range asked.GetLabels() {
			labelled = labelled && made.GetSpec().GetLabels()[label] == value
		}
		if labelled {
			out.Sandboxes = append(out.Sandboxes, &sandboxv1.Sandbox{Id: id, State: s.state[id], Spec: made.GetSpec()})
		}
	}
	return out, nil
}

func (s *sandboxes) DeleteSandbox(_ context.Context, asked *sandboxv1.DeleteSandboxRequest) (*sandboxv1.DeleteSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.made[asked.GetSandboxId()] == nil {
		return nil, connect.NewError(connect.CodeNotFound, "no such sandbox")
	}
	s.deleted = append(s.deleted, asked.GetSandboxId())
	for request, id := range s.named {
		if id == asked.GetSandboxId() {
			delete(s.named, request)
		}
	}
	delete(s.made, asked.GetSandboxId())
	return &sandboxv1.DeleteSandboxResponse{}, nil
}

func (s *sandboxes) ResumeSandbox(_ context.Context, asked *sandboxv1.ResumeSandboxRequest) (*sandboxv1.ResumeSandboxResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resumed, s.state[asked.GetSandboxId()] = append(s.resumed, asked.GetSandboxId()), sandboxv1.Sandbox_STATE_RUNNING
	return &sandboxv1.ResumeSandboxResponse{}, nil
}

func (s *sandboxes) DescribeCredential(_ context.Context, asked *sandboxv1.DescribeCredentialRequest) (*sandboxv1.DescribeCredentialResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	host, ok := s.kept[asked.GetName()]
	if !ok {
		return nil, refusal.New(refusal.Sandbox, connect.CodeNotFound, "CREDENTIAL_NOT_FOUND", "none is kept")
	}
	variable := "A_STAND_IN"
	if s.variable != "" {
		variable = s.variable
	}
	return &sandboxv1.DescribeCredentialResponse{Credential: &sandboxv1.Credential{Name: asked.GetName(), Host: host, Username: "someone", Variable: variable}}, nil
}

func (s *sandboxes) SetCredential(_ context.Context, asked *sandboxv1.SetCredentialRequest) (*sandboxv1.SetCredentialResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keepsNot != nil {
		return nil, s.keepsNot
	}
	s.kept[asked.GetCredential().GetName()] = asked.GetCredential().GetHost()
	return &sandboxv1.SetCredentialResponse{}, nil
}

func (s *sandboxes) DeleteCredential(_ context.Context, asked *sandboxv1.DeleteCredentialRequest) (*sandboxv1.DeleteCredentialResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inUse[asked.GetName()] {
		return nil, refusal.New(refusal.Sandbox, connect.CodeFailedPrecondition, "CREDENTIAL_IN_USE", "a sandbox is still let through with it")
	}
	delete(s.kept, asked.GetName())
	return &sandboxv1.DeleteCredentialResponse{}, nil
}

// keeps keeps a credential for a host, as its owner's control plane asks
// the sandbox service to.
func (s *sandboxes) keeps(owner, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kept[credentialID(owner, host)] = host
}

func (s *sandboxes) Dial(ctx context.Context, id string, port int) (net.Conn, error) {
	s.mu.Lock()
	server := s.servers[id]
	gone := s.made[id] == nil
	s.mu.Unlock()
	if server == nil || gone || port != ports.Source {
		return nil, errors.New("the test's sandbox holds nothing there")
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
}

func (s *sandboxes) Proxy(w http.ResponseWriter, r *http.Request, id string, port int, rest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.led = append(s.led, fmt.Sprintf("%s:%d/%s", id, port, rest))
}

func (s *sandboxes) Tunnel(w http.ResponseWriter, r *http.Request, id string, port int) {
	s.Proxy(w, r, id, port, "a tunnel")
	if !s.speaks {
		return
	}
	socket, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	inside := websocket.NetConn(r.Context(), socket, websocket.MessageBinary)
	defer inside.Close()
	fmt.Fprintf(inside, "%s:%d\n", id, port)
	_, _ = io.Copy(inside, inside)
}

// asked are the sandboxes that were asked for, in order, and ended those
// that were ended.
func (s *sandboxes) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.order)
}

func (s *sandboxes) ended() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.deleted)
	slices.Sort(out)
	return out
}

func (s *sandboxes) sandbox(id string) *sandboxv1.SandboxSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.made[id].GetSpec()
}

func (s *sandboxes) set(id string, state sandboxv1.Sandbox_State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state[id] = state
}

// of is the sandbox a workspace lives in, as the test's sandboxes keep
// them: the last made that carries its name and is not ended.
func (s *sandboxes) of(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := ""
	for _, id := range s.order {
		if made := s.made[id]; made != nil && made.GetSpec().GetLabels()[Named] == name {
			found = id
		}
	}
	return found
}

func (s *sandboxes) daemon(id string) *managing {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.daemons[id]
}

// system is a version control system that says of a fetch what a test
// can read back.
type system struct{}

func (system) Requests(repository string) []vcs.Request {
	return []vcs.Request{{Method: "GET", Path: repository + "/refs", Query: map[string]string{"service": "a-fetch"}}, {Method: "POST", Path: repository + "/a-fetch"}}
}
func (system) Pushes(repository string) []vcs.Request {
	return []vcs.Request{{Method: "POST", Path: repository + "/a-push"}}
}
func (system) Programs() []string { return []string{"/usr/bin/a-fetcher"} }

// Cloned reads a remote as the system a daemon has reads it
// (chosen.System), since what the control plane refuses of a remote,
// and what it writes a rule for, is held to what the daemon in the
// sandbox would clone.
func (system) Cloned(remote string) (string, error) {
	return chosen.System().Cloned(remote)
}
func (system) NameOf(remote string) string {
	return strings.TrimSuffix(path.Base(strings.TrimSuffix(remote, "/")), ".git")
}
func (system) Tells() string { return "A_SYSTEM_" }
func (system) Presenting(_ map[string]string, origin, username, variable string) (map[string]string, error) {
	return map[string]string{"A_SYSTEM_PRESENTS": username + " at " + origin + " by " + variable}, nil
}
func (system) Authoring(name, email string) map[string]string {
	if name == "" {
		return nil
	}
	return map[string]string{"A_SYSTEM_AUTHOR": name + " <" + email + ">"}
}

// served serves a control plane over a test's sandboxes, keeping its
// records where it is told, and returns a client, the address, and what
// stops it.
func served(t *testing.T, s *sandboxes, state string, change func(*Config)) (workspacev1alpha1connect.WorkspaceServiceClient, string, *Plane) {
	t.Helper()
	return serve(t, func(address string) Config {
		c := Config{
			Sandboxes: s, PublicURL: address, State: state, Box: "registry.example/acme/box:1", Builder: "/opt/a/builder",
			Grid:     Grid{Hosts: []string{"grid.example:8980", "assets.example:8983"}, Arguments: []string{"--grid=grid.example:8980", "--assets=assets.example:8983", "--platform=a=b"}},
			Fetching: system{}, Every: 10 * time.Millisecond, Answers: 300 * time.Millisecond, Callers: knowing(t),
		}
		if change != nil {
			change(&c)
		}
		return c
	})
}

// serve serves a control plane of the configuration made for the address
// it is served at, which it stops when the test ends, and returns a client,
// the address, and the plane.
func serve(t *testing.T, configured func(address string) Config) (workspacev1alpha1connect.WorkspaceServiceClient, string, *Plane) {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	server.Start()
	plane, err := Serve(configured(server.URL))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	server.Config.Handler = plane
	t.Cleanup(func() { plane.Close(); server.Close() })
	return workspacev1alpha1connect.NewWorkspaceServiceClient(connect.NewClient(connecthttp.NewTransport(bearing(owner), server.URL))), server.URL, plane
}

// made serves a control plane over a test's sandboxes and asks it for a
// workspace of a name and a spec, and returns the sandboxes and a client.
func made(t *testing.T, name string, spec *workspacev1.WorkspaceSpec) (*sandboxes, workspacev1alpha1connect.WorkspaceServiceClient) {
	t.Helper()
	s := over(t)
	client, _, _ := served(t, s, t.TempDir(), nil)
	if _, err := client.CreateWorkspace(context.Background(), &workspacev1.CreateWorkspaceRequest{Name: name, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	return s, client
}

func of(remotes ...string) *workspacev1.WorkspaceSpec {
	spec := &workspacev1.WorkspaceSpec{}
	for _, remote := range remotes {
		spec.Repositories = append(spec.Repositories, &workspacev1.RepositorySource{Remote: remote})
	}
	return spec
}

// waited follows a workspace until waiting for it ends, and returns it
// as it then was.
func waited(t *testing.T, client workspacev1alpha1connect.WorkspaceServiceClient, name string) *workspacev1.Workspace {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	seen, err := client.WaitWorkspace(ctx, &workspacev1.WaitWorkspaceRequest{Name: name})
	if err != nil {
		t.Fatalf("waiting for %s: %v", name, err)
	}
	return seen.GetWorkspace()
}

// gone waits for a workspace to be there no more.
func gone(t *testing.T, client workspacev1alpha1connect.WorkspaceServiceClient, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := client.DescribeWorkspace(context.Background(), &workspacev1.DescribeWorkspaceRequest{Name: name})
		if connect.CodeOf(err) == connect.CodeNotFound {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the workspace %s is still there: %v", name, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A workspace is named by what it was first asked for where no name is
// given, and what is no workspace is refused before anything is kept or
// made: nothing asked for, a name that is none, and a repository's
// address that holds a user or a password, which a record would say to
// whoever lists workspaces.
func TestAWorkspaceIsNamedByWhatItWasAskedFor(t *testing.T) {
	for want, spec := range map[string]*workspacev1.WorkspaceSpec{
		"code":                  of("https://forge.example/acme/code.git"),
		"code-main":             {Repositories: []*workspacev1.RepositorySource{{Remote: "https://forge.example/acme/code.git", Ref: "main"}}},
		"code-feature-x-y":      {Repositories: []*workspacev1.RepositorySource{{Remote: "https://forge.example/acme/Code/", Ref: "feature/X_y"}}},
		"payments":              {Entities: []*typev1.EntityRef{{Kind: "component", Namespace: "default", Name: "payments"}}, Repositories: of("https://forge.example/acme/code.git").Repositories},
		"d":                     of("git@forge.example:acme/d.git"),
		strings.Repeat("a", 48): of("https://forge.example/acme/" + strings.Repeat("a", 60)),
		"":                      of("https://forge.example/"),
	} {
		if got := derived(spec); got != want {
			t.Errorf("%v is named %q, want %q", spec, got, want)
		}
	}
	s := over(t)
	client, _, _ := served(t, s, t.TempDir(), nil)
	ctx := context.Background()
	for what, request := range map[string]*workspacev1.CreateWorkspaceRequest{
		"nothing asked for":          {Name: "a-name", Spec: &workspacev1.WorkspaceSpec{}},
		"what gives no name":         {Spec: of("https://forge.example/")},
		"a name in capitals":         {Name: "A-Name", Spec: of("https://forge.example/acme/code")},
		"a name that climbs":         {Name: "../a", Spec: of("https://forge.example/acme/code")},
		"a name that ends in a dash": {Name: "a-", Spec: of("https://forge.example/acme/code")},
		"a name too long":            {Name: strings.Repeat("a", 49), Spec: of("https://forge.example/acme/code")},
		"a password in an address":   {Name: "a-name", Spec: of("https://someone:a-made-up-secret@forge.example/acme/code")},
		"a user in an address":       {Name: "a-name", Spec: of("https://someone@forge.example/acme/code")},
	} {
		if _, err := client.CreateWorkspace(ctx, request); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want it refused as not well formed", what, err)
		}
	}
	if listed, err := client.ListWorkspaces(ctx, &workspacev1.ListWorkspacesRequest{}); err != nil || len(listed.GetWorkspaces()) != 0 || len(s.asked()) != 0 {
		t.Errorf("what was refused left %v and %d sandboxes (%v)", listed.GetWorkspaces(), len(s.asked()), err)
	}
}

// The sandbox a workspace lives in is written in the sandbox layer's own
// terms, by rules: a host fetched from for the requests a fetch of each
// repository asks, by the fetching programs, with the host's credential
// where one is kept and a push only where the workspace pushes; the grid
// and its asset service by the daemon's program. A repository whose
// remote the system clones over https, though it is written otherwise,
// is fetched by the rule of the host it is cloned from. It is told the
// workspace's name, who commits and whose key gets in.
func TestTheSandboxAWorkspaceLivesInIsWrittenAsRules(t *testing.T) {
	s := over(t)
	s.keeps("local", "forge.example:443")
	client, _, _ := served(t, s, t.TempDir(), nil)
	ctx := context.Background()
	spec := &workspacev1.WorkspaceSpec{
		Repositories: []*workspacev1.RepositorySource{
			{Remote: "https://forge.example/acme/a.git"},
			{Remote: "https://Forge.Example/acme/b"},
			{Remote: "http://plain.example:8123/code"},
			{Remote: "git@forge.example:acme/d.git"},
		},
		Author:        &workspacev1.Author{Name: "Someone", Email: "someone@forge.example"},
		AuthorizedKey: "ssh-ed25519 AAAA a-key",
	}
	made, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "a-workspace", Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	ready := waited(t, client, "a-workspace")
	if !made.GetCreated() || ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		t.Fatalf("it was made %v and is %v", made.GetCreated(), ready.GetStatus())
	}
	in := s.sandbox(s.of(ready.GetName()))
	if in.GetLabels()[Named] != "a-workspace" || in.GetImage() != "registry.example/acme/box:1" || !slices.Equal(in.GetPorts(), []int32{7601, 2222}) || in.GetEgress().GetMode() != sandboxv1.Egress_MODE_ALLOW_LISTED {
		t.Errorf("its sandbox carries %v, runs %q, serves %v and reaches by %v", in.GetLabels(), in.GetImage(), in.GetPorts(), in.GetEgress().GetMode())
	}
	// Its daemon is told by its flags, and nothing of where the daemon is
	// is told the command line beside it, which finds its daemon itself.
	if told, want := in.GetArguments(), []string{"--grid=grid.example:8980", "--assets=assets.example:8983", "--platform=a=b", "--name=a-workspace", "--authorized-key=ssh-ed25519 AAAA a-key"}; !slices.Equal(told, want) {
		t.Errorf("its daemon is told %q, want %q", told, want)
	}
	want := map[string]string{
		"A_SYSTEM_AUTHOR": "Someone <someone@forge.example>", "A_SYSTEM_PRESENTS": "someone at https://forge.example:443 by A_STAND_IN",
	}
	if got := in.GetEnvironment(); len(got) != len(want) {
		t.Errorf("it is told %v, want %v", got, want)
	}
	for name, value := range want {
		if in.GetEnvironment()[name] != value {
			t.Errorf("it is told %s=%q, want %q", name, in.GetEnvironment()[name], value)
		}
	}
	fetch := func(repository string) []*sandboxv1.EgressRequest {
		return []*sandboxv1.EgressRequest{{Method: "GET", Path: repository + "/refs", Query: map[string]string{"service": "a-fetch"}}, {Method: "POST", Path: repository + "/a-fetch"}}
	}
	rules := []*sandboxv1.EgressRule{
		{Host: "forge.example:443", Programs: []string{"/usr/bin/a-fetcher"}, Credential: credentialID("local", "forge.example:443"), Requests: append(append(fetch("/acme/a.git"), fetch("/acme/b")...), fetch("/acme/d.git")...)},
		{Host: "http://plain.example:8123", Programs: []string{"/usr/bin/a-fetcher"}, Requests: fetch("/code")},
		{Host: "grid.example:8980", Programs: []string{"/opt/a/builder"}},
		{Host: "assets.example:8983", Programs: []string{"/opt/a/builder"}},
	}
	got := in.GetEgress().GetRules()
	if len(got) != len(rules) {
		t.Fatalf("it reaches by the rules\n%v\nwant\n%v", got, rules)
	}
	for i := range rules {
		if !proto.Equal(got[i], rules[i]) {
			t.Errorf("rule %d is\n%v\nwant\n%v", i, got[i], rules[i])
		}
	}
	// Its daemon was told what it is to hold, once, and what it holds is
	// the daemon's word.
	if told, holds := s.daemon(s.of(ready.GetName())).was(); told != 1 || !proto.Equal(source(holds), source(spec)) || len(ready.GetStatus().GetRepositories()) != len(spec.GetRepositories()) {
		t.Errorf("its daemon was told %d times to hold %v, and is said to hold %v", told, holds, ready.GetStatus().GetRepositories())
	}

	// A push adds what a push asks, of each repository, and is of
	// repositories fetched alone; the control is the one taken.
	pushing := of("https://forge.example/acme/a.git")
	pushing.Push = true
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "pushing", Spec: pushing}); err != nil {
		t.Fatalf("a push to a repository fetched: %v", err)
	}
	pushed := waited(t, client, "pushing")
	requests := s.sandbox(s.of(pushed.GetName())).GetEgress().GetRules()[0].GetRequests()
	if len(requests) != 3 || requests[2].GetPath() != "/acme/a.git/a-push" {
		t.Errorf("a workspace that pushes is let through for %v", requests)
	}
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "refused", Spec: &workspacev1.WorkspaceSpec{Repositories: of("https://forge.example/acme/a.git").Repositories, AuthorizedKey: "ssh-ed25519 AAAA\nssh-ed25519 BBBB"}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a key of two lines: %v, want it refused", err)
	}
	none, _, _ := served(t, over(t), t.TempDir(), func(c *Config) { c.Box = "" })
	if _, err := none.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "no-image", Spec: of("https://forge.example/acme/a.git")}); !refusedAsNotServed(err, "IMAGE_NOT_SERVED") {
		t.Errorf("a control plane that names no box image: %v, want it refused as not served", err)
	}
	if len(s.asked()) != 2 {
		t.Errorf("%d sandboxes were asked for in all, want the two that were taken", len(s.asked()))
	}
}

// A workspace asked for again is the one there is, however many ask at
// once. Asked for with less than it has, or with no key, it is still
// that one; asked for with more, with other source, or with another key
// let into its shell, which is said once, it is refused by name and is
// as it was. Its daemon and its shell are led to by its name.
func TestAWorkspaceAskedForAgainIsTheOneThereIs(t *testing.T) {
	s := over(t)
	client, address, _ := served(t, s, t.TempDir(), nil)
	ctx := context.Background()
	spec := func(key string) *workspacev1.WorkspaceSpec {
		out := of("https://forge.example/acme/code.git")
		out.Push, out.AuthorizedKey = true, key
		return out
	}
	answers := make(chan *workspacev1.CreateWorkspaceResponse, 8)
	for range 8 {
		go func() {
			made, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Spec: spec("ssh-ed25519 AAAA a-key")})
			if err != nil {
				t.Error(err)
			}
			answers <- made
		}()
	}
	created := 0
	for range 8 {
		if made := <-answers; made.GetCreated() {
			created++
		} else if made.GetWorkspace().GetName() != "code" {
			t.Errorf("one of eight was given %q", made.GetWorkspace().GetName())
		}
	}
	first := waited(t, client, "code")
	if created != 1 || len(s.asked()) != 1 || s.of(first.GetName()) != "sbx_1" {
		t.Fatalf("%d of eight made it, in %v: want one, in one sandbox", created, s.asked())
	}
	// With less than it has: without the push it was made with.
	plain := of("https://forge.example/acme/code.git")
	if again, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "code", Spec: plain}); err != nil || again.GetCreated() || !again.GetWorkspace().GetSpec().GetPush() || again.GetWorkspace().GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		t.Errorf("asked for without the push it has: %v, %v; want the one there is, as it is", again, err)
	}
	other := of("https://forge.example/acme/another.git")
	image := spec("ssh-ed25519 AAAA a-key")
	image.Image = "registry.example/acme/another:2"
	key := spec("ssh-ed25519 AAAA another-key")
	for what, more := range map[string]*workspacev1.WorkspaceSpec{"other source": other, "another image": image, "another key": key} {
		if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "code", Spec: more}); connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Errorf("asked for again with %s: %v, want it refused as there already", what, err)
		}
	}
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "code", Spec: key}); err == nil || !strings.Contains(err.Error(), "another key") {
		t.Errorf("asked for again with another key: %v, want it refused saying so", err)
	}
	// The control of "more": one made without push, asked for with it.
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "plain", Spec: plain}); err != nil {
		t.Fatal(err)
	}
	// Its sandbox is asked for once creating it has returned: what was
	// asked is counted once it is there.
	waited(t, client, "plain")
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "plain", Spec: spec("ssh-ed25519 AAAA")}); connect.CodeOf(err) != connect.CodeAlreadyExists || !strings.Contains(err.Error(), "without push") {
		t.Errorf("one made without push, asked for with it: %v, want it refused by name", err)
	}
	if len(s.asked()) != 2 || len(s.ended()) != 0 {
		t.Errorf("asking again made %v and ended %v", s.asked(), s.ended())
	}
	if told, _ := s.daemon("sbx_1").was(); told != 1 {
		t.Errorf("its daemon was told %d times, want once", told)
	}
	for path, status := range map[string]int{"/workspaces/code/shell": 200, "/workspaces/another/shell": 404} {
		if got, said := asked(t, http.MethodGet, address+path, owner, ""); got != status {
			t.Errorf("%s answered %d %s, want %d", path, got, said, status)
		}
	}
	s.mu.Lock()
	led := slices.Clone(s.led)
	s.mu.Unlock()
	if want := []string{"sbx_1:2222/a tunnel"}; !slices.Equal(led, want) {
		t.Errorf("what was led on is %v, want %v", led, want)
	}
	if first.GetOwner() != "local" {
		t.Errorf("it is %q's", first.GetOwner())
	}
}

// A control plane that starts again goes on from what was asked for: a
// workspace whose sandbox was not yet running, and whose daemon was told
// nothing, is made ready by the one that comes after. The control is the
// first, which could not.
func TestAControlPlaneThatStartsAgainGoesOnFromWhatWasAskedFor(t *testing.T) {
	s := over(t)
	s.starts = sandboxv1.Sandbox_STATE_STARTING
	state := t.TempDir()
	client, _, first := served(t, s, state, nil)
	ctx := context.Background()
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "kept", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.asked()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "kept"}); err != nil || got.GetWorkspace().GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_STARTING {
		t.Fatalf("under the first it is %v (%v), want starting", got.GetWorkspace().GetStatus(), err)
	}
	first.Close()
	s.set("sbx_1", sandboxv1.Sandbox_STATE_RUNNING)
	after, _, _ := served(t, s, state, nil)
	ready := waited(t, after, "kept")
	if ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY || s.of(ready.GetName()) != "sbx_1" || func() bool { n, _ := s.daemon("sbx_1").was(); return n != 1 }() || len(s.asked()) != 1 {
		t.Errorf("under the one that came after it is %v, in %v", ready.GetStatus(), s.asked())
	}
}

// A record of a workspace that names no owner is none this server
// wrote: a control plane that finds one does not start, and names its
// file, so that no one's workspace is handed to every caller. The
// control is the same record with its owner, which is taken up.
func TestARecordOfNoOnesWorkspaceIsRefusedWhenTheControlPlaneStarts(t *testing.T) {
	s := over(t)
	state := t.TempDir()
	client, address, first := served(t, s, state, nil)
	if _, err := client.CreateWorkspace(context.Background(), &workspacev1.CreateWorkspaceRequest{Name: "kept", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	first.Close()
	file := filepath.Join(state, "kept.json")
	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(written, &record); err != nil {
		t.Fatal(err)
	}
	for _, without := range []string{"owner", "generation"} {
		lacking := maps.Clone(record)
		delete(lacking, without)
		raw, _ := json.Marshal(lacking)
		if err := os.WriteFile(file, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		plane, err := Serve(Config{Sandboxes: s, PublicURL: address, State: state, Callers: knowing(t)})
		if err == nil {
			plane.Close()
			t.Errorf("a record without its %s was taken up", without)
		} else if !strings.Contains(err.Error(), file) || !strings.Contains(err.Error(), "remove the file") {
			t.Errorf("a record without its %s: %v, want its file named", without, err)
		}
	}
	// A record is read strictly: one that holds what this server does
	// not know is not read in part.
	more := maps.Clone(record)
	more["another"] = true
	raw, _ := json.Marshal(more)
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if plane, err := Serve(Config{Sandboxes: s, PublicURL: address, State: state, Callers: knowing(t)}); err == nil {
		plane.Close()
		t.Error("a record of a field unknown was taken up")
	} else if !strings.Contains(err.Error(), file) {
		t.Errorf("a record of a field unknown: %v, want its file named", err)
	}
	if err := os.WriteFile(file, written, 0o600); err != nil {
		t.Fatal(err)
	}
	after, _, _ := served(t, s, state, nil)
	if _, err := after.DescribeWorkspace(context.Background(), &workspacev1.DescribeWorkspaceRequest{Name: "kept"}); err != nil {
		t.Errorf("the control: the record with its owner is not taken up: %v", err)
	}
}

// A workspace that is ended is gone promptly though its daemon answers
// nothing, as one too busy to does: the ending does not wait on what
// was being asked of the daemon. The control is the same workspace's
// daemon answering, which is gone as promptly.
func TestAWorkspaceIsEndedPromptlyThoughItsDaemonAnswersNothing(t *testing.T) {
	for _, stalls := range []bool{false, true} {
		s, client := made(t, "x", of("https://forge.example/acme/code.git"))
		waited(t, client, "x")
		daemon := s.daemon(s.of("x"))
		if stalls {
			// A pass that asks it, and waits on it, before the ending is
			// asked for.
			daemon.asked = make(chan string, 1)
			daemon.stalls.Store(true)
			select {
			case <-daemon.asked:
			case <-time.After(5 * time.Second):
				t.Fatal("no pass asked its daemon")
			}
		}
		endedPromptly(t, client, "x", fmt.Sprintf("its daemon stalling %v", stalls))
	}
}

// endedPromptly ends a workspace and waits for it to be gone, and fails
// the test where that took more than a second.
func endedPromptly(t *testing.T, client workspacev1alpha1connect.WorkspaceServiceClient, name, how string) {
	t.Helper()
	started := time.Now()
	if _, err := client.DeleteWorkspace(context.Background(), &workspacev1.DeleteWorkspaceRequest{Name: name}); err != nil {
		t.Fatal(err)
	}
	gone(t, client, name)
	if took := time.Since(started); took > time.Second {
		t.Errorf("%s, it was gone after %s, not promptly", how, took.Round(time.Millisecond))
	}
}

// A workspace that is ended while it is being updated, its daemon told
// what is added and answering nothing, is gone promptly, and the update
// is refused as one of a workspace that is ending. The control is the
// same update with nothing ended, which waits for the daemon still.
func TestAWorkspaceEndedWhileItIsUpdatedIsGonePromptly(t *testing.T) {
	s, client := made(t, "x", of("https://forge.example/acme/code.git"))
	waited(t, client, "x")
	daemon := s.daemon(s.of("x"))
	daemon.asked, daemon.only = make(chan string, 8), "SetSource"
	daemon.stalls.Store(true)
	updated := make(chan error, 1)
	go func() {
		more := of("https://forge.example/acme/code.git")
		more.WholeRepositories = true
		_, err := client.UpdateWorkspace(context.Background(), &workspacev1.UpdateWorkspaceRequest{Name: "x", Spec: more})
		updated <- err
	}()
	for until := time.After(5 * time.Second); ; {
		select {
		case call := <-daemon.asked:
			if call != "SetSource" {
				continue
			}
		case err := <-updated:
			t.Fatalf("the update ended before it told its daemon: %v", err)
		case <-until:
			t.Fatal("the update did not tell its daemon")
		}
		break
	}
	select {
	case err := <-updated:
		t.Fatalf("the control: the update did not wait for its daemon: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	endedPromptly(t, client, "x", "ended while it was updated")
	if err := <-updated; func() string { why, _ := reason(t, err); return why }() != "WORKSPACE_ENDING" {
		t.Errorf("the update ended as %v, not refused as of a workspace that is ending", err)
	}
}

// A workspace that is ended is gone with every sandbox that carries its
// name, one a stopped making left behind among them, and is refused
// while it ends. Its name is then another workspace's to take: made
// anew, in a sandbox of its own asked for by another request, told and
// ready, with nothing of the first.
func TestAWorkspaceThatIsEndedIsGoneAndItsNameCanBeGivenAgain(t *testing.T) {
	s, client := made(t, "x", of("https://forge.example/acme/code.git"))
	ctx := context.Background()
	first := waited(t, client, "x")
	// A sandbox left by a making that was stopped, whose answer was lost,
	// carries the name too, and the request the control plane kept
	// before it asked.
	lived := s.of(first.GetName())
	request := s.sandbox(lived).GetLabels()[Made]
	if request == "" {
		t.Fatalf("the workspace's sandbox carries no request: %v", s.sandbox(s.of(first.GetName())).GetLabels())
	}
	if _, err := s.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{RequestId: "left-behind", Spec: &sandboxv1.SandboxSpec{Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_ALL}, Labels: map[string]string{Named: "x", Made: request}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	gone(t, client, "x")
	if !slices.Equal(s.ended(), []string{"sbx_1", "sbx_2"}) {
		t.Errorf("ending it ended the sandboxes %v, want its own and the one left behind", s.ended())
	}
	if _, err := client.DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: "x"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("one that is gone, ended again: %v, want it not found", err)
	}
	again, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "x", Spec: of("https://forge.example/acme/another.git")})
	if err != nil || !again.GetCreated() {
		t.Fatalf("the name given again: %v, %v", again, err)
	}
	second := waited(t, client, "x")
	if second.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY || s.of(second.GetName()) == lived || func() bool { n, _ := s.daemon(s.of(second.GetName())).was(); return n != 1 }() {
		t.Errorf("the second of the name is %v", second.GetStatus())
	}
	if got := second.GetStatus().GetRepositories(); len(got) != 1 || got[0].GetRemote() != "https://forge.example/acme/another.git" {
		t.Errorf("the second holds %v, want its own source", got)
	}
}

// A workspace whose daemon could not bring what was asked for has
// failed, and says why; asked for again, it is tried again, in the same
// sandbox, and is ready where what failed no longer does. One whose
// daemon never comes to answer has failed too, and waiting for it ends.
func TestAWorkspaceThatFailedSaysWhyAndIsTriedAgainWhenAskedFor(t *testing.T) {
	s := over(t)
	s.fails = "the branch later is not at the remote"
	client, _, _ := served(t, s, t.TempDir(), nil)
	ctx := context.Background()
	spec := of("https://forge.example/acme/code.git")
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "later", Spec: spec}); err != nil {
		t.Fatal(err)
	}
	failed := waited(t, client, "later")
	if failed.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_FAILED || !strings.Contains(failed.GetStatus().GetFailure(), "not at the remote") {
		t.Fatalf("it is %v, want failed, saying why", failed.GetStatus())
	}
	daemon := s.daemon("sbx_1")
	daemon.mu.Lock()
	daemon.fails = ""
	daemon.mu.Unlock()
	s.mu.Lock()
	s.fails = ""
	s.mu.Unlock()
	again, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "later", Spec: spec})
	if err != nil || again.GetCreated() || again.GetWorkspace().GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_FAILED {
		t.Fatalf("asked for again it was answered %v (%v), want the one there is, as it was found", again, err)
	}
	ready := waited(t, client, "later")
	if told, _ := daemon.was(); ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY || s.of(ready.GetName()) != "sbx_1" || told != 2 || len(s.ended()) != 0 {
		t.Errorf("tried again it is %v, its daemon told %d times, and %v was ended", ready.GetStatus(), told, s.ended())
	}

	s.mu.Lock()
	s.silent = true
	s.mu.Unlock()
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "silent", Spec: spec}); err != nil {
		t.Fatal(err)
	}
	if never := waited(t, client, "silent"); never.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_FAILED || !strings.Contains(never.GetStatus().GetFailure(), "does not answer") {
		t.Errorf("one whose daemon never answers is %v, want failed, saying so", never.GetStatus())
	}
}

// A workspace that is paused is said to be, and waiting for it ends
// there; asked for again it is gone on with, and is ready. A workspace
// with a lifetime ends itself, with its sandbox, when that has run out.
func TestAPausedWorkspaceIsGoneOnWithAndALifetimeEnds(t *testing.T) {
	spec := of("https://forge.example/acme/code.git")
	s, client := made(t, "kept", spec)
	ctx := context.Background()
	waited(t, client, "kept")
	s.set("sbx_1", sandboxv1.Sandbox_STATE_PAUSED)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "kept"})
		if err == nil && got.GetWorkspace().GetStatus().GetState() == workspacev1.WorkspaceStatus_STATE_PAUSED {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a workspace whose sandbox paused is %v (%v)", got.GetWorkspace().GetStatus(), err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	resumed := func() []string {
		s.mu.Lock()
		defer s.mu.Unlock()
		return slices.Clone(s.resumed)
	}
	if paused := waited(t, client, "kept"); paused.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_PAUSED || len(resumed()) != 0 {
		t.Fatalf("waiting for a paused workspace ended at %v, and resumed %v", paused.GetStatus(), resumed())
	}
	if again, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "kept", Spec: spec}); err != nil || again.GetWorkspace().GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_PAUSED {
		t.Fatalf("asked for again it was answered %v (%v), want it as it was found", again.GetWorkspace().GetStatus(), err)
	}
	if ready := waited(t, client, "kept"); ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY || !slices.Equal(resumed(), []string{"sbx_1"}) {
		t.Errorf("gone on with, it is %v, and %v was resumed", ready.GetStatus(), resumed())
	}

	job := of("https://forge.example/acme/code.git")
	job.Lifetime = durationpb.New(300 * time.Millisecond)
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "a-job", Spec: job}); err != nil {
		t.Fatal(err)
	}
	if ready := waited(t, client, "a-job"); ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		t.Fatalf("a job's workspace is %v", ready.GetStatus())
	}
	gone(t, client, "a-job")
	if !slices.Contains(s.ended(), "sbx_2") || slices.Contains(s.ended(), "sbx_1") {
		t.Errorf("a lifetime that ran out ended %v, want the job's sandbox alone", s.ended())
	}
}

// A workspace is brought more by its spec, given whole: what is added
// within what its sandbox reaches is told to its daemon, an entity added
// resolved and those it had not resolved again; a repository its
// sandbox was not made to fetch is refused by name, by its address or by
// an entity's, and so is a spec that leaves out what the workspace has
// or changes what is said once.
func TestAWorkspaceIsBroughtMoreByItsSpec(t *testing.T) {
	spec := &workspacev1.WorkspaceSpec{Repositories: []*workspacev1.RepositorySource{{Remote: "https://forge.example/acme/code.git", Ref: "main"}}}
	s := over(t)
	fake := backstagetest.Serve(t, catalogOfTheTests)
	client, _, _ := served(t, s, t.TempDir(), resolving(t, fake))
	ctx := context.Background()
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "grows", Spec: spec}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "grows")
	more := proto.Clone(spec).(*workspacev1.WorkspaceSpec)
	more.Entities = []*typev1.EntityRef{{Kind: "component", Name: "payments"}}
	if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "grows", Spec: more}); err != nil {
		t.Fatalf("more within what its sandbox reaches: %v", err)
	}
	holds := toldAgain(t, s.daemon("sbx_1"), 2)
	if len(holds.GetEntities()) != 1 || holds.GetEntities()[0].GetRoot() != "payments" || len(holds.GetRepositories()) != 2 || holds.GetRepositories()[1].GetName() != "code" {
		t.Errorf("its daemon was last told to hold %v", holds)
	}
	asked := len(fake.Asked())
	twice := proto.Clone(more).(*workspacev1.WorkspaceSpec)
	if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "grows", Spec: twice}); err != nil {
		t.Fatalf("more again: %v", err)
	}
	if again := len(fake.Asked()); again != asked {
		t.Errorf("an entity it had was resolved again: the catalog was asked %d times more", again-asked)
	}
	beyond := proto.Clone(twice).(*workspacev1.WorkspaceSpec)
	beyond.Repositories = append(beyond.Repositories, &workspacev1.RepositorySource{Remote: "https://forge.example/acme/another.git"})
	elsewhere := proto.Clone(twice).(*workspacev1.WorkspaceSpec)
	elsewhere.Entities = append(elsewhere.Entities, &typev1.EntityRef{Name: "stranger"})
	for what, wanted := range map[string]*workspacev1.WorkspaceSpec{"a repository its sandbox was not made to fetch": beyond, "an entity in a repository it does not reach": elsewhere} {
		if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "grows", Spec: wanted}); !refusedAsNotServed(err, "REACH_NOT_SERVED") {
			t.Errorf("%s: %v, want it refused as not served", what, err)
		}
	}
	pushing := proto.Clone(twice).(*workspacev1.WorkspaceSpec)
	pushing.Push = true
	for what, wanted := range map[string]*workspacev1.WorkspaceSpec{"a spec that leaves out what it has": spec, "a spec that changes whether it pushes": pushing} {
		if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "grows", Spec: wanted}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want it refused as not well formed", what, err)
		}
	}
	if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "another", Spec: more}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("more for a workspace that is not there: %v", err)
	}
	if len(s.asked()) != 1 {
		t.Errorf("bringing more made the sandboxes %v", s.asked())
	}
}

// A workspace whose sandbox could not be made has failed, says why, and
// is made when it is asked for again: what failed is the control plane's
// own, and is tried again as one of its daemon's is.
func TestAWorkspaceWhoseSandboxCouldNotBeMadeIsMadeWhenAskedForAgain(t *testing.T) {
	s := over(t)
	s.refuses = "the gateway is away"
	client, _, _ := served(t, s, t.TempDir(), nil)
	ctx := context.Background()
	spec := of("https://forge.example/acme/code.git")
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "unmade", Spec: spec}); err != nil {
		t.Fatal(err)
	}
	if failed := waited(t, client, "unmade"); failed.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_FAILED || !strings.Contains(failed.GetStatus().GetFailure(), "the gateway is away") {
		t.Fatalf("it is %v, want failed, saying why", failed.GetStatus())
	}
	s.mu.Lock()
	s.refuses = ""
	s.mu.Unlock()
	// The control: left alone, it stays as it was.
	time.Sleep(100 * time.Millisecond)
	if got, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "unmade"}); err != nil || got.GetWorkspace().GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_FAILED || len(s.asked()) != 0 {
		t.Fatalf("left alone it is %v with %v made (%v), want it failed and nothing made", got.GetWorkspace().GetStatus(), s.asked(), err)
	}
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "unmade", Spec: spec}); err != nil {
		t.Fatal(err)
	}
	if ready := waited(t, client, "unmade"); ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY || len(s.asked()) != 1 {
		t.Errorf("asked for again it is %v, in %v", ready.GetStatus(), s.asked())
	}
}

// What a version control system says of a fetch is held before it
// becomes a sandbox's rule: requests beneath the repository alone,
// programs each by its whole path, and variables that begin as the
// system says its own do. Each that is not is refused, beside one that
// is.
func TestWhatASystemSaysOfAFetchIsHeld(t *testing.T) {
	const repository = "/acme/code"
	sound := []vcs.Request{{Method: "GET", Path: repository + "/info/refs", Query: map[string]string{"service": "an-upload"}}, {Method: "POST", Path: repository + "/an-upload"}}
	if err := asking("a fetch", repository, sound); err != nil {
		t.Errorf("what a fetch asks was refused: %v", err)
	}
	for name, requests := range map[string][]vcs.Request{
		"nothing":                   nil,
		"the repository itself":     {{Method: "GET", Path: repository}},
		"another repository":        {{Method: "GET", Path: "/acme/other/info/refs"}},
		"a repository of like name": {{Method: "GET", Path: repository + "-private/info/refs"}},
		"everything":                {{Method: "GET", Path: "/**"}},
		"a pattern beneath":         {{Method: "GET", Path: repository + "/*"}},
		"a path that climbs":        {{Method: "GET", Path: repository + "/../other"}},
		"a path that climbs unseen": {{Method: "GET", Path: repository + "/%2e%2e/other"}},
		"a path of two lines":       {{Method: "GET", Path: repository + "/info\n/refs"}},
		"a path with a part of it":  {{Method: "GET", Path: repository + "/info#refs"}},
		"a path with a space":       {{Method: "GET", Path: repository + "/info refs"}},
		"nothing beneath":           {{Method: "GET", Path: repository + "/"}},
		"another method":            {{Method: "DELETE", Path: repository + "/info/refs"}},
		"any method":                {{Method: "*", Path: repository + "/info/refs"}},
		"a parameter of any value":  {{Method: "GET", Path: repository + "/info/refs", Query: map[string]string{"service": "*"}}},
		"a parameter with no value": {{Method: "GET", Path: repository + "/info/refs", Query: map[string]string{"service": ""}}},
		"a parameter and another":   {{Method: "GET", Path: repository + "/info/refs", Query: map[string]string{"service": "a&b=c"}}},
	} {
		if err := asking("a fetch", repository, requests); err == nil {
			t.Errorf("%s was taken as what a fetch asks", name)
		}
	}

	if err := fetchedBy([]string{"/usr/bin/a-program"}); err != nil {
		t.Errorf("a program by its path was refused: %v", err)
	}
	for name, programs := range map[string][]string{
		"none":                nil,
		"any program":         {"/**"},
		"a pattern":           {"/usr/bin/*"},
		"a name and no path":  {"a-program"},
		"a path not in order": {"/usr/bin/../bin/a-program"},
		"the root":            {"/"},
		"no path":             {""},
		"a path with a space": {"/usr/bin/a program"},
		"a path of two lines": {"/usr/bin/a\nprogram"},
	} {
		if err := fetchedBy(programs); err == nil {
			t.Errorf("%s was taken as the programs that fetch", name)
		}
	}

	if err := telling(map[string]string{"A_SYSTEM_SETTING_0": "a value", "A_SYSTEM_COUNT": "1"}, "A_SYSTEM_"); err != nil {
		t.Errorf("variables of the system's own were refused: %v", err)
	}
	for name, told := range map[string]map[string]string{
		"where programs are found":   {"PATH": "/tmp"},
		"a library to load":          {"LD_PRELOAD": "/tmp/x.so"},
		"a variable of frontseat's":  {"FRONTSEAT_SERVER": "http://elsewhere:1"},
		"a proxy":                    {"HTTPS_PROXY": "http://elsewhere:1"},
		"whom to trust":              {"SSL_CERT_FILE": "/tmp/x.pem"},
		"what a shell runs first":    {"BASH_ENV": "/tmp/x"},
		"home":                       {"HOME": "/tmp"},
		"its own, of two lines":      {"A_SYSTEM_SETTING": "one\ntwo"},
		"its own, written otherwise": {"A_SYSTEM_setting": "x"},
		"its beginning and no more":  {"A_SYSTEM_": "x"},
		"no name":                    {"": "x"},
	} {
		if err := telling(told, "A_SYSTEM_"); err == nil {
			t.Errorf("%s was taken as what a system tells its program", name)
		}
	}
	for _, own := range []string{"", "_", "A", "a_system_", "A SYSTEM_", "FRONTSEAT_", "FRONTSEAT_FETCH_", "OPENSHELL_X_", "LD_", "L", "MISE_", "XDG_"} {
		if err := telling(map[string]string{own + "X": "x"}, own); err == nil {
			t.Errorf("%q was taken as what a system's own variables begin with", own)
		}
	}
}

// answering is a version control system that answers as it is made to.
type answering struct {
	system
	requests []vcs.Request
	programs []string
}

func (a answering) Requests(repository string) []vcs.Request {
	if a.requests != nil {
		return a.requests
	}
	return a.system.Requests(repository)
}

func (a answering) Programs() []string {
	if a.programs != nil {
		return a.programs
	}
	return a.system.Programs()
}

// A workspace is not made on what a system says where it asks for more
// than its repository, or names a pattern of programs: it is refused, and
// no sandbox is asked for.
func TestASystemThatSaysTooMuchMakesNoWorkspace(t *testing.T) {
	for name, said := range map[string]vcs.Fetching{
		"requests of everything": answering{requests: []vcs.Request{{Method: "GET", Path: "/**"}}},
		"a pattern of programs":  answering{programs: []string{"/**"}},
	} {
		s := over(t)
		client, _, _ := served(t, s, t.TempDir(), func(c *Config) { c.Fetching = said })
		_, err := client.CreateWorkspace(context.Background(), &workspacev1.CreateWorkspaceRequest{Name: "a-workspace", Spec: of("https://forge.example/acme/code")})
		if err == nil || !strings.Contains(err.Error(), "the version control system") || len(s.asked()) != 0 {
			t.Errorf("%s: %v, and %v made; want it refused as the system's mistake, and nothing made", name, err, s.asked())
		}
	}
}

// A workspace asked for again with less than it has, or with what it has
// in another order, is the one there is; with a lifetime it was not made
// with, it is refused; and a lifetime that is no while to come is no
// lifetime.
func TestAWorkspaceAskedForWithLessIsTheOneThereIs(t *testing.T) {
	two := of("https://forge.example/acme/a.git", "https://forge.example/acme/b.git")
	s, client := made(t, "two", two)
	ctx := context.Background()
	waited(t, client, "two")
	for what, asked := range map[string]*workspacev1.WorkspaceSpec{
		"less":           of("https://forge.example/acme/a.git"),
		"another order":  of("https://forge.example/acme/b.git", "https://forge.example/acme/a.git"),
		"the same again": two,
	} {
		again, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "two", Spec: asked})
		if err != nil || again.GetCreated() {
			t.Errorf("asked for with %s: %v, %v; want the one there is", what, again.GetCreated(), err)
		}
	}
	// The control: more is refused.
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "two", Spec: of("https://forge.example/acme/a.git", "https://forge.example/acme/c.git")}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("asked for with more: %v, want it refused", err)
	}
	job := of("https://forge.example/acme/a.git")
	job.Lifetime = durationpb.New(time.Hour)
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "two", Spec: job}); connect.CodeOf(err) != connect.CodeAlreadyExists || !strings.Contains(err.Error(), "lasts otherwise") {
		t.Errorf("a kept workspace asked for as a job's: %v, want it refused by name", err)
	}
	for _, life := range []time.Duration{0, -time.Hour} {
		job.Lifetime = durationpb.New(life)
		if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "a-job", Spec: job}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("a lifetime of %v: %v, want it refused", life, err)
		}
	}
	if len(s.asked()) != 1 {
		t.Errorf("asking again made %v", s.asked())
	}
}

// What a workspace's daemon would refuse to bring is refused when it is
// asked for, and is not kept: the workspace is as it was, and is asked for
// again as it was. One that is not ready is brought more once it is.
func TestWhatADaemonRefusesToBringIsNotKept(t *testing.T) {
	spec := &workspacev1.WorkspaceSpec{Repositories: []*workspacev1.RepositorySource{{Remote: "https://forge.example/acme/code.git", Ref: "main"}}}
	s := over(t)
	client, _, _ := served(t, s, t.TempDir(), resolving(t, backstagetest.Serve(t, catalogOfTheTests)))
	ctx := context.Background()
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "kept", Spec: spec}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "kept")
	daemon := s.daemon("sbx_1")
	daemon.mu.Lock()
	daemon.refuses = refusal.New(refusal.Source, connect.CodeInvalidArgument, "SOURCE_UNFIT", "the repository payments cannot be held")
	daemon.mu.Unlock()
	refused := proto.Clone(spec).(*workspacev1.WorkspaceSpec)
	refused.Entities = []*typev1.EntityRef{{Kind: "component", Namespace: "default", Name: "payments"}}
	if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "kept", Spec: refused}); connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "payments") {
		t.Fatalf("what the daemon refuses: %v, want its refusal", err)
	} else if reason, domain := frontseat.Reason(err); reason != "SOURCE_UNFIT" || domain != refusal.Workspace {
		t.Errorf("what the daemon refused by name is said with the reason %q of %q, want SOURCE_UNFIT of the workspace API", reason, domain)
	}
	got, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "kept"})
	if err != nil || len(got.GetWorkspace().GetSpec().GetEntities()) != 0 || len(got.GetWorkspace().GetStatus().GetEntities()) != 0 {
		t.Errorf("what was refused was kept: %v (%v)", got.GetWorkspace(), err)
	}
	if again, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "kept", Spec: spec}); err != nil || again.GetCreated() {
		t.Errorf("asked for again as it was: %v, %v", again.GetCreated(), err)
	}
	if ready := waited(t, client, "kept"); ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		t.Errorf("after a refusal it is %v", ready.GetStatus())
	}
}

// A workspace whose sandbox never comes to run has failed, and waiting
// for it ends.
func TestAWorkspaceWhoseSandboxNeverRunsHasFailed(t *testing.T) {
	s := over(t)
	s.starts = sandboxv1.Sandbox_STATE_STARTING
	client, _, _ := served(t, s, t.TempDir(), func(c *Config) { c.Starts = 300 * time.Millisecond })
	if _, err := client.CreateWorkspace(context.Background(), &workspacev1.CreateWorkspaceRequest{Name: "stuck", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	if never := waited(t, client, "stuck"); never.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_FAILED || !strings.Contains(never.GetStatus().GetFailure(), "did not come to run") {
		t.Errorf("one whose sandbox never runs is %v, want failed, saying so", never.GetStatus())
	}
}

// What the sandbox service says of a kept credential is held before it
// reaches git's configuration: a variable is one name and a username one
// word. A remote that is written otherwise than plainly, or that the
// system clones nothing from, is refused; a host is one however it is
// spelled.
func TestWhatReachesAProgramsConfigurationIsHeld(t *testing.T) {
	s := over(t)
	s.keeps("local", "forge.example:443")
	s.variable = `X"; curl https://elsewhere.example; echo "`
	client, _, _ := served(t, s, t.TempDir(), nil)
	ctx := context.Background()
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "planted", Spec: of("https://forge.example/acme/code.git")}); err == nil || !strings.Contains(err.Error(), "no username and variable") {
		t.Errorf("a variable that is a command: %v, want it refused", err)
	}
	s.variable = ""
	for what, remote := range map[string]string{
		"an escape that climbs":     "https://forge.example/acme/%2e%2e/secret/x.git",
		"an escape of another sort": "https://forge.example/acme/a%20b.git",
		"a path of two slashes":     "https://forge.example/acme//x.git",
		"another transport":         "svn://anything.example/x",
		"a host and no repository":  "https://whole.example/",
	} {
		if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "odd", Spec: of(remote)}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want it refused as no repository", what, err)
		}
	}
	made, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "spelled", Spec: of("https://forge.example/acme/a.git", "https://FORGE.example/acme/b.git")})
	if err != nil {
		t.Fatal(err)
	}
	_ = made
	ready := waited(t, client, "spelled")
	rules := s.sandbox(s.of(ready.GetName())).GetEgress().GetRules()
	var hosts []string
	for _, rule := range rules {
		hosts = append(hosts, rule.GetHost())
	}
	if want := []string{"forge.example:443", "grid.example:8980", "assets.example:8983"}; !slices.Equal(hosts, want) {
		t.Errorf("it reaches %v, want %v", hosts, want)
	}
}

// The control plane is the one writer of what a workspace holds: a
// daemon started again holds no generation and is told again, and one
// told otherwise from inside its sandbox, which holds another
// generation, is told the control plane's own; what the control plane
// keeps of the workspace is as it was asked for. The control is a
// daemon left alone, which is told once however many passes go by.
func TestADaemonStartedAgainOrToldFromInsideIsToldWhatTheControlPlaneKeeps(t *testing.T) {
	spec := of("https://forge.example/acme/code.git")
	s, client := made(t, "kept", spec)
	ctx := context.Background()
	waited(t, client, "kept")
	daemon := s.daemon("sbx_1")
	toldTimes := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			n, holds := daemon.was()
			daemon.mu.Lock()
			generation := daemon.generation
			daemon.mu.Unlock()
			if n == want && generation == 1 && proto.Equal(source(holds), source(spec)) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("its daemon was told %d times, at the generation %d, to hold %v; want %d times, at 1, what was asked for", n, generation, holds, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	// Left alone, it is told once, over many passes.
	time.Sleep(300 * time.Millisecond)
	toldTimes(1)
	daemon.restarted()
	toldTimes(2)
	// Told otherwise from inside its sandbox: another generation, and
	// more than was asked for.
	daemon.mu.Lock()
	daemon.generation = 99
	daemon.spec = told.Spec(of("https://forge.example/acme/code.git", "https://forge.example/acme/more.git"), 99, told.Resolved{}, system{}.NameOf)
	daemon.mu.Unlock()
	toldTimes(3)
	kept, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "kept"})
	if err != nil || !proto.Equal(source(kept.GetWorkspace().GetSpec()), source(spec)) {
		t.Errorf("what is kept of it is %v (%v), want what was asked for", kept.GetWorkspace().GetSpec(), err)
	}
}

// A daemon that is bringing what it was told before refuses to be told
// more by a reason, SOURCE_POPULATING, and the workspace is populating
// while it does: the control plane acts on the reason, never on the
// words. The control is a daemon whose refusal says "populating" in its
// words under another reason, which leaves the workspace failed.
func TestADaemonThatIsBringingIsKnownByItsReasonAndNotByItsWords(t *testing.T) {
	for what, c := range map[string]struct {
		refusal error
		want    workspacev1.WorkspaceStatus_State
	}{
		"its reason":       {refusal.New(refusal.Source, connect.CodeFailedPrecondition, "SOURCE_POPULATING", "busy"), workspacev1.WorkspaceStatus_STATE_POPULATING},
		"its words":        {refusal.New(refusal.Source, connect.CodeFailedPrecondition, "SOURCE_MOUNTED", "the workspace is being populated already"), workspacev1.WorkspaceStatus_STATE_FAILED},
		"no reason at all": {connect.NewError(connect.CodeFailedPrecondition, "the workspace is being populated already"), workspacev1.WorkspaceStatus_STATE_FAILED},
	} {
		t.Run(what, func(t *testing.T) {
			s := over(t)
			s.refusing = c.refusal
			client, _, _ := served(t, s, t.TempDir(), nil)
			if _, err := client.CreateWorkspace(context.Background(), &workspacev1.CreateWorkspaceRequest{Name: "busy", Spec: of("https://forge.example/acme/code.git")}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				got, err := client.DescribeWorkspace(context.Background(), &workspacev1.DescribeWorkspaceRequest{Name: "busy"})
				if err == nil && got.GetWorkspace().GetStatus().GetState() == c.want {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("a daemon refusing by %s leaves the workspace %v (%v), want %v", what, got.GetWorkspace().GetStatus(), err, c.want)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

// A daemon that is bringing what it was told, for longer than a daemon
// is waited for to answer, is populating, and has not failed: it
// answers, saying so. The control is a daemon that says nothing, which
// has failed (TestAWorkspaceThatFailedSaysWhyAndIsTriedAgainWhenAskedFor).
// What it was told to add is said to be brought from the answer.
func TestADaemonThatIsLongBringingIsPopulatingAndNotFailed(t *testing.T) {
	spec := of("https://forge.example/acme/code.git")
	s, client := made(t, "slow", spec)
	ctx := context.Background()
	waited(t, client, "slow")
	daemon := s.daemon("sbx_1")
	daemon.mu.Lock()
	daemon.bringing = true
	daemon.mu.Unlock()
	more := proto.Clone(spec).(*workspacev1.WorkspaceSpec)
	more.WholeRepositories = true
	updated, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "slow", Spec: more})
	if err != nil || updated.GetWorkspace().GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_POPULATING {
		t.Fatalf("what was added is said to be %v (%v), want it populating", updated.GetWorkspace().GetStatus().GetState(), err)
	}
	// Longer than a daemon is waited for to answer (Answers, 300 ms here).
	time.Sleep(time.Second)
	got, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "slow"})
	if err != nil || got.GetWorkspace().GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_POPULATING {
		t.Errorf("a daemon long bringing leaves the workspace %v (%v), want populating", got.GetWorkspace().GetStatus(), err)
	}
}

// refusedAsNotServed is whether an error refuses what is not served, as every
// server refuses it: UNIMPLEMENTED, with the reason that names it, in the
// workspace API's domain.
func refusedAsNotServed(err error, reason string) bool {
	said, domain := frontseat.Reason(err)
	return connect.CodeOf(err) == connect.CodeUnimplemented && said == reason && domain == refusal.Workspace
}

// A refusal of a workspace says what is wrong and repeats nothing of
// what it refused: not a name, not a repository's path, not an address,
// each of which may hold a token. The control of each is the same
// request with what it refused mended, which is taken.
func TestARefusalOfAWorkspaceRepeatsNothingItRefused(t *testing.T) {
	const refusedValue = "v4lue-of-adas"
	_, client := made(t, "x", of("https://forge.example/acme/code.git"))
	waited(t, client, "x")
	ctx := context.Background()
	refused := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s was taken", what)
		} else if strings.Contains(err.Error(), refusedValue) {
			t.Errorf("the refusal of %s repeats what it refused: %v", what, err)
		}
	}
	_, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "A-" + refusedValue, Spec: of("https://forge.example/acme/code.git")})
	refused("a name that is none", err)
	_, err = client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "A-" + refusedValue})
	refused("a name there is no workspace of", err)
	_, err = client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "path", Spec: of("https://forge.example/acme/a%20" + refusedValue + ".git")})
	refused("a repository's path written otherwise than plainly", err)
	more := of("https://forge.example/acme/code.git")
	more.Repositories = append(more.Repositories, &workspacev1.RepositorySource{Remote: "https://forge.example/" + refusedValue + "/other.git"})
	_, err = client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "x", Spec: more})
	refused("a repository its sandbox does not reach", err)
	// The controls.
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "a-" + refusedValue, Spec: of("https://forge.example/acme/a-" + refusedValue + ".git")}); err != nil {
		t.Errorf("the control: a workspace of a name and a path written plainly: %v", err)
	}
	if _, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "a-" + refusedValue}); err != nil {
		t.Errorf("the control: a workspace there is, asked after: %v", err)
	}
	if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "x", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Errorf("the control: a repository its sandbox reaches: %v", err)
	}
}

// authoring is a version control system that tells its program what it
// is made to of who commits.
type authoring struct {
	system
	told map[string]string
}

func (a authoring) Authoring(string, string) map[string]string { return a.told }

// What a version control system tells its program of who commits is
// held to what is its own to say, as what it presents is: no variable
// of this server's, the gateway's, the loader's or a box's tools', and
// not where programs are found. The control is a variable of its own.
func TestWhoCommitsIsToldByNoVariableOfAnothers(t *testing.T) {
	ask := func(said map[string]string) error {
		s := &service{Config: Config{Box: "registry.example/acme/box:1", Fetching: authoring{told: said}}}
		_, err := s.sandbox(context.Background(), "x", "ada", &workspacev1.WorkspaceSpec{}, told.Resolved{})
		return err
	}
	if err := ask(map[string]string{"A_SYSTEM_AUTHOR": "Ada <ada@example.org>"}); err != nil {
		t.Errorf("the control: a variable of the system's own was refused: %v", err)
	}
	for _, name := range []string{"FRONTSEAT_SERVER", "LD_PRELOAD", "OPENSHELL_ENDPOINT", "MISE_CONFIG_FILE", "XDG_CONFIG_HOME", "DYLD_INSERT_LIBRARIES", "NONO_PROFILE", "PATH"} {
		if err := ask(map[string]string{name: "x"}); err == nil {
			t.Errorf("who commits was told as %s", name)
		}
	}
}

// A repository's address is read once, as the daemon in the sandbox
// reads it, when the workspace is asked for: what the daemon would
// refuse, or would fetch from where no rule leads, is refused then, and
// nothing of it is kept, said back or made. An address with a query,
// which may hold a token; a host written as an address, in any
// spelling; a directory of this machine; and a path written otherwise
// than plainly are each none. One that names anyone before its host
// but git, however it is written, is refused as credentialed, since
// what is written there may be a token. The control is the same
// workspace with its address written as one is, and written for ssh,
// naming git or no one, which the system clones over https and a rule
// is written for.
func TestARepositorysAddressIsReadOnceAsTheDaemonReadsIt(t *testing.T) {
	const refusedValue = "v4lue-of-adas"
	s := over(t)
	client, _, _ := served(t, s, t.TempDir(), nil)
	ctx := context.Background()
	for what, remote := range map[string]string{
		"a query":                    "https://forge.example/acme/code.git?access_token=" + refusedValue,
		"a fragment":                 "https://forge.example/acme/code.git#" + refusedValue,
		"an address in tens":         "http://2130706433/" + refusedValue,
		"an address":                 "https://192.0.2.7/" + refusedValue + ".git",
		"an address in sixteens":     "https://0x7f.1/" + refusedValue + ".git",
		"a directory":                "file:///remotes/" + refusedValue,
		"a path of this machine":     "/remotes/" + refusedValue,
		"a path that climbs":         "https://forge.example/acme/../" + refusedValue,
		"a path of two spellings":    "https://forge.example/acme/%2e%2e/" + refusedValue,
		"a port that is none":        "https://forge.example:0/" + refusedValue,
		"another transport, unread":  "svn://forge.example/acme/" + refusedValue,
		"a user and a password":      "https://someone:" + refusedValue + "@forge.example/acme/code.git",
		"no repository of the host":  "https://forge.example/",
		"a host and nothing of path": "https://forge.example",
	} {
		_, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "refused", Spec: of(remote)})
		if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeInvalidArgument || reason != "SPEC_INVALID" {
			t.Errorf("%s: %v, want it refused SPEC_INVALID", what, err)
		} else if strings.Contains(err.Error(), refusedValue) {
			t.Errorf("the refusal of %s repeats what it refused: %v", what, err)
		}
	}
	for what, remote := range map[string]string{
		"a user over https":                 "https://" + refusedValue + "@forge.example/acme/code.git",
		"a user before a host and a path":   "ghp_" + refusedValue + "@forge.example:acme/code.git",
		"a user over ssh":                   "ssh://" + refusedValue + "@forge.example/acme/code.git",
		"a user over ssh, written for git+": "git+ssh://" + refusedValue + "@forge.example/acme/code.git",
	} {
		_, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "refused", Spec: of(remote)})
		if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeInvalidArgument || reason != "REMOTE_CREDENTIALED" {
			t.Errorf("%s: %v, want it refused REMOTE_CREDENTIALED", what, err)
		} else if strings.Contains(err.Error(), refusedValue) {
			t.Errorf("the refusal of %s repeats what it refused: %v", what, err)
		}
	}
	if listed, err := client.ListWorkspaces(ctx, &workspacev1.ListWorkspacesRequest{}); err != nil || len(listed.GetWorkspaces()) != 0 || len(s.asked()) != 0 {
		t.Errorf("what was refused is listed as %v (%v) and made the sandboxes %v", listed.GetWorkspaces(), err, s.asked())
	}
	// The controls.
	for name, remote := range map[string]string{
		"over-ssh":           "ssh://git@forge.example/acme/code.git",
		"over-ssh-no-one":    "ssh://forge.example:2222/acme/code",
		"before-a-path":      "git@forge.example:acme/code.git",
		"before-a-path-bare": "forge.example:acme/code.git",
	} {
		if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: name, Spec: of(remote)}); err != nil {
			t.Errorf("the control: %s, written for ssh: %v", remote, err)
		}
	}
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "plain", Spec: of("https://forge.example/acme/" + refusedValue + ".git")}); err != nil {
		t.Fatalf("the control: an address written as one is: %v", err)
	}
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "converted", Spec: of("git@elsewhere.example:acme/code.git")}); err != nil {
		t.Fatalf("the control: a remote the system clones over https: %v", err)
	}
	waited(t, client, "converted")
	rules := s.sandbox(s.of("converted")).GetEgress().GetRules()
	if len(rules) == 0 || rules[0].GetHost() != "elsewhere.example:443" || len(rules[0].GetRequests()) == 0 || rules[0].GetRequests()[0].GetPath() != "/acme/code.git/refs" {
		t.Errorf("a remote cloned over https is reached by %v, want a rule for the host it is cloned from", rules)
	}
	// An address is held when a workspace is brought more, as when it
	// is made.
	waited(t, client, "plain")
	more := of("https://forge.example/acme/"+refusedValue+".git", "https://forge.example/acme/more.git?token="+refusedValue)
	if _, err := client.UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "plain", Spec: more}); connect.CodeOf(err) != connect.CodeInvalidArgument || strings.Contains(err.Error(), refusedValue+"?") || strings.Contains(err.Error(), "token") {
		t.Errorf("a workspace brought more by an address with a query: %v, want it refused, and not repeated", err)
	}
}
