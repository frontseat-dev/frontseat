package build

import (
	"context"
	"testing"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// A daemon says whether it has anything in hand: a build that has not
// ended is in hand though no call waits on it, since it was asked for
// and is answered later, and a daemon is not to end itself under it.
// The controls are the daemon before the build was asked for and after
// it was cancelled and ended, which has nothing in hand.
func TestADaemonHasABuildInHandUntilItEnds(t *testing.T) {
	workspace := t.TempDir()
	write(t, workspace, "frontseat.yaml", "plugins: {}\n")
	write(t, workspace, "catalog-info.yaml", `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: one
spec:
  type: service
  lifecycle: production
  owner: team
  frontseat:
    actions:
      held:
        command: ["true"]
`)
	c := serve(t, scripted{hang: true}, workspace, true)
	if c.daemon.Busy() {
		t.Fatal("the control: a daemon asked for nothing has something in hand")
	}
	run := create(t, c.runs)
	if !c.daemon.Busy() {
		t.Error("a daemon with a build that has not ended has nothing in hand")
	}
	if _, err := c.runs.CancelBuild(context.Background(), &buildv1.CancelBuildRequest{BuildId: run.GetId()}); err != nil {
		t.Fatal(err)
	}
	wait(t, c.runs, run.GetId())
	for begun := time.Now(); c.daemon.Busy(); time.Sleep(5 * time.Millisecond) {
		if time.Since(begun) > 30*time.Second {
			t.Fatal("thirty seconds after its build ended the daemon still has it in hand")
		}
	}
}
