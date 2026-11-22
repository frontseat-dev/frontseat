package controlplane

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/internal/controlplane/identity"
	sandboxv1 "github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1"
)

// owner is the token the tests' control planes know their owner by, and
// another one none of them knows.
const (
	owner   = "6f776e65722d6f662d7468652d746573742d636f6e74726f6c2d706c616e6573"
	another = "616e6f746865722d746f6b656e2d6e6f2d706c616e652d6b6e6f77732d697421"
)

// knowing knows the tests' owner by their token.
func knowing(t *testing.T) identity.Authenticator {
	t.Helper()
	file := filepath.Join(t.TempDir(), "owner", "token")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(owner+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	known, err := OwnerToken(file)
	if err != nil {
		t.Fatal(err)
	}
	return known
}

// bearer is a transport that sends a token with every request.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// bearing is an HTTP client that sends a token with every request; one
// that sends none where the token is empty.
func bearing(token string) *http.Client {
	if token == "" {
		return &http.Client{}
	}
	return &http.Client{Transport: bearer{token}}
}

// asked sends a request to a control plane with a token, or with none
// where it is empty, and returns how it was answered.
func asked(t *testing.T, method, at, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, at, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	got, err := bearing(token).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	said, _ := io.ReadAll(got.Body)
	return got.StatusCode, string(said)
}

// reason is the reason and domain an error carries, as ErrorInfo.
func reason(t *testing.T, err error) (string, string) {
	t.Helper()
	var said *connect.Error
	if !errors.As(err, &said) {
		return "", ""
	}
	for _, detail := range said.Details() {
		message, err := connectproto.UnmarshalErrorDetail(detail)
		if err != nil {
			t.Fatal(err)
		}
		if info, ok := message.(*errdetails.ErrorInfo); ok {
			return info.GetReason(), info.GetDomain()
		}
	}
	return "", ""
}

// logged gathers what the process logs while a test runs, through the
// standard logger and the structured one.
func logged(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var kept bytes.Buffer
	into := writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return kept.Write(p)
	})
	before, beforeSlog := log.Writer(), slog.Default()
	log.SetOutput(into)
	slog.SetDefault(slog.New(slog.NewTextHandler(into, nil)))
	t.Cleanup(func() { log.SetOutput(before); slog.SetDefault(beforeSlog) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return kept.String()
	}
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

// No call of the sandbox API is served on the control plane's endpoint,
// nor a way to a sandbox's port beneath a sandbox's id: whoever reaches
// the endpoint, its owner among them, runs nothing in a sandbox, ends
// none and lists none. The sandbox service is the control plane's own,
// in its process, and the control is the control plane's use of it: a
// workspace is made in a sandbox, and ended with it.
func TestNoSandboxCallIsServedOnTheEndpoint(t *testing.T) {
	client, held, address, sandboxes := overRealServed(t)
	ctx := context.Background()
	id := madeX(t, client, sandboxes)
	if !held.Holds(id) {
		t.Fatalf("the driver does not hold the workspace's sandbox %s", id)
	}
	// Without a token nothing is asked at all; with the owner's, there
	// is no such call and no such way.
	for token, want := range map[string]int{"": http.StatusUnauthorized, owner: http.StatusNotFound} {
		for _, call := range []string{"ListSandboxes", "DescribeSandbox", "CreateSandbox", "ExecSandbox", "DeleteSandbox", "PauseSandbox", "SetCredential"} {
			status, said := asked(t, http.MethodPost, address+"/frontseat.sandbox.v1alpha1.SandboxService/"+call, token, `{"sandboxId":"`+id+`","arguments":["true"]}`)
			if status != want {
				t.Errorf("SandboxService/%s, with the owner's token %v: %d %s, want %d", call, token != "", status, said, want)
			}
		}
		for _, path := range []string{"/sandboxes/" + id + "/ports/7600/", "/sandboxes/" + id + "/tunnels/2222"} {
			if status, said := asked(t, http.MethodGet, address+path, token, ""); status != want {
				t.Errorf("%s, with the owner's token %v: %d %s, want %d", path, token != "", status, said, want)
			}
		}
	}
	// Nothing asked of the endpoint ended the sandbox.
	if _, err := sandboxes.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: id}); err != nil || !held.Holds(id) {
		t.Fatalf("the workspace's sandbox %s after the calls: %v", id, err)
	}
	if _, err := client.DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	gone(t, client, "x")
	if held.Holds(id) {
		t.Errorf("the workspace is gone and its sandbox %s is still held", id)
	}
}

