package stage

import (
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// An action that reaches the network, a fetch with a command of its own,
// is sent to the runner sandboxes whose policy lets them reach it, by the
// platform property they advertise; an action that reaches nothing is
// sent to the others, and its platform names no such property. A test's
// own name for the property is tenant zero's (network=egress).
func TestAnActionThatReachesTheNetworkIsSentWhereItMay(t *testing.T) {
	snapshot := sourcetest.Taken(t, nil)
	fetch := plan.Action{Declared: &pluginv1.Action{
		Name: "fetch/modules", Fetch: true,
		Command: &pluginv1.Command{Arguments: []string{"go", "mod", "download"}},
		Outputs: []*pluginv1.Output{{Path: "~/go/pkg/mod/"}},
	}}
	_, staged := fetching(t, snapshot, fetch)
	if got := staged.Platform[exec.NetworkProperty]; got != exec.Egress {
		t.Errorf("a fetch is sent with %s=%q, want %q", exec.NetworkProperty, got, exec.Egress)
	}
	if !networked(staged.Arguments) {
		t.Errorf("a fetch's spec does not say it reaches the network: %q", staged.Arguments)
	}
	// The control: an action that reaches nothing names no network.
	compile := plan.Action{Declared: &pluginv1.Action{Command: &pluginv1.Command{Arguments: []string{"true"}}}}
	_, staged = fetching(t, snapshot, compile)
	if got, named := staged.Platform[exec.NetworkProperty]; named {
		t.Errorf("an action that reaches nothing is sent with %s=%q", exec.NetworkProperty, got)
	}
	// A test with no services reaches nothing either: it is sent to the
	// runner sandboxes that reach nothing, where its loopback is its own.
	test := plan.Action{Declared: &pluginv1.Action{Test: true, Command: &pluginv1.Command{Arguments: []string{"true"}}}}
	_, staged = fetching(t, snapshot, test)
	for _, property := range []string{exec.NetworkProperty, RunnerProperty} {
		if got, named := staged.Platform[property]; named {
			t.Errorf("a test with no services is sent with %s=%q", property, got)
		}
	}
	// What the provisioner holds is not written over: its platform is
	// every action's.
	if _, named := installed().Platform[exec.NetworkProperty]; named {
		t.Error("the workers' platform was changed by staging a fetch")
	}
}
