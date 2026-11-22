package fix_test

import (
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/server/internal/exec/fix"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

func TestAdditionalSourceGroupsDoNotExtendRewriteAuthority(t *testing.T) {
	snapshot := sourcetest.Taken(t, map[string]string{"fixture.txt": "original", "app/own.txt": "original"})
	action := plan.Action{Label: model.Label{Path: "app", Plugin: "custom", Name: "format"}, ProjectRoot: "app",
		Sources: []pattern.Group{{Directory: "app", Patterns: []string{"*.txt"}}, {Patterns: []string{"fixture.txt"}}},
		Declared: &pluginv1.Action{Name: "format", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{
			Arguments: []string{"sh", "-c", "printf changed > own.txt; printf changed > ../fixture.txt"},
		}, Rewrites: []string{"**"}}}},
	}
	changeable, err := fix.Changeable(t.Context(), snapshot, action)
	if err != nil || !slices.Equal(changeable, []string{"app/own.txt"}) {
		t.Fatalf("readable parent data became writable: %v, %v", changeable, err)
	}
	changes, err := (fix.Runner{Tools: &tools.Provisioner{}, Kept: t.TempDir()}).Run(t.Context(), snapshot, action)
	if err != nil || len(changes) != 1 || changes[0].Path != "app/own.txt" || string(changes[0].Content) != "changed" {
		t.Fatalf("fix changed outside its project or lost the permitted change: %+v, %v", changes, err)
	}
}
