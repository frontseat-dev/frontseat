package run

import (
	"context"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// told is the events of a run nobody records, and the outcome of each
// action that finished, on a channel.
func told(finished chan<- buildv1.Outcome) Events {
	return Events{
		Planned:   func() {},
		Restarted: func(string, string) {},
		Started:   func(plan.Action) {},
		Finished:  func(result *buildv1.ActionResult) { finished <- result.GetOutcome() },
		Entity:    func(string) *typev1.EntityRef { return nil },
		Print:     func([]byte) *buildv1.Digest { return nil },
		Patched:   func(string, []string) {},
	}
}

// A run that is cancelled has ended only once nothing of it asks the
// grid: the action that waited for its tool's install gives up at once
// and is finished, the install is told to stop and is still inside its
// call to the grid, and Run returns only once that call has. Whoever
// closes the grid after the run closes it under nothing. The control is
// the same install before it is let, which had not ended.
func TestARunHasEndedOnlyOnceTheInstallItStoppedHas(t *testing.T) {
	root := t.TempDir()
	testpins.Go(t, root)
	snapshot, err := new(source.Keeper).Take(t.Context(), source.Tree{Root: root, Paths: map[string]string{"": "."}})
	if err != nil {
		t.Fatal(err)
	}
	grid := toolstest.NewUnwinding()
	runner := Runner{Executor: grid, Answers: &exec.Remembering{Executor: grid}, Platform: testgrid.Platform()}
	compile := plan.Action{
		Label:    model.Label{Path: "module", Plugin: "go", Name: "compile"},
		Declared: &pluginv1.Action{Name: "compile", Tools: []string{"go"}, Command: &pluginv1.Command{Arguments: []string{"go", "build", "./..."}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	finished, returned := make(chan buildv1.Outcome, 1), make(chan struct{})
	go func() {
		defer close(returned)
		if err := runner.Run(ctx, snapshot, []plan.Action{compile}, told(finished)); err != nil {
			t.Error(err)
		}
	}()
	grid.Begun(t)
	cancel()
	// The action is not kept waiting for the install it gave up on.
	select {
	case outcome := <-finished:
		if outcome != buildv1.Outcome_OUTCOME_ERRORED {
			t.Errorf("the action that gave up waiting for its tool ended %v", outcome)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the action went on waiting for the install it had given up on")
	}
	grid.KeepsBack(t, "the run", returned)
}
