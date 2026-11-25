package stage

import (
	"github.com/frontseat-dev/frontseat/build/engine/pattern"
	"path"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
)

func TestArtifactOnlyHandoffCopiesNoSourceAndCannotReplaceReadSource(t *testing.T) {
	snapshot := sourcetest.Taken(t, map[string]string{"app/package.json": "source", "app/modules/committed.js": "source"})
	for _, readsModules := range []bool{false, true} {
		action := plan.Action{ProjectRoot: "app", Sources: []pattern.Group{{Directory: "app", Patterns: []string{"package.json"}}}, Handed: []plan.Handed{{Root: "app", Into: ".", Artifacts: []plan.Read{{ProjectRoot: "app", Output: "modules/"}}}}, Declared: &pluginv1.Action{Command: &pluginv1.Command{Arguments: []string{"true"}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}}}
		if readsModules {
			action.Sources[0].Patterns = append(action.Sources[0].Patterns, "modules/**")
		}
		staged, err := Of(t.Context(), snapshot, action, installed(), nil, map[string]exec.Held{"app/modules": {Directory: true, Digest: exec.Digest{Hash: strings.Repeat("1", 64), Size: 1}}})
		if readsModules {
			if err == nil || !strings.Contains(err.Error(), "also a file of the repository") {
				t.Fatalf("source collision: %v", err)
			}
		} else if err != nil || len(staged.Copies) != 0 {
			t.Fatalf("artifact-only handoff copied source: %v, %+v", err, staged.Copies)
		}
	}
}

// An action handed something where a file of its repository is says so
// by where that file is in the workspace, which a person reads: the
// repository's folder and the file's path in it, not the place it sits
// at in the action's root, where its repository is at the top. The
// control is the same in a checkout of its own, whose folder is ".".
func TestWhatIsHandedWhereAFileIsSaysTheFileByItsPlaceInTheWorkspace(t *testing.T) {
	for _, c := range []struct{ folder, want string }{
		{"repos/app", "is handed repos/app/app/modules, which is also a file"},
		{".", "is handed app/modules, which is also a file"},
	} {
		files := map[string]string{path.Join(c.folder, "app/package.json"): "source", path.Join(c.folder, "app/modules/committed.js"): "source"}
		snapshot := sourcetest.Workspace(t, map[string]string{"app": c.folder}, files)
		action := plan.Action{Repository: "app", ProjectRoot: "app", Sources: []pattern.Group{{Directory: "app", Patterns: []string{"package.json", "modules/**"}}}, Handed: []plan.Handed{{Root: "app", Into: ".", Artifacts: []plan.Read{{ProjectRoot: "app", Output: "modules/"}}}}, Declared: &pluginv1.Action{Command: &pluginv1.Command{Arguments: []string{"true"}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}}}
		_, err := Of(t.Context(), snapshot, action, installed(), nil, map[string]exec.Held{path.Join(c.folder, "app/modules"): {Directory: true, Digest: exec.Digest{Hash: strings.Repeat("1", 64), Size: 1}}})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("in %s: %v; want it to say %q", c.folder, err, c.want)
		}
	}
}
