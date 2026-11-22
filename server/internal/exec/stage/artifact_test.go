package stage

import (
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
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
