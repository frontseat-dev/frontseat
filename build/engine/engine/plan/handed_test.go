package plan

import (
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
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

// What one directory of one repository provides is placed at one place
// however many dependencies it is, as a package of a JavaScript
// workspace is handed its install, its configuration and its siblings'
// sources, each its workspace's root's: their files are of one tree, and
// none replaces another. The control: two directories at one place are
// refused, since one would replace the other.
func TestDependenciesOfOneDirectoryArePlacedTogetherAndOfTwoAreNot(t *testing.T) {
	action := func(second Handed) Action {
		return Action{ProjectRoot: "portal/packages/app", Declared: &pluginv1.Action{Name: "compile"}, Handed: []Handed{
			{Repository: "r", Root: "portal", Patterns: []string{"package.json"}, Into: "../.."},
			second,
		}}
	}
	for _, together := range []Handed{
		{Repository: "r", Root: "portal", Patterns: []string{"packages/plugin/**"}, Into: "../.."},
		{Repository: "r", Root: "portal", Artifacts: []Read{{ProjectRoot: "portal", Output: "node_modules/"}}, Into: "../../"},
		{Repository: "r", Root: "portal/", Patterns: []string{"yarn.lock"}, Into: "../.."},
	} {
		if err := CheckHanded(action(together)); err != nil {
			t.Errorf("two dependencies of one directory at one place were refused: %v", err)
		}
	}
	for _, apart := range []Handed{
		{Repository: "r", Root: "other", Patterns: []string{"**"}, Into: "../.."},
		{Repository: "s", Root: "portal", Patterns: []string{"**"}, Into: "../.."},
	} {
		if err := CheckHanded(action(apart)); err == nil {
			t.Errorf("dependencies of two directories (%s %s) were placed at one place", apart.Repository, apart.Root)
		}
	}
}
