package controlplanetest

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/sandbox"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgateway"
)

// A server stopped before it ended a sandbox leaves it to EndLeft, which
// has the gateway end it; the control is a sandbox another server made,
// recorded in another state, which the gateway holds running still, and
// which is then ended the same way.
func TestWhatAStoppedServerLeftInTheGatewayIsEnded(t *testing.T) {
	// A sandbox that runs until it is ended, and needs nothing of the
	// box: the box image's supervisor, told nothing, exits at once.
	image := testbox.Plain(t)
	identity := testgateway.Required(t)
	left := func() (string, string) {
		t.Helper()
		state := filepath.Join(t.TempDir(), "state")
		server, err := sandbox.New(sandbox.Options{PublicURL: "http://127.0.0.1", State: state, Gateway: &sandbox.Gateway{Address: testgateway.Address, Identity: identity, Runtime: "docker"}})
		if err != nil {
			t.Fatal(err)
		}
		// Closing it ends no sandbox: it lets go of its gateway.
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		made, err := server.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK}, Image: image, Arguments: []string{"sleep", "infinity"}}})
		if err != nil {
			t.Fatal(err)
		}
		// It is made, and not still being made, when its server is let go
		// of.
		if waited, err := server.WaitSandbox(ctx, &sandboxv1.WaitSandboxRequest{SandboxId: made.GetSandbox().GetId()}); err != nil || waited.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_RUNNING {
			t.Fatalf("the sandbox left is %v %q (%v), want it running", waited.GetSandbox().GetState(), waited.GetSandbox().GetFailure(), err)
		}
		// The server is let go of here, its sandbox not ended.
		return state, made.GetSandbox().GetId()
	}
	// What the gateway holds of it, as a server over its state finds.
	held := func(state, id string) sandboxv1.Sandbox_State {
		t.Helper()
		again, err := sandbox.New(sandbox.Options{PublicURL: "http://127.0.0.1", State: state, Gateway: &sandbox.Gateway{Address: testgateway.Address, Identity: identity, Runtime: "docker"}})
		if err != nil {
			t.Fatal(err)
		}
		defer again.Close()
		now, err := again.DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: id})
		if connect.CodeOf(err) == connect.CodeNotFound {
			return sandboxv1.Sandbox_STATE_UNSPECIFIED
		}
		if err != nil {
			t.Fatal(err)
		}
		return now.GetSandbox().GetState()
	}

	other, kept := left()
	state, id := left()
	if err := EndLeft(identity, state); err != nil {
		t.Error(err)
	}
	if now := held(state, id); now != sandboxv1.Sandbox_STATE_ENDING && now != sandboxv1.Sandbox_STATE_UNSPECIFIED {
		t.Errorf("a sandbox a stopped server left is %s, not ended", now)
	}
	if now := held(other, kept); now == sandboxv1.Sandbox_STATE_ENDING || now == sandboxv1.Sandbox_STATE_UNSPECIFIED {
		t.Errorf("the control: another server's sandbox is %s", now)
	}
	if err := EndLeft(identity, other); err != nil {
		t.Error(err)
	}
}
