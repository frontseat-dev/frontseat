package fix_test

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/build/engine/exec/fix"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	"github.com/frontseat-dev/frontseat/build/engine/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
)

// A fix of a repository a workspace holds in repos/x asks mise for its
// environment by the action a checkout of the repository built on its
// own asks it by, which is the one its check asks it by on the grid
// (stage): its repository at the top of the root. A path mise says
// beneath that root, as the repository's mise.toml says one
// ({{config_root}}), is beneath the repository where the fix's copy
// lays its files. The control is the checkout built on its own.
func TestAFixOfARepositoryInAWorkspaceIsGivenWhatItsCheckIs(t *testing.T) {
	keys := map[string]string{}
	for _, folder := range []string{".", "repos/x"} {
		snapshot := sourcetest.Workspace(t, map[string]string{"x": folder}, map[string]string{
			path.Join(folder, "mise.toml"): "[env]\nCONF = \"{{config_root}}/conf\"\n",
			path.Join(folder, "app/f.txt"): "before\n",
		})
		action := plan.Action{
			Label: model.Label{Path: "app", Plugin: "custom", Name: "own"}, Repository: "x", ProjectRoot: "app",
			Sources:  []pattern.Group{{Directory: "app", Patterns: []string{"f.txt"}}},
			Declared: &pluginv1.Action{Name: "own", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"sh", "-c", `printf '%s\n%s' "$CONF" "$(pwd -P)" > f.txt`}}, Rewrites: []string{"f.txt"}}}, Inputs: []*pluginv1.Input{{Kind: &pluginv1.Input_Source{Source: "f.txt"}}}, Command: &pluginv1.Command{Arguments: []string{"true"}}},
		}
		// Where the fix's copy is, as the shell says its directory.
		kept, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		grid := &toolstest.Configured{Said: map[string]string{"CONF": "./conf"}}
		changes, err := fix.Runner{Tools: &tools.Provisioner{Executor: grid}, Kept: kept}.Run(context.Background(), snapshot, action)
		if err != nil {
			t.Fatalf("in %s: %v", folder, err)
		}
		if len(changes) != 1 || len(grid.Asked()) != 1 {
			t.Fatalf("in %s, the fix changed %d files and asked mise %d times, want f.txt and once", folder, len(changes), len(grid.Asked()))
		}
		said, project, _ := strings.Cut(string(changes[0].Content), "\n")
		if laid := filepath.Join(filepath.Dir(project), "conf"); said != laid || !strings.HasSuffix(said, filepath.Join("work", folder, "conf")) {
			t.Errorf("in %s, mise's path is given as %s, want %s, beneath the repository where the fix's copy lays it", folder, said, laid)
		}
		keys[folder] = reapitest.Key(t, grid.Asked()[0])
	}
	if keys["repos/x"] != keys["."] {
		t.Errorf("mise is asked for the fix's environment by %s in repos/x and by %s in a checkout built on its own, want one action", keys["repos/x"], keys["."])
	}
}
