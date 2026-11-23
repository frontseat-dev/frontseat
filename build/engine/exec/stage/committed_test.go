package stage

import (
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/build/engine/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
)

// What a group reads as committed is given as the repository holds it,
// generated source included, as a check over the repository reads it;
// the control is the same files read by a group that is not, which
// leaves the committed copy of generated source out, the build's own
// being what an action is given of it.
func TestWhatIsReadAsCommittedIsGivenAsCommittedGeneratedSourceIncluded(t *testing.T) {
	snapshot := sourcetest.Taken(t, map[string]string{"gen/api.go": "package gen\n", "main.go": "package main\n"})
	given := func(committed bool) []string {
		t.Helper()
		action := plan.Action{
			Sources:          []pattern.Group{{Patterns: []string{"**"}, Committed: committed}},
			GeneratedSources: []plan.GeneratedSource{{Directory: "gen", Patterns: []string{"**"}}},
			Declared:         &pluginv1.Action{Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}, Command: &pluginv1.Command{Arguments: []string{"true"}}},
		}
		staged, err := Of(t.Context(), snapshot, action, installed(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return staged.Inputs
	}
	if inputs := given(true); !slices.Contains(inputs, "gen/api.go") || !slices.Contains(inputs, "main.go") {
		t.Fatalf("read as committed, given %v", inputs)
	}
	if inputs := given(false); slices.Contains(inputs, "gen/api.go") || !slices.Contains(inputs, "main.go") {
		t.Fatalf("the control: read otherwise, given %v", inputs)
	}
}
