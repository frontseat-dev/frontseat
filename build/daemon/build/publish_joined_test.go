package build

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// publishHeld is bringing's grid, which holds what a publish asks it of
// mise: the first thing a publish does is have its artifacts brought,
// and whatever asks mise for an environment from then on is held, told
// to stop when the daemon ends, and goes on until the test lets it.
type publishHeld struct {
	bringing
	publishing *atomic.Bool
	asked      *toolstest.Unwinding
}

func (p publishHeld) Materialize(ctx context.Context, held exec.Held, into string) error {
	p.publishing.Store(true)
	return p.bringing.Materialize(ctx, held, into)
}

func (p publishHeld) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if toolstest.Asks(action) && p.publishing.Load() {
		return exec.Result{}, p.asked.Hold(ctx)
	}
	return p.bringing.Execute(ctx, action)
}

// A daemon that ends while a release publishes has ended only once
// nothing of the publish asks the grid: the publish gives up at once on
// what it asked of mise, which is told to stop and is still inside its
// call to the grid, and the daemon, which lets its grid go when it has
// closed, closes only once that call has returned. The control is the
// same call before it is let, which had not ended.
func TestADaemonEndsOnlyOnceWhatItsPublishAskedOfTheGridHasStopped(t *testing.T) {
	grid := publishHeld{publishing: &atomic.Bool{}, asked: toolstest.NewUnwinding()}
	c, _, _ := givenACheckoutWith(t, grid, "release/1.0", publishingSource(t, t.TempDir(), false, false), 3)
	s := &releaseService{graph: c.daemon.releases.graph, releases: c.daemon.releases}
	going := releaseBegun(t, s, "greet")
	grid.asked.Begun(t)
	closed := make(chan struct{})
	go func() { c.daemon.Close(); close(closed) }()
	grid.asked.KeepsBack(t, "the daemon's close", closed)
	if release := releaseEnded(t, s, going.GetId()); release.GetState() != buildv1.Release_STATE_FAILED {
		t.Errorf("the release the daemon ended as it published ended %v (%s)", release.GetState(), release.GetFailure())
	}
}
