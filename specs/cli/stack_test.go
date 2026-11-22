package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
	"os/exec"
	"testing"

	"github.com/frontseat-dev/frontseat/local/ports"
	"github.com/frontseat-dev/frontseat/sdk/go"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/server/testsupport/testowner"
	"github.com/frontseat-dev/frontseat/server/testsupport/testsandbox"
)

// stack is where the local stack's control plane answers, which is where
// the command line looks.
var stack = testowner.Stack

// port is the stack's control plane's, as the port of a container is
// written.
var port = strconv.Itoa(ports.ControlPlane)

// served is the stack's one container, which holds its control plane, as
// the container runtime names it, and known where the stack lays out
// what the gateway knows the control plane by, in the stack's state.
const (
	served = "frontseat-local"
	known  = "/var/lib/frontseat/controlplane/openshell"
)

// The local stack serves frontseat's workspace API itself, to its owner:
// with the stack up, nothing is started by hand, what the gateway knows
// the server by is given to it by the stack and need be copied nowhere,
// and the owner's token is laid out where the command line finds it. A
// workspace asked of it is made by the gateway, clones its repository
// and builds.
//
// What answers on the port is held to be the stack's own service, the
// one that publishes it: a server a person started there would answer
// the same and prove nothing of the stack.
func TestTheStackMakesAWorkspaceWithNothingStartedByHand(t *testing.T) {
	testrequired.Stack(t)
	// Without a grid there is no stack, and the test is skipped; with
	// one, the control plane is part of it.
	testgrid.InSandbox(t)
	image := testbox.Image(t)

	out, err := exec.Command("docker", "inspect", "--format", "{{json .}}", served).Output()
	if err != nil {
		t.Fatalf("the stack has no control plane (%s): %v", served, err)
	}
	var service struct {
		State      struct{ Running bool }
		Config     struct{ Labels map[string]string }
		HostConfig struct {
			PortBindings map[string][]struct{ HostIp, HostPort string }
		}
	}
	if err := json.Unmarshal(out, &service); err != nil {
		t.Fatal(err)
	}
	if !service.State.Running || service.Config.Labels["frontseat.dev/checkout"] == "" {
		t.Fatalf("%s is not the stack's, started from a checkout, running: %s", served, out)
	}
	if published := service.HostConfig.PortBindings[port+"/tcp"]; len(published) != 1 || published[0].HostIp != "127.0.0.1" || published[0].HostPort != port {
		t.Fatalf("the stack's control plane is published at %v, want this machine's loopback, port %s, and nowhere else", published, port)
	}
	// What the gateway knows it by is laid out in the stack's state, the
	// control plane's alone to read.
	if laid, err := exec.Command("docker", "exec", served, "stat", "-c", "%U %a", known, known+"/tls.key").Output(); err != nil || string(laid) != "frontseat 700\nfrontseat 600\n" {
		t.Fatalf("the stack's control plane is not given what the gateway knows it by, its own alone: %q %v", laid, err)
	}

	owner := stackOwner(t)
	checkout := helloModule(t)

	made, _ := madeWorkspace(t, frontseat.New(stack, owner), frontseat.Spec{Image: image, Repositories: cloned(t, checkout)})
	run, labels := buildIn(t, made)
	if run.GetExitCode().GetName() != "SUCCESS" || run.GetCounts().GetPassed() == 0 {
		t.Fatalf("the build ended %s with %d actions passed (%s), want SUCCESS", run.GetExitCode().GetName(), run.GetCounts().GetPassed(), labels)
	}
}

// stackOwner presents the local stack's owner's token, as the stack laid
// it out, which is this person's alone.
func stackOwner(t *testing.T) frontseat.Option {
	t.Helper()
	return frontseat.WithToken(testowner.Token(t))
}

// Whatever reaches the stack's endpoint from this machine runs nothing in
// a sandbox: no call of the sandbox API is served there, to anyone, its
// owner among them, so neither listing the sandboxes nor running a
// command in one is answered. A call without the owner's token is
// refused before anything is asked of it. The control is the workspace
// API, which answers the owner.
func TestTheStacksEndpointRunsNothingInASandbox(t *testing.T) {
	testrequired.Stack(t)
	testgrid.InSandbox(t)
	// Without a token first, before anything is read of what the stack
	// laid out: refused as from no one known. Then the owner, who is
	// known, and is told there is no such call.
	for _, each := range []struct {
		name string
		want connect.Code
	}{{"no token", connect.CodeUnauthenticated}, {"the owner's token", connect.CodeUnimplemented}} {
		name, want := each.name, each.want
		var over []*http.Client
		if name != "no token" {
			over = append(over, testowner.Client(testowner.Token(t), stack))
		}
		sandboxes := testsandbox.Client(stack, over...)
		listed, err := sandboxes.ListSandboxes(context.Background(), &sandboxv1.ListSandboxesRequest{PageSize: 1000})
		if connect.CodeOf(err) != want {
			t.Errorf("ListSandboxes at %s with %s: %d sandboxes, %v; want it refused as %s", stack, name, len(listed.GetSandboxes()), err, want)
		}
		ran, err := sandboxes.ExecSandbox(context.Background(), &sandboxv1.ExecSandboxRequest{SandboxId: "sbx_000000000000", Arguments: []string{"true"}})
		if connect.CodeOf(err) != want {
			t.Errorf("ExecSandbox at %s with %s: %v, %v; want it refused as %s", stack, name, ran, err, want)
		}
	}
	if _, err := frontseat.New(stack).Workspaces(context.Background()); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("ListWorkspaces at %s with no token: %v, want it refused as unauthenticated", stack, err)
	}
	if _, err := frontseat.New(stack, stackOwner(t)).Workspaces(context.Background()); err != nil {
		t.Errorf("ListWorkspaces at %s with the owner's token: %v", stack, err)
	}
}
