package stage

import (
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source/sourcetest"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// A workspace file is staged where it is, as an input of the action; one
// the repository does not hold is refused by name. A file arranged above
// the project is written there, over a file of the repository the action
// does not read, but never in place of a workspace file it reads.
func TestWorkspaceFilesAndArrangedFilesAboveTheProject(t *testing.T) {
	snapshot := sourcetest.Taken(t, map[string]string{"Cargo.toml": "[workspace]", "Cargo.lock": "lock", "crates/hello/Cargo.toml": "[package]", "crates/hello/src/main.rs": "fn main() {}"})
	provisioner := installed()
	action := func(workspaceFiles []string, arranged ...string) plan.Action {
		a := plan.Action{
			ProjectRoot:    "crates/hello",
			Sources:        []pattern.Group{{Directory: "crates/hello", Patterns: []string{"**"}}},
			WorkspaceFiles: workspaceFiles,
			Declared:       &pluginv1.Action{Name: "compile", Command: &pluginv1.Command{Arguments: []string{"cargo", "build"}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
		}
		for _, path := range arranged {
			a.Arranged = append(a.Arranged, plan.Arranged{Path: path, Content: []byte("narrowed")})
		}
		return a
	}
	staged, err := Of(t.Context(), snapshot, action([]string{"Cargo.lock"}, "Cargo.toml"), provisioner, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(staged.Inputs, "Cargo.lock") || slices.Contains(staged.Inputs, "Cargo.toml") {
		t.Errorf("the workspace file is not an input, or the arranged file is: %v", staged.Inputs)
	}
	if _, written := staged.Written["Cargo.toml"]; !written {
		t.Errorf("the workspace manifest was not written above the project: %v", staged.Written)
	}
	if _, err := Of(t.Context(), snapshot, action([]string{"Cargo.lock", "rust-toolchain.toml"}), provisioner, nil, nil); err == nil || !strings.Contains(err.Error(), "rust-toolchain.toml") {
		t.Errorf("a workspace file the repository lacks was not refused by name: %v", err)
	}
	if _, err := Of(t.Context(), snapshot, action([]string{"Cargo.lock"}, "Cargo.lock"), provisioner, nil, nil); err == nil || !strings.Contains(err.Error(), "Cargo.lock") {
		t.Errorf("an arranged file in place of a workspace file the action reads was not refused by name: %v", err)
	}
}