// A call to the control plane carries its owner's token: one without a
// token is refused as unauthenticated, saying the identity is missing;
// one with another token, or with a token written otherwise than as a
// bearer's, saying it is not one known. The same call with the owner's
// token is answered. What is refused never says the token it was given,
// and neither does anything the control plane logs, the owner's own
// among them. The ways to a workspace's daemon and shell are held alike.
func TestACallWithoutTheOwnersTokenIsRefused(t *testing.T) {
	said := logged(t)
	s := over(t)
	client, address, _ := served(t, s, t.TempDir(), nil)
	ctx := context.Background()
	if _, err := client.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "code", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	waited(t, client, "code")
	of := func(token string) workspacev1alpha1connect.WorkspaceServiceClient {
		return workspacev1alpha1connect.NewWorkspaceServiceClient(connect.NewClient(connecthttp.NewTransport(bearing(token), address)))
	}
	refusals := ""
	for _, refused := range []struct{ what, token, reason string }{
		{"no token", "", "IDENTITY_MISSING"},
		{"another token", another, "IDENTITY_INVALID"},
		{"the owner's token and more", owner + "0", "IDENTITY_INVALID"},
		{"part of the owner's token", owner[:32], "IDENTITY_INVALID"},
	} {
		_, err := of(refused.token).ListWorkspaces(ctx, &workspacev1.ListWorkspacesRequest{})
		why, domain := reason(t, err)
		if connect.CodeOf(err) != connect.CodeUnauthenticated || why != refused.reason || domain != "workspace.frontseat.dev" {
			t.Errorf("ListWorkspaces with %s: %v (%s, %s), want unauthenticated, %s, workspace.frontseat.dev", refused.what, err, why, domain, refused.reason)
		}
		if err != nil {
			refusals += err.Error()
		}
		_, err = of(refused.token).DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: "code"})
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("DeleteWorkspace with %s: %v, want unauthenticated", refused.what, err)
		}
		for _, path := range []string{"/workspaces/code/shell", "/workspaces/code/build/frontseat.build.v1alpha1.BuildService/ListBuilds"} {
			status, body := asked(t, http.MethodPost, address+path, refused.token, "{}")
			if status != http.StatusUnauthorized {
				t.Errorf("%s with %s: %d %s, want 401", path, refused.what, status, body)
			}
			refusals += body
		}
	}
	// A token written as something other than a bearer's.
	req, _ := http.NewRequest(http.MethodPost, address+"/frontseat.workspace.v1alpha1.WorkspaceService/ListWorkspaces", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+owner)
	if got, err := http.DefaultClient.Do(req); err != nil || got.StatusCode != http.StatusUnauthorized {
		t.Errorf("the owner's token written as Basic: %v, %v, want 401", got, err)
	} else {
		body, _ := io.ReadAll(got.Body)
		got.Body.Close()
		refusals += string(body)
	}
	if s.mu.Lock(); len(s.led) != 0 {
		t.Errorf("a call without the owner's token was led on: %v", s.led)
	}
	s.mu.Unlock()
	// The control: the same calls with the owner's token.
	listed, err := of(owner).ListWorkspaces(ctx, &workspacev1.ListWorkspacesRequest{})
	if err != nil || len(listed.GetWorkspaces()) != 1 {
		t.Errorf("ListWorkspaces with the owner's token: %v, %v", listed, err)
	}
	if status, body := asked(t, http.MethodPost, address+"/workspaces/code/build/frontseat.build.v1alpha1.BuildService/ListBuilds", owner, "{}"); status != http.StatusOK {
		t.Errorf("the build route with the owner's token: %d %s", status, body)
	}
	for _, token := range []string{owner, another, owner[:32]} {
		if strings.Contains(refusals, token) || strings.Contains(said(), token) {
			t.Errorf("a token was said back or logged:\n%s\n%s", refusals, said())
		}
	}
}

