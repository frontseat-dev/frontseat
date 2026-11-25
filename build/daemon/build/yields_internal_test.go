package build

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
)

// holding is a grid that runs an action as scripted does while hold is
// false, and, while it is true, until the action is cancelled; what the
// daemon asks of the grid itself for the tools it runs at once.
type holding struct {
	scripted
	hold *atomic.Bool
}

func (h holding) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if h.hold.Load() && !installs(action) && !toolstest.Asks(action) {
		<-ctx.Done()
		return exec.Result{}, ctx.Err()
	}
	return h.scripted.Execute(ctx, action)
}

// Holding is a daemon over a workspace of one Go module, its files
// settled, looking every millisecond, for the test of its life
// (yields_test.go), which is of a package of its own to import the life
// that imports this one: its actions run while hold is false, and until
// they are cancelled while it is true.
func Holding(t *testing.T) (daemon *Daemon, hold *atomic.Bool) {
	t.Helper()
	workspace := oneModule(t)
	settled(t, workspace)
	plugins, err := goAndCatalog()
	if err != nil {
		t.Fatal(err)
	}
	hold = &atomic.Bool{}
	c := serveWith(t, configuration{Workspace: workspace, Mounted: true, Executor: holding{hold: hold}}, plugins)
	c.daemon.runs.every = time.Millisecond
	return c.daemon, hold
}
