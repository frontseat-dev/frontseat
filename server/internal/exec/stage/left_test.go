package stage

import (
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// An action that passed leaves what it says it writes. What it says it
// may not leave is not missed where it is not there, and is handed on
// where it is.
func TestOnlyAnOutputThatMayBeAbsentIsNotMissed(t *testing.T) {
	tree := source.Tree{Paths: map[string]string{"lab": "lab"}}
	declared := func(outputs ...*pluginv1.Output) plan.Action {
		return plan.Action{Repository: "lab", ProjectRoot: "tool", Declared: &pluginv1.Action{Outputs: outputs}}
	}
	made, programs := &pluginv1.Output{Path: "made/"}, &pluginv1.Output{Path: "~/go/bin/", Optional: true}
	held := exec.Held{Digest: exec.Digest{Hash: "tree", Size: 1}, Directory: true}
	for name, c := range map[string]struct {
		action  plan.Action
		grid    map[string]exec.Held
		left    []string
		missing string
	}{
		"both are left":                      {declared(made, programs), map[string]exec.Held{"made": held, "../../.frontseat/home/go/bin": held}, []string{"lab/tool/made", ".frontseat/home/go/bin"}, ""},
		"the one that may be absent is":      {declared(made, programs), map[string]exec.Held{"made": held}, []string{"lab/tool/made"}, ""},
		"the one that may not be absent is":  {declared(made, programs), map[string]exec.Held{"../../.frontseat/home/go/bin": held}, nil, "made/"},
		"the one that may be absent, alone":  {declared(programs), nil, nil, ""},
		"what must be there comes second":    {declared(programs, made), nil, nil, "made/"},
		"what must be there, and nothing is": {declared(made), nil, nil, "made/"},
	} {
		left, missing, err := Left(tree, c.action, c.grid)
		if err != nil || missing != c.missing {
			t.Errorf("%s: missing %q, %v; want %q", name, missing, err, c.missing)
			continue
		}
		if c.missing != "" {
			continue
		}
		if len(left) != len(c.left) {
			t.Errorf("%s: left %v, want %v", name, left, c.left)
		}
		for _, at := range c.left {
			if _, there := left[at]; !there {
				t.Errorf("%s: nothing is left at %s, of %v", name, at, left)
			}
		}
	}
}
