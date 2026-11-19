package run

import (
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/schedule"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"testing"
)

// What a catalog file adds to what a test reads changes nothing of when
// the test has nothing to do: that is what its own plugin named one by
// one, resolved here as a run resolves it. A fixture that was not left
// does not skip a test whose program was, and reading the whole of the
// action that builds the program does not hide that it left none.
func TestWhatACatalogAddsToATestsReadsDoesNotChangeWhenItIsIdle(t *testing.T) {
	const program = "_tests/0/exists.test"
	build := plan.Action{Label: testBuild, Repository: "lab", ProjectRoot: "fixtures", Declared: &pluginv1.Action{Name: "test-build", Outputs: []*pluginv1.Output{{Path: program, Optional: true}}}}
	fixtureLabel := model.Label{Path: "lab/runtime", Plugin: "fixture", Name: "prepare"}
	fixture := plan.Action{Label: fixtureLabel, Repository: "lab", ProjectRoot: "runtime", Declared: &pluginv1.Action{Name: "prepare", Outputs: []*pluginv1.Output{{Path: "fixture.dat", Optional: true}}}}
	present := []schedule.Handed{{From: testBuild, Left: map[string]exec.Held{"lab/fixtures/" + program: {}}}}
	for _, tc := range []struct {
		name, reference string
		handed          []schedule.Handed
		idle            bool
	}{
		{"native present control", "", present, false},
		{"native absent control", "", nil, true},
		{"missing runtime data cannot skip a present native test", "//lab/runtime:fixture.dat", present, false},
		{"whole build runtime reference cannot erase native absence", ":go.test-build", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			test := reading("test/exists", program, true)
			// As a plugin said it, before anything is resolved.
			test.Reads, test.IdleWithout = nil, nil
			test.References = []plan.Reference{{Reference: model.Reference{Path: "lab/fixtures", Rest: program, Written: ":" + program}, Plugin: "go", Shared: "lab"}}
			if tc.reference != "" {
				ref, err := model.ParseReference("lab/fixtures", tc.reference)
				if err != nil {
					t.Fatal(err)
				}
				test.References = append(test.References, plan.Reference{Reference: ref, File: "lab/fixtures/catalog-info.yaml"})
			}
			actions := []plan.Action{build, fixture, test}
			if err := plan.Resolve(actions); err != nil {
				t.Fatal(err)
			}
			missing, idle := nothingToDo(labTree, actions[2], tc.handed)
			if idle != tc.idle {
				t.Fatalf("catalog runtime input changed native idle selection: idle=%v want %v (%s), reads=%+v", idle, tc.idle, missing, actions[2].Reads)
			}
		})
	}
}
