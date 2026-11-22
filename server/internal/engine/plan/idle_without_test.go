package plan

import (
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// What an action has nothing to do without is each output its own plugin
// named one by one, said by Resolve from its references: also where the
// plugin named the whole of the same action beside it, which folds the
// output out of what is read; never what a catalog file named, and never
// for an action that does not say it has nothing to do without its
// inputs. Resolved again from other references, it is said again.
func TestResolveSaysWhatAnActionHasNothingToDoWithout(t *testing.T) {
	const program = "_tests/0/exists.test"
	build := Action{Label: model.Label{Path: "lab/app", Plugin: "go", Name: "test-build"}, Repository: "lab", ProjectRoot: "app",
		Declared: &pluginv1.Action{Name: "test-build", Outputs: []*pluginv1.Output{{Path: program, Optional: true}}}}
	exact := Reference{Reference: model.Reference{Path: "lab/app", Rest: program, Written: ":" + program}, Plugin: "go", Shared: "lab"}
	whole := Reference{Reference: model.Reference{Path: "lab/app", Rest: "test-build", Written: ":test-build"}, Plugin: "go", Shared: "lab"}
	written := Reference{Reference: model.Reference{Path: "lab/app", Rest: program, Written: ":" + program}, File: "lab/app/catalog-info.yaml"}
	want := Read{From: build.Label, Repository: "lab", ProjectRoot: "app", Output: program}
	for _, tc := range []struct {
		name       string
		idle       bool
		references []Reference
		without    []Read
	}{
		{"its plugin's output", true, []Reference{exact}, []Read{want}},
		{"named twice, said once", true, []Reference{exact, exact}, []Read{want}},
		{"beside the whole action, which is all that is read", true, []Reference{exact, whole}, []Read{want}},
		{"the whole action alone", true, []Reference{whole}, nil},
		{"a catalog file's output", true, []Reference{written}, nil},
		{"an action that does not say so", false, []Reference{exact}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := Action{Label: model.Label{Path: "lab/app", Plugin: "go", Name: "test/exists"}, Repository: "lab", ProjectRoot: "app",
				Declared: &pluginv1.Action{Name: "test/exists", Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{IdleWithoutInputs: tc.idle}}}, References: tc.references}
			actions := []Action{build, reader}
			if err := Resolve(actions); err != nil {
				t.Fatal(err)
			}
			if got := actions[1].IdleWithout; len(got) != len(tc.without) || (len(got) == 1 && got[0] != tc.without[0]) {
				t.Fatalf("it has nothing to do without %+v, want %+v", got, tc.without)
			}
			// Resolved again from the whole action alone, nothing is left over.
			actions[1].References = []Reference{whole}
			if err := Resolve(actions); err != nil {
				t.Fatal(err)
			}
			if got := actions[1].IdleWithout; len(got) != 0 {
				t.Fatalf("resolved again, it still has nothing to do without %+v", got)
			}
		})
	}
}
