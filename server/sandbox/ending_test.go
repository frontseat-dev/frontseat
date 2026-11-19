package sandbox_test

import (
	"context"
	"testing"

	connect "connectrpc.com/connect/v2"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
)

// madeOverMemory serves the sandbox API over a driver that holds its
// sandboxes in memory and asks it for a sandbox; it returns the driver, a
// client, and the sandbox made.
func madeOverMemory(t *testing.T, asked *sandboxv1.CreateSandboxRequest) (*drivertest.Memory, sandboxv1alpha1connect.SandboxServiceClient, *sandboxv1.Sandbox) {
	t.Helper()
	held := drivertest.NewMemory()
	client, _ := serveOver(t, held)
	made, err := client.CreateSandbox(context.Background(), asked)
	if err != nil {
		t.Fatal(err)
	}
	return held, client, made.GetSandbox()
}

// lingering asks for the end of a sandbox whose ending the driver then
// holds on to, which is not said to be deleted, and returns what lets the
// driver let go of it.
func lingering(t *testing.T, held *drivertest.Memory, client sandboxv1alpha1connect.SandboxServiceClient, id string) (gone func()) {
	t.Helper()
	gone = held.Linger()
	if _, err := client.DeleteSandbox(context.Background(), &sandboxv1.DeleteSandboxRequest{SandboxId: id}); err == nil {
		t.Fatal("a sandbox that was not gone was said to be deleted")
	}
	return gone
}

// A sandbox that could not be ended is still there: while its driver
// holds it, it is listed and described, ending, so that whoever asked
// for its end knows to ask again; nothing else is asked of it; and it
// is gone once the driver holds it no more. The control is the sandbox that ends at once, which is not
// found afterwards.
func TestASandboxWhoseEndingLingersIsStillListed(t *testing.T) {
	asked := &sandboxv1.CreateSandboxRequest{RequestId: "of-x", Spec: &sandboxv1.SandboxSpec{Image: "box", Labels: map[string]string{"of": "x"}}}
	held, client, at := madeOverMemory(t, asked)
	ctx := context.Background()
	control := create(t, client, &sandboxv1.SandboxSpec{Image: "box"})
	if _, err := client.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: control.GetId()}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: control.GetId()}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("one that ended at once: %v, want it not found", err)
	}

	gone := lingering(t, held, client, at.GetId())
	if !held.Holds(at.GetId()) {
		t.Fatal("the driver let go of a sandbox whose ending lingers")
	}
	listed, err := client.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{LabelSelector: "of=x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetSandboxes()) != 1 || listed.GetSandboxes()[0].GetId() != at.GetId() || listed.GetSandboxes()[0].GetState() != sandboxv1.Sandbox_STATE_ENDING {
		t.Errorf("listed %v while the driver still holds %s, want it listed, ending", listed.GetSandboxes(), at.GetId())
	}
	if now, err := client.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: at.GetId()}); err != nil || now.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_ENDING {
		t.Errorf("described while the driver still holds it: %v, %v, want it there, ending", now.GetSandbox().GetState(), err)
	}
	if _, err := client.PauseSandbox(ctx, &sandboxv1.PauseSandboxRequest{SandboxId: at.GetId()}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("one that is ending, paused: %v, want a failed precondition", err)
	}
	if again, err := client.CreateSandbox(ctx, asked); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("the request that made it, sent again while it ends: %v, %v, want a failed precondition", again.GetSandbox(), err)
	}
	if _, err := client.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: at.GetId()}); err == nil {
		t.Error("ended again while the driver still holds it, it was said to be deleted")
	}

	gone()
	if _, err := client.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: at.GetId()}); err != nil {
		t.Errorf("ended again once it is gone: %v", err)
	}
	if _, err := client.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: at.GetId()}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("once it is gone: %v, want it not found", err)
	}
}

// A sandbox whose ending lingered and that its provider then let go of,
// with no one asking again, is forgotten: it is listed no more, and the
// request that made it, sent again, makes another. The control is the
// request sent again before, which is answered with the first.
func TestASandboxThatEndedByItselfIsForgottenAndItsRequestMakesAnother(t *testing.T) {
	asked := &sandboxv1.CreateSandboxRequest{RequestId: "of-x", Spec: &sandboxv1.SandboxSpec{Image: "box", Labels: map[string]string{"of": "x"}}}
	held, client, first := madeOverMemory(t, asked)
	ctx := context.Background()
	if same, err := client.CreateSandbox(ctx, asked); err != nil || same.GetSandbox().GetId() != first.GetId() {
		t.Fatalf("the request sent again: %v, %v, want the first", same.GetSandbox(), err)
	}
	gone := lingering(t, held, client, first.GetId())
	gone()
	listed, err := client.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{LabelSelector: "of=x"})
	if err != nil || len(listed.GetSandboxes()) != 0 {
		t.Fatalf("listed %v, %v once the driver let go, want nothing", listed.GetSandboxes(), err)
	}
	second, err := client.CreateSandbox(ctx, asked)
	if err != nil || second.GetSandbox().GetId() == first.GetId() || second.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_RUNNING {
		t.Fatalf("the request sent again once its sandbox is gone: %v, %v, want another, running", second.GetSandbox(), err)
	}
	if !held.Holds(second.GetSandbox().GetId()) || held.Holds(first.GetId()) {
		t.Errorf("the driver holds the first: %v, the second: %v", held.Holds(first.GetId()), held.Holds(second.GetSandbox().GetId()))
	}
}

// The request that made a sandbox, sent again once the sandbox is gone
// and before anything listed it, makes another all the same.
func TestARequestWhoseSandboxIsGoneUnlistedMakesAnother(t *testing.T) {
	asked := &sandboxv1.CreateSandboxRequest{RequestId: "of-y", Spec: &sandboxv1.SandboxSpec{Image: "box"}}
	held, client, first := madeOverMemory(t, asked)
	ctx := context.Background()
	gone := lingering(t, held, client, first.GetId())
	gone()
	second, err := client.CreateSandbox(ctx, asked)
	if err != nil || second.GetSandbox().GetId() == first.GetId() {
		t.Fatalf("the request sent again once its sandbox is gone: %v, %v, want another", second.GetSandbox(), err)
	}
}
