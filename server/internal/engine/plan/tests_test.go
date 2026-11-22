package plan

import (
	"errors"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// Tests are chosen by what their plugins say of them, never by their
// names: an action that runs tests and is named "check" is one, and one
// named "test" that its plugin does not say runs tests is not. What a
// test needs is kept, and nothing else. A selection that holds no test
// is refused as NoTests, as Bazel ends a `bazel test` of no tests with
// exit code 4; the control is the same workspace pointed at a project
// with a test.
func TestTestsAreWhatPluginsSayRunTestsAndWhatTheyNeed(t *testing.T) {
	compile := Action{Label: model.Label{Path: "app", Plugin: "go", Name: "compile"}, Declared: &pluginv1.Action{Name: "compile"}}
	tested := Action{Label: model.Label{Path: "app", Plugin: "go", Name: "check"}, Declared: &pluginv1.Action{Name: "check", Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}}, Needs: []model.Label{compile.Label}}
	lint := Action{Label: model.Label{Path: "app", Plugin: "go", Name: "lint"}, Declared: &pluginv1.Action{Name: "lint"}}
	named := Action{Label: model.Label{Path: "docs", Plugin: "mk", Name: "test"}, Declared: &pluginv1.Action{Name: "test"}}
	needed := []Action{compile, tested, lint, named}

	runs, asked, err := Tests(needed, []Action{tested, lint, named}, []string{"app", "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0].Label != tested.Label {
		t.Errorf("asked %v, want the action that runs tests alone", asked)
	}
	if len(runs) != 2 || runs[0].Label != compile.Label || runs[1].Label != tested.Label {
		t.Errorf("runs %v, want the test after what it needs", runs)
	}

	_, _, err = Tests([]Action{lint, named}, []Action{lint, named}, []string{"docs"})
	var none *NoTests
	if !errors.As(err, &none) || !strings.Contains(err.Error(), "nothing docs points at runs tests") {
		t.Errorf("a selection of no tests: %v", err)
	}
	_, _, err = Tests(nil, nil, nil)
	if !errors.As(err, &none) || !strings.Contains(err.Error(), "nothing the build was asked for runs tests") {
		t.Errorf("a workspace of no tests: %v", err)
	}
}
