package openshell_test

import (
	"context"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"github.com/ai-openshell/sdk-go/openshell/v1/fake"

	"github.com/frontseat-dev/frontseat/sandbox"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/driver/openshell"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

// What this driver refuses reaches whoever asked the sandbox service as
// the one kind it is: a directory of the host bound in by a compute
// driver that binds none, a capability the gateway does not serve, is
// UNIMPLEMENTED CAPABILITY_NOT_SERVED; a subpath that is not there
// beneath its volume, what is not well formed, is INVALID_ARGUMENT
// SPEC_INVALID. Nothing is made of either. The control is the same
// volume, whole, over a compute driver that binds it, which is taken.
func TestWhatTheDriverRefusesReachesTheCallerAsTheKindItIs(t *testing.T) {
	bound := func(subpath string) *sandboxv1.CreateSandboxRequest {
		return &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{
			Image:   "box",
			Egress:  &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK},
			Volumes: []*sandboxv1.Volume{{Name: "work", Kind: sandboxv1.Volume_KIND_HOST, Source: t.TempDir(), Path: "/work", Subpath: subpath}},
		}}
	}
	for name, c := range map[string]struct {
		compute string
		asked   *sandboxv1.CreateSandboxRequest
		code    connect.Code
		reason  string
	}{
		"a directory of its host bound in by a compute driver that binds none": {"kubernetes", bound(""), connect.CodeUnimplemented, "CAPABILITY_NOT_SERVED"},
		"a subpath that is not there beneath its volume":                       {"podman", bound("missing"), connect.CodeInvalidArgument, "SPEC_INVALID"},
	} {
		gateway := fake.NewClient()
		s := serving(t, openshell.OverHeld(t, gateway, c.compute))
		_, err := s.CreateSandbox(context.Background(), c.asked)
		if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != c.code || reason != c.reason {
			t.Errorf("%s: %v (%s), want it refused %v %s", name, err, reason, c.code, c.reason)
		}
		if held, err := gateway.Sandboxes().ListAll(context.Background(), "default"); err != nil || len(held) != 0 {
			t.Errorf("%s: refused, %d sandboxes were made (%v)", name, len(held), err)
		}
	}
	gateway := fake.NewClient()
	s := serving(t, openshell.OverHeld(t, gateway, "podman"))
	made, err := s.CreateSandbox(context.Background(), bound(""))
	if err != nil {
		t.Fatalf("the control: the whole volume over a compute driver that binds it: %v", err)
	}
	// Taken is made: CreateSandbox answers before the driver has made it,
	// so what the control proves is read once the making has ended (the
	// fake gateway makes it, and never says it is ready).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := made.GetSandbox().GetId()
	if err := s.Made(ctx, id); err != nil {
		t.Fatal(err)
	}
	if now, err := s.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: id}); err != nil || now.GetSandbox().GetState() == sandboxv1.Sandbox_STATE_FAILED {
		t.Errorf("the control: the whole volume over a compute driver that binds it is %v %q (%v), want it made", now.GetSandbox().GetState(), now.GetSandbox().GetFailure(), err)
	}
	if held, err := gateway.Sandboxes().ListAll(context.Background(), "default"); err != nil || len(held) != 1 {
		t.Errorf("the control: %d sandboxes were made (%v), want the one", len(held), err)
	}
}

// serving is the sandbox service over a driver, closed when the test
// ends.
func serving(t *testing.T, d *openshell.Driver) *sandbox.Server {
	t.Helper()
	s, err := sandbox.New(sandbox.Options{Driver: d, PublicURL: "http://sandboxes.example", State: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