// The way to a workspace's daemon leads to the build API alone: a call
// of the workspace API, of the daemon's source API, which the control
// plane alone asks, or of anything else the daemon or the sandbox
// serves, is not led on, however it is spelled, and the way it had
// before, beneath /daemon, is no more. The control is a call of each of
// the build services, which is led on, without the owner's token: what
// the caller is known by stays outside the sandbox.
func TestTheBuildRouteLeadsToTheBuildServicesAlone(t *testing.T) {
	s, client := made(t, "code", of("https://forge.example/acme/code.git"))
	ready := waited(t, client, "code")
	address := strings.TrimSuffix(ready.GetStatus().GetBuildUri(), "/workspaces/code/build")
	if !strings.HasSuffix(ready.GetStatus().GetBuildUri(), "/workspaces/code/build") {
		t.Fatalf("its daemon is said to be at %q, want beneath /workspaces/code/build", ready.GetStatus().GetBuildUri())
	}
	for _, path := range []string{
		"/workspaces/code/build/frontseat.workspace.v1alpha1.WorkspaceService/UpdateWorkspace",
		"/workspaces/code/build/frontseat.workspace.v1alpha1.WorkspaceService/DescribeWorkspace",
		"/workspaces/code/build/frontseat.sandbox.v1alpha1.SandboxService/ExecSandbox",
		"/workspaces/code/build/frontseat.source.v1alpha1.SourceService/SetSource",
		"/workspaces/code/build/frontseat.source.v1alpha1.SourceService/WaitSource",
		"/frontseat.source.v1alpha1.SourceService/SetSource",
		"/workspaces/code/source/frontseat.source.v1alpha1.SourceService/SetSource",
		"/workspaces/code/build/frontseat.build.v1alpha1.BuildServiceX/ListBuilds",
		"/workspaces/code/build/frontseat.build.v1alpha1.BuildService/ListBuilds/more",
		"/workspaces/code/build/frontseat.build.v1alpha1.BuildService/",
		"/workspaces/code/build/frontseat.build.v1alpha1.BuildService%2F..%2F..%2Ffrontseat.workspace.v1alpha1.WorkspaceService%2FUpdateWorkspace",
		"/workspaces/code/build/frontseat.build.v1alpha1.BuildService/..%2Ffrontseat.workspace.v1alpha1.WorkspaceService%2FUpdateWorkspace",
		"/workspaces/code/build/frontseat%2Ebuild%2Ev1alpha1%2EBuildService/ListBuilds",
		"/workspaces/code/build/",
		"/workspaces/code/daemon/frontseat.workspace.v1alpha1.WorkspaceService/UpdateWorkspace",
		"/workspaces/code/daemon/frontseat.build.v1alpha1.BuildService/ListBuilds",
	} {
		status, said := asked(t, http.MethodPost, address+path, owner, "{}")
		if status == http.StatusOK || status/100 == 3 {
			t.Errorf("%s: %d %s, want it not led on", path, status, said)
		}
	}
	s.mu.Lock()
	refused := slices.Clone(s.led)
	s.mu.Unlock()
	if len(refused) != 0 {
		t.Fatalf("led on: %v", refused)
	}
	var want []string
	for _, call := range []string{"BuildService/ListBuilds", "GraphService/ListEntities", "ReleaseService/PlanRelease", "RunService/ListRuns"} {
		if status, said := asked(t, http.MethodPost, address+"/workspaces/code/build/frontseat.build.v1alpha1."+call, owner, "{}"); status != http.StatusOK {
			t.Errorf("%s: %d %s, want it led on", call, status, said)
		}
		want = append(want, "sbx_1:7600/frontseat.build.v1alpha1."+call)
	}
	s.mu.Lock()
	led, carried := slices.Clone(s.led), slices.Clone(s.carried)
	s.mu.Unlock()
	if !slices.Equal(led, want) {
		t.Errorf("led on %v, want %v", led, want)
	}
	if len(carried) != 0 {
		t.Errorf("what the caller is known by was led into the sandbox: %d times", len(carried))
	}
}
