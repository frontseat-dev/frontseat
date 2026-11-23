package sandbox

import (
	"context"
	"testing"

	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
)

// The service, called in its caller's process, keeps a spec of its own:
// what the caller changes of the spec it asked with, or of one it was
// answered, is no change of the sandbox's. The control is the spec as it
// was asked for, which is what each answer says.
func TestASpecAskedOrAnsweredIsNotTheServicesOwn(t *testing.T) {
	ctx := context.Background()
	s := started(t, drivertest.NewMemory(), t.TempDir())
	asked := box(func(spec *sandboxv1.SandboxSpec) { spec.Labels = map[string]string{"owner": "someone"} })
	made, err := s.CreateSandbox(ctx, asked)
	if err != nil {
		t.Fatal(err)
	}
	id := made.GetSandbox().GetId()
	asked.Spec.Labels["owner"] = "another"
	made.GetSandbox().GetSpec().Labels["owner"] = "a third"
	described, err := s.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: id})
	if err != nil {
		t.Fatal(err)
	}
	if got := described.GetSandbox().GetSpec().GetLabels()["owner"]; got != "someone" {
		t.Errorf("the sandbox is described as of %q, want it as it was asked for", got)
	}
	described.GetSandbox().GetSpec().Labels["owner"] = "a fourth"
	again, err := s.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: id})
	if err != nil {
		t.Fatal(err)
	}
	if got := again.GetSandbox().GetSpec().GetLabels()["owner"]; got != "someone" {
		t.Errorf("after an answer was changed, the sandbox is described as of %q, want it as it was asked for", got)
	}
}
