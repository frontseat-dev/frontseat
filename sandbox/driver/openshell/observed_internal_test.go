package openshell

import (
	"testing"

	client "github.com/ai-openshell/sdk-go/openshell/v1"

	"github.com/frontseat-dev/frontseat/sandbox/driver"
)

// A sandbox the gateway says has failed says why by each condition that
// does not hold: its name, the gateway's reason for it, and what the
// gateway says of it. A condition that holds is not part of why. The
// control is the same sandbox ready, which has not failed and says
// nothing.
func TestASandboxThatFailedSaysWhichConditionsDoNotHoldAndWhy(t *testing.T) {
	held := &client.Sandbox{Labels: map[string]string{identity: "sbx_1"}}
	held.Status.Phase = client.SandboxError
	held.Status.Conditions = []client.SandboxCondition{
		{Type: "Ready", Status: "False", Reason: "RuntimeRestart"},
		{Type: "Scheduled", Status: "True", Reason: "Placed", Message: "placed"},
		{Type: "ConfigurationReady", Status: "False", Reason: "ConfigurationPending", Message: "Waiting for validation"},
	}
	got := observed(held)
	want := "the sandbox is error; Ready (RuntimeRestart); ConfigurationReady (ConfigurationPending): Waiting for validation"
	if got.State != driver.Failed || got.Message != want {
		t.Errorf("a sandbox that failed is %v, saying %q; want it failed, saying %q", got.State, got.Message, want)
	}
	held.Status.Phase = client.SandboxReady
	if got := observed(held); got.State != driver.Running || got.Message != "" {
		t.Errorf("the control, the same sandbox ready, is %v, saying %q", got.State, got.Message)
	}
}
