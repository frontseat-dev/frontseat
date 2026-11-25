package run

import (
	"path"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	"github.com/frontseat-dev/frontseat/build/engine/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// A check's fix, which runs where the daemon is, and the check, staged
// for the grid, ask mise for their environment by one action, so that
// mise is asked once for both: in a workspace that holds the repository
// in repos/x, as in the control, a checkout built on its own.
func TestACheckAndItsFixAskMiseForTheirEnvironmentOnce(t *testing.T) {
	for _, folder := range []string{".", "repos/x"} {
		snapshot := sourcetest.Workspace(t, map[string]string{"x": folder}, map[string]string{
			path.Join(folder, "mise.toml"): "[env]\nCONF = \"{{config_root}}/conf\"\n",
			path.Join(folder, "app/f.txt"): "as it is\n",
		})
		check := plan.Action{
			Label: model.Label{Path: "app", Plugin: "custom", Name: "lint"}, Repository: "x", ProjectRoot: "app",
			Sources:  []pattern.Group{{Directory: "app", Patterns: []string{"f.txt"}}},
			Declared: &pluginv1.Action{Name: "lint", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"true"}}, Rewrites: []string{"f.txt"}}}, Inputs: []*pluginv1.Input{{Kind: &pluginv1.Input_Source{Source: "f.txt"}}}, Command: &pluginv1.Command{Arguments: []string{"true"}}},
		}
		grid := &toolstest.Configured{}
		runner := Runner{Executor: grid, Answers: &exec.Remembering{Executor: grid}, Fix: true, Kept: t.TempDir()}
		finished := make(chan buildv1.Outcome, 1)
		if err := runner.Run(t.Context(), snapshot, []plan.Action{check}, told(finished)); err != nil {
			t.Fatal(err)
		}
		if outcome := <-finished; outcome != buildv1.Outcome_OUTCOME_PASSED {
			t.Fatalf("in %s, the check ended %v", folder, outcome)
		}
		if asked := len(grid.Asked()); asked != 1 {
			t.Errorf("in %s, mise was asked %d times for the environment of the check and its fix, want once", folder, asked)
		}
	}
}
