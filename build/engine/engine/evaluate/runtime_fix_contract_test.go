package evaluate

import (
	"errors"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/config"
	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// A name a test's own command sets is not given as written as well:
// refused of the catalog file that wrote it, whether or not the build
// fixes. The control is a test whose command sets no such name, which
// is given its Component's setting. (A test has no fix: an action is of
// one kind, and only a check has one.)
func TestATestIsNotGivenWhatItsOwnCommandSets(t *testing.T) {
	owner := entity.New("component", "default", "app")
	configured := map[string][]config.TestRuntime{"": {{Entity: owner, Directory: "app", File: "app/catalog-info.yaml", Environment: map[string]string{"TEST_REVIEW_VALUE": "configured"}}}}
	evaluation := func(environment map[string]string) Evaluation {
		declared := &pluginv1.Action{Name: "test", Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}, Command: &pluginv1.Command{Arguments: []string{"true"}, Environment: environment}}
		return Evaluation{Entities: []record.Located{{Root: "app", Ref: owner}}, Actions: []plan.Action{{Label: model.Label{Path: "app", Plugin: "fixture", Name: "test"}, ProjectRoot: "app", Declared: declared}}}
	}
	out := evaluation(map[string]string{"TEST_REVIEW_VALUE": "own"})
	err := testRuntimes(&out, configured, nil)
	var mistake *model.Mistake
	if !errors.As(err, &mistake) || mistake.Where() != "app/catalog-info.yaml" || !strings.Contains(mistake.Message, "TEST_REVIEW_VALUE") {
		t.Fatalf("given what its command sets, the setting was taken: %v", err)
	}
	out = evaluation(nil)
	if err := testRuntimes(&out, configured, nil); err != nil {
		t.Fatal(err)
	}
	if out.Actions[0].Environment["TEST_REVIEW_VALUE"] != "configured" {
		t.Fatal("a test whose command sets nothing was not given its Component's setting")
	}
}
