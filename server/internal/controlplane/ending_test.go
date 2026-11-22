package controlplane

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/sandbox"
	sandboxv1 "github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
)

// real is the real sandbox service, over a driver the test holds, as
// the control plane asks it: its calls go to the service itself, served
// as it is served to anyone. It leads to no port, so no daemon answers
// and a workspace over it does not come to be ready.
type real struct {
	sandboxv1alpha1connect.UnimplementedSandboxServiceHandler
	client sandboxv1alpha1connect.SandboxServiceClient
}

func (a real) CreateSandbox(ctx context.Context, r *sandboxv1.CreateSandboxRequest) (*sandboxv1.CreateSandboxResponse, error) {
	return a.client.CreateSandbox(ctx, r)
}
func (a real) DescribeSandbox(ctx context.Context, r *sandboxv1.DescribeSandboxRequest) (*sandboxv1.DescribeSandboxResponse, error) {
	return a.client.DescribeSandbox(ctx, r)
}
func (a real) ListSandboxes(ctx context.Context, r *sandboxv1.ListSandboxesRequest) (*sandboxv1.ListSandboxesResponse, error) {
	return a.client.ListSandboxes(ctx, r)
}
func (a real) DeleteSandbox(ctx context.Context, r *sandboxv1.DeleteSandboxRequest) (*sandboxv1.DeleteSandboxResponse, error) {
	return a.client.DeleteSandbox(ctx, r)
}
func (a real) DescribeCredential(ctx context.Context, r *sandboxv1.DescribeCredentialRequest) (*sandboxv1.DescribeCredentialResponse, error) {
	return a.client.DescribeCredential(ctx, r)
}
func (real) Dial(context.Context, string, int) (net.Conn, error) {
	return nil, errors.New("no port is led to here")
}
func (real) Proxy(w http.ResponseWriter, _ *http.Request, _ string, _ int, _ string) {
	http.Error(w, "no port is led to here", http.StatusBadGateway)
}
func (real) Tunnel(w http.ResponseWriter, _ *http.Request, _ string, _ int) {
	http.Error(w, "no port is led to here", http.StatusBadGateway)
}

// overReal serves a control plane over the real sandbox service, over a
// driver that holds sandboxes in memory, and returns a client of it and
// the driver.
func overReal(t *testing.T) (workspacev1alpha1connect.WorkspaceServiceClient, *drivertest.Memory, sandboxv1alpha1connect.SandboxServiceClient) {
	t.Helper()
	client, held, _, sandboxes := overRealServed(t)
	return client, held, sandboxes
}

