package publish_test

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/build/engine/exec/publish"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
)

// What publishes a release of a repository a workspace holds in repos/x
// asks mise for its environment by the action a checkout of the
// repository built on its own asks it by, which is the one an action of
// the repository asks it by on the grid (stage): its repository at the
// top of the root. A path mise says beneath that root, as the
// repository's mise.toml says one ({{config_root}}), is beneath the
// repository where the publish lays out its root. The control is the
// checkout built on its own.
func TestAPublishOfARepositoryInAWorkspaceIsGivenWhatItsActionsAre(t *testing.T) {
	// It prints what it was given, and where it runs, to said.
	said := filepath.Join(t.TempDir(), "said")
	action := plan.Action{Repository: "x", Declared: &pluginv1.Action{
		Name:    "release",
		Kind:    &pluginv1.Action_Publish{Publish: &pluginv1.Publish{}},
		Command: &pluginv1.Command{Arguments: []string{"sh", "-c", `printf '%s\n%s' "$CONF" "$(pwd -P)" > "$0"`, said}},
	}}
	keys := map[string]string{}
	for _, folder := range []string{".", "repos/x"} {
		config := path.Join(folder, "mise.toml")
		snapshot := sourcetest.Workspace(t, map[string]string{"x": folder}, map[string]string{config: "[env]\nCONF = \"{{config_root}}/conf\"\n"})
		// Where the publish's work is, as the shell says its directory.
		kept, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		grid := &toolstest.Configured{Said: map[string]string{"CONF": "./conf"}}
		if err := (publish.Runner{Tools: &tools.Provisioner{Executor: grid}, Kept: kept}).Run(t.Context(), snapshot, action, publish.Release{}); err != nil {
			t.Fatalf("in %s: %v", folder, err)
		}
		if len(grid.Asked()) != 1 {
			t.Fatalf("in %s, mise was asked %d times for the publish's environment, want once", folder, len(grid.Asked()))
		}
		printed, err := os.ReadFile(said)
		if err != nil {
			t.Fatal(err)
		}
		// It runs in the directory of what it is given, beside its root.
		conf, given, _ := strings.Cut(string(printed), "\n")
		if laid := filepath.Join(filepath.Dir(given), "work", folder, "conf"); conf != laid {
			t.Errorf("in %s, mise's path is given as %s, want %s, beneath the repository in the publish's root", folder, conf, laid)
		}
		keys[folder] = reapitest.Key(t, grid.Asked()[0])
	}
	if keys["repos/x"] != keys["."] {
		t.Errorf("mise is asked for the publish's environment by %s in repos/x and by %s in a checkout built on its own, want one action", keys["repos/x"], keys["."])
	}
}
