package plan

import (
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// What is handed is placed within the repository: above the project
// where its tool finds it there, never outside.
func TestAHandedPlacementStaysInTheRepository(t *testing.T) {
	action := func(into string) Action {
		return Action{ProjectRoot: "crates/hello", Declared: &pluginv1.Action{Name: "compile"}, Handed: []Handed{{Root: "crates/greet", Patterns: []string{"**"}, Into: into}}}
	}
	if err := CheckHanded(action("../greet")); err != nil {
		t.Fatalf("a placement above the project was refused: %v", err)
	}
	for _, refused := range []string{"../../../out", "/tmp/greet", ""} {
		if err := CheckHanded(action(refused)); err == nil {
			t.Errorf("%q was accepted as a placement", refused)
		}
	}
}