// overRealServed is overReal, and returns too the control plane's
// address and a client of the sandbox service beneath it, as the
// process that serves the control plane holds it.
func overRealServed(t *testing.T) (workspacev1alpha1connect.WorkspaceServiceClient, *drivertest.Memory, string, sandboxv1alpha1connect.SandboxServiceClient) {
	t.Helper()
	held := drivertest.RuledMemory()
	under := httptest.NewUnstartedServer(nil)
	under.Start()
	t.Cleanup(under.Close)
	served, err := sandbox.New(sandbox.Options{Driver: held, PublicURL: under.URL, State: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	under.Config.Handler = served.Handler()
	sandboxes := sandboxv1alpha1connect.NewSandboxServiceClient(connect.NewClient(connecthttp.NewTransport(under.Client(), under.URL)))
	client, address, _ := serve(t, func(address string) Config {
		return Config{
			Sandboxes: real{client: sandboxes}, PublicURL: address, State: t.TempDir(), Box: "registry.example/acme/box:1", Daemon: "/opt/a/daemon",
			Fetching: system{}, Every: 10 * time.Millisecond, Answers: time.Minute, Callers: knowing(t),
		}
	})
	return client, held, address, sandboxes
}

// madeX asks for the workspace x and waits for it to be in a sandbox,
// and returns which.
func madeX(t *testing.T, client workspacev1alpha1connect.WorkspaceServiceClient, sandboxes sandboxv1alpha1connect.SandboxServiceClient) string {
	t.Helper()
	if _, err := client.CreateWorkspace(context.Background(), &workspacev1.CreateWorkspaceRequest{Name: "x", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	return inSandbox(t, client, sandboxes, "x")
}

// inSandbox waits for a workspace to be in a sandbox, and returns which,
// as the sandbox service beneath the control plane says: the workspace's
// status says where its build API is, and nothing of its sandbox.
func inSandbox(t *testing.T, client workspacev1alpha1connect.WorkspaceServiceClient, sandboxes sandboxv1alpha1connect.SandboxServiceClient, name string) string {
	t.Helper()
	for until := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		now, err := client.DescribeWorkspace(context.Background(), &workspacev1.DescribeWorkspaceRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if now.GetWorkspace().GetStatus().GetBuildUri() != "" {
			listed, err := sandboxes.ListSandboxes(context.Background(), &sandboxv1.ListSandboxesRequest{LabelSelector: Named + "=" + name})
			if err != nil {
				t.Fatal(err)
			}
			for i := len(listed.GetSandboxes()) - 1; i >= 0; i-- {
				if each := listed.GetSandboxes()[i]; each.GetState() != sandboxv1.Sandbox_STATE_ENDING {
					return each.GetId()
				}
			}
		}
		if time.Now().After(until) {
			t.Fatalf("the workspace %s is in no sandbox: %v", name, now.GetWorkspace().GetStatus())
		}
	}
}

// A workspace that is ended is there, ending, for as long as what makes
// sandboxes still holds its sandbox: one whose ending takes longer than
// its driver waits for is not gone, the workspace is not said to be,
// and its name is not another's to take. It is gone once the sandbox
// is, though no one ended the sandbox again, and its name, asked for
// again with what it had, is a workspace in another sandbox. This is
// the control plane over the real sandbox service; the control is the
// sandbox that ends at once
// (TestAWorkspaceThatIsEndedIsGoneAndItsNameCanBeGivenAgain).
func TestAWorkspaceIsNotGoneWhileItsSandboxIsStillEnding(t *testing.T) {
	client, held, sandboxes := overReal(t)
	ctx := context.Background()
	asked := &workspacev1.CreateWorkspaceRequest{Name: "x", Spec: of("https://forge.example/acme/code.git")}
	if _, err := client.CreateWorkspace(ctx, asked); err != nil {
		t.Fatal(err)
	}
	first := inSandbox(t, client, sandboxes, "x")
	if !held.Holds(first) {
		t.Fatalf("the driver does not hold the workspace's sandbox %s", first)
	}

	let := held.Linger()
	if _, err := client.DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	// Many passes over it go by, and the sandbox is still held.
	for until := time.Now().Add(500 * time.Millisecond); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		now, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "x"})
		if !held.Holds(first) {
			t.Fatalf("the driver let go of %s while its ending lingered", first)
		}
		if err != nil || now.GetWorkspace().GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_ENDING {
			t.Fatalf("the workspace, while the driver still holds its sandbox %s: %v, %v, want it there and ending", first, now.GetWorkspace().GetStatus(), err)
		}
	}
	if _, err := client.CreateWorkspace(ctx, asked); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("its name, asked for while it ends: %v, want it refused as there already", err)
	}

	// The provider lets go by itself.
	let()
	gone(t, client, "x")
	if held.Holds(first) {
		t.Errorf("the workspace is gone and the driver still holds %s", first)
	}
	again, err := client.CreateWorkspace(ctx, asked)
	if err != nil || !again.GetCreated() {
		t.Fatalf("the name given again: %v, %v", again, err)
	}
	if second := inSandbox(t, client, sandboxes, "x"); second == first || !held.Holds(second) {
		t.Errorf("the second of the name is in %s, the first was in %s: want another, which the driver holds", second, first)
	}
}

// A workspace whose sandbox is ending, though the workspace was not
// asked to end, has failed and says so: nothing more is asked of that
// sandbox. The control is the workspace before, which is ready.
func TestAWorkspaceWhoseSandboxIsEndingHasFailed(t *testing.T) {
	s, client := made(t, "x", of("https://forge.example/acme/code.git"))
	ctx := context.Background()
	ready := waited(t, client, "x")
	if ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		t.Fatalf("the workspace is %v, want ready", ready.GetStatus())
	}
	s.set(s.of(ready.GetName()), sandboxv1.Sandbox_STATE_ENDING)
	for until := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		now, err := client.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "x"})
		if err != nil {
			t.Fatal(err)
		}
		status := now.GetWorkspace().GetStatus()
		if status.GetState() == workspacev1.WorkspaceStatus_STATE_FAILED {
			if !strings.Contains(status.GetFailure(), "is ending") {
				t.Errorf("it failed saying %q, want that its sandbox is ending", status.GetFailure())
			}
			return
		}
		if time.Now().After(until) {
			t.Fatalf("the workspace whose sandbox is ending is %v, want failed", status)
		}
	}
}

// Ending a workspace ends the sandboxes the control plane made for it,
// and no other: a sandbox another asked for, labelled with the
// workspace's name, is not its own and is not ended with it. The control
// is the workspace's own sandbox, which is.
func TestASandboxAnotherLabelledForAWorkspaceIsNotEndedWithIt(t *testing.T) {
	client, held, _, sandboxes := overRealServed(t)
	ctx := context.Background()
	own := madeX(t, client, sandboxes)
	other, err := sandboxes.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{RequestId: "another-asking", Spec: &sandboxv1.SandboxSpec{
		Image: "registry.example/acme/box:1", Labels: map[string]string{Named: "x"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	theirs := other.GetSandbox().GetId()
	if _, err := client.DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	gone(t, client, "x")
	if held.Holds(own) {
		t.Errorf("the workspace is gone and its own sandbox %s is still held", own)
	}
	if !held.Holds(theirs) {
		t.Errorf("the sandbox %s another asked for, labelled with the workspace's name, was ended with it", theirs)
	}
}
