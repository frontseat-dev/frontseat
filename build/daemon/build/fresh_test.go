package build

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// keptGrid is a grid that keeps what it ran, by the action as it was
// sent, and answers an action it kept from that, unless it is sent
// fresh; it notes, by label, whether each action it ran was sent fresh.
type keptGrid struct {
	leaving
	mu   *sync.Mutex
	kept map[string]bool
	ran  map[string][]bool
}

// Key implements exec.Keyed: the daemon remembers what the grid
// answered by it.
func (k keptGrid) Key(_ context.Context, action exec.Action) (string, error) {
	return fmt.Sprint(action.Label, action.Arguments, action.Environment, action.Outputs), nil
}

func (k keptGrid) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if installs(action) || toolstest.Asks(action) {
		return k.leaving.Execute(ctx, action)
	}
	key, _ := k.Key(ctx, action)
	k.mu.Lock()
	kept := k.kept[key] && !action.Fresh
	if !kept {
		k.kept[key] = true
		k.ran[action.Label] = append(k.ran[action.Label], action.Fresh)
	}
	k.mu.Unlock()
	result, err := k.leaving.Execute(ctx, action)
	result.Cached = kept
	return result, err
}

// sent is, by label, whether each run of an action was sent fresh.
func (k keptGrid) sent(label string) []bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.ran[label])
}

// A release runs the actions that carry its version and those that
// package what it ships, whatever was kept of them: neither what the
// daemon remembers nor what the grid kept answers them, and each is
// sent to the grid to be run fresh. A package that a build for
// production built before, as the same action, is run again for the
// release. The control is a second build for production, which no
// worker runs anything for, the daemon remembering what the first was
// answered; and the release's action that neither carries its version
// nor packages it, which is answered from what was kept, as are those of
// another component, which the release does not move, that would be
// fresh were it moved.
func TestAReleaseRunsItsVersionedAndPackagingActionsWhateverWasKept(t *testing.T) {
	grid := keptGrid{mu: &sync.Mutex{}, kept: map[string]bool{}, ran: map[string][]bool{}}
	c, _ := releasingClients(t, grid, "release/1.0", func(remote string) {
		// greet's bundle reads what log packages and the version it
		// carries, so that a release of greet alone builds log's actions
		// too, a unit it does not move.
		for name, more := range map[string][2]string{"greet": {"  dependsOn: [component:default/log]\n", "      bundle:\n        command: [tool, bundle]\n        inputs: [\"//log:custom.pack\", \"//log:custom.says\"]\n        outputs: [out/bundle]\n"}, "log": {}} {
			write(t, remote, name+"/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: "+name+"\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n"+more[0]+"  frontseat:\n    actions:\n      pack:\n        command: [tool, pack]\n        outputs: [out/"+name+".tgz]\n        kind: package\n      says:\n        command: [echo, \"{frontseat.version}\"]\n        outputs: [out/says.txt]\n        versioned: true\n      notes:\n        command: [tool, notes]\n        outputs: [out/notes.txt]\n"+more[1])
		}
	})
	const pack, says, notes = "//services/greet:custom.pack", "//services/greet:custom.says", "//services/greet:custom.notes"
	// log's, which a release of greet alone does not move.
	const logPack, logSays = "//services/log:custom.pack", "//services/log:custom.says"
	production := func(when string) {
		t.Helper()
		if ended := wait(t, c.runs, createAs(t, c.runs, typev1.Profile_PROFILE_PRODUCTION).GetId()); ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
			t.Fatalf("%s: a build for production ended %s: %s", when, ended.GetExitCode(), ended.GetFailure())
		}
	}
	production("first")
	for _, label := range []string{pack, says, notes, logPack, logSays} {
		if got := grid.sent(label); !slices.Equal(got, []bool{false}) {
			t.Fatalf("the first build for production sent %s %v (fresh, each time), want once and not fresh", label, got)
		}
	}
	// The control: built again, nothing is run again.
	production("again")
	for _, label := range []string{pack, says, notes, logPack, logSays} {
		if got := grid.sent(label); !slices.Equal(got, []bool{false}) {
			t.Errorf("the control: a second build for production had %s run again (%v), want it answered from what was kept", label, got)
		}
	}

	releasedAlone(t, c, "greet")
	for _, label := range []string{pack, says} {
		if got := grid.sent(label); !slices.Equal(got, []bool{false, true}) {
			t.Errorf("the release sent %s %v (fresh, each time), want it run again, fresh, though the same action was kept", label, got)
		}
	}
	if got := grid.sent(notes); !slices.Equal(got, []bool{false}) {
		t.Errorf("the control: the release had %s, which neither carries its version nor packages it, run again (%v), want it answered from what was kept", notes, got)
	}
	for _, label := range []string{logPack, logSays} {
		if got := grid.sent(label); !slices.Equal(got, []bool{false}) {
			t.Errorf("the control: the release of greet had %s, of log, which it does not move, run again (%v), want it answered from what was kept: only a unit the release moves is built fresh", label, got)
		}
	}
}
