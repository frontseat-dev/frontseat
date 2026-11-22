package evaluate

import (
	"errors"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"strings"
	"testing"
)

// A test that is a check with a fix is a declaration the host takes,
// and its Component's test settings reach it.
func TestATestThatIsACheckWithAFixIsGivenItsComponentsSettings(t *testing.T) {
	action := &pluginv1.Action{Name: "test", Test: true, Check: true, Inputs: pluginhosttest.SourceInputs("*.txt"), Rewrites: []string{"*.txt"}, Command: &pluginv1.Command{Arguments: []string{"true"}}, Fix: &pluginv1.Command{Arguments: []string{"sh", "-c", "printf '%s' \"$TEST_REVIEW_VALUE\" > own.txt"}}}
	response := &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Root: "", Projects: []*pluginv1.Project{{Root: "app", Name: "app", Kind: "fixture", Manifest: "app/fixture.toml", Actions: []*pluginv1.Action{action}}}}}}
	if err := pluginhost.CheckAnalysis("fixture", []string{""}, response); err != nil {
		t.Fatalf("test/check fix is already a served declaration: %v", err)
	}
	owner := entity.New("component", "default", "app")
	out := Evaluation{Entities: []record.Located{{Root: "app", Ref: owner}}, Actions: []plan.Action{{Label: model.Label{Path: "app", Plugin: "fixture", Name: "test"}, ProjectRoot: "app", Declared: action}}}
	configured := map[string][]config.TestRuntime{"": {{Entity: owner, Directory: "app", File: "app/catalog-info.yaml", Environment: map[string]string{"TEST_REVIEW_VALUE": "configured"}}}}
	if err := testRuntimes(&out, configured, nil); err != nil {
		t.Fatal(err)
	}
	if out.Actions[0].Environment["TEST_REVIEW_VALUE"] != "configured" {
		t.Fatal("test runtime did not apply")
	}
}

// A name a test's own command sets, or its fix's, is not given as
// written as well: refused of the catalog file that wrote it, whether
// or not the build fixes, beside a name neither sets (the test above).
func TestATestIsNotGivenWhatItsOwnCommandsSet(t *testing.T) {
	for name, declared := range map[string]*pluginv1.Action{
		"its command": {Name: "test", Test: true, Command: &pluginv1.Command{Arguments: []string{"true"}, Environment: map[string]string{"TEST_REVIEW_VALUE": "own"}}},
		"its fix": {Name: "test", Test: true, Check: true, Rewrites: []string{"*.txt"}, Command: &pluginv1.Command{Arguments: []string{"true"}},
			Fix: &pluginv1.Command{Arguments: []string{"true"}, Environment: map[string]string{"TEST_REVIEW_VALUE": "own"}}},
	} {
		t.Run(name, func(t *testing.T) {
			owner := entity.New("component", "default", "app")
			out := Evaluation{Entities: []record.Located{{Root: "app", Ref: owner}}, Actions: []plan.Action{{Label: model.Label{Path: "app", Plugin: "fixture", Name: "test"}, ProjectRoot: "app", Declared: declared}}}
			configured := map[string][]config.TestRuntime{"": {{Entity: owner, Directory: "app", File: "app/catalog-info.yaml", Environment: map[string]string{"TEST_REVIEW_VALUE": "configured"}}}}
			err := testRuntimes(&out, configured, nil)
			var mistake *model.Mistake
			if !errors.As(err, &mistake) || mistake.File != "app/catalog-info.yaml" || !strings.Contains(mistake.Message, "TEST_REVIEW_VALUE") {
				t.Fatalf("given what %s sets, the setting was taken: %v", name, err)
			}
		})
	}
}
