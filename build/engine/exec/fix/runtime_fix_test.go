package fix_test

import (
	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/build/engine/exec/fix"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	"github.com/frontseat-dev/frontseat/build/engine/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"slices"
	"strings"
	"testing"
)

// A project's directory is one directory however it is spelled: a fix
// may change and create beneath it under each spelling the snapshot
// selects by, as under the canonical one, which is the control.
func TestAFixKeepsItsAuthorityUnderEverySpellingOfItsProject(t *testing.T) {
	for _, project := range []string{"app", "app/.", "./app", "app/"} {
		t.Run(project, func(t *testing.T) {
			snapshot := sourcetest.Taken(t, map[string]string{"app/own.txt": "original"})
			action := plan.Action{Label: model.Label{Path: "app", Plugin: "custom", Name: "format"}, ProjectRoot: project,
				Sources:  []pattern.Group{{Directory: project, Patterns: []string{"*.txt"}}},
				Declared: &pluginv1.Action{Name: "format", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"sh", "-c", "printf changed > own.txt; printf new > new.txt"}}, Rewrites: []string{"*.txt"}}}},
			}
			changeable, err := fix.Changeable(t.Context(), snapshot, action)
			if err != nil || !slices.Equal(changeable, []string{"app/own.txt"}) {
				t.Errorf("accepted root alias lost rewrite authority: %v, %v", changeable, err)
			}
			changes, err := (fix.Runner{Tools: &tools.Provisioner{}, Kept: t.TempDir()}).Run(t.Context(), snapshot, action)
			if err != nil || len(changes) != 2 {
				t.Errorf("fix changes lost at root %q: %+v, %v", project, changes, err)
			}
		})
	}
}

// A test that is a check with a fix is given, in its fix, what its
// Component gives its tests as written.
func TestAFixIsGivenWhatItsTestIsGivenAsWritten(t *testing.T) {
	snapshot := sourcetest.Taken(t, map[string]string{"own.txt": "original"})
	action := plan.Action{Label: model.Label{Plugin: "fixture", Name: "test"}, Sources: []pattern.Group{{Patterns: []string{"*.txt"}}}, Environment: map[string]string{"TEST_REVIEW_VALUE": "configured"},
		Declared: &pluginv1.Action{Name: "test", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"sh", "-c", "printf '%s' \"$TEST_REVIEW_VALUE\" > own.txt"}}, Rewrites: []string{"*.txt"}}}, Command: &pluginv1.Command{Arguments: []string{"true"}}},
	}
	changes, err := (fix.Runner{Tools: &tools.Provisioner{}, Kept: t.TempDir()}).Run(t.Context(), snapshot, action)
	if err != nil || len(changes) != 1 || string(changes[0].Content) != "configured" {
		t.Fatalf("test check's runtime value was dropped by its fix: %+v, %v", changes, err)
	}
}

// What a fix is given as written is held to what its check is: a name
// the fix's own command, the check's command or what runs it sets is
// refused by name, beside a name none of them sets, which is given.
func TestAFixRefusesWhatItIsGivenTwice(t *testing.T) {
	snapshot := sourcetest.Taken(t, map[string]string{"own.txt": "original"})
	for _, tc := range []struct {
		name    string
		given   map[string]string
		refused string
	}{
		{"a name of its own", map[string]string{"TEST_VALUE": "configured"}, ""},
		{"the fix's own", map[string]string{"FROM_FIX": "configured"}, "is given it twice"},
		{"the check's own", map[string]string{"FROM_CHECK": "configured"}, "is given it twice"},
		{"what runs it sets", map[string]string{"HOME": "/elsewhere"}, "sets HOME itself"},
		{"no name of a variable", map[string]string{"NOT A NAME": "x"}, "no name of a variable"},
		// A refusal never repeats what it refused: a name may hold a value.
		{"a name that holds a value", map[string]string{"NAME=v4lue": "x"}, "no name of a variable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			action := plan.Action{Label: model.Label{Plugin: "fixture", Name: "test"}, Sources: []pattern.Group{{Patterns: []string{"*.txt"}}}, Environment: tc.given,
				Declared: &pluginv1.Action{Name: "test", Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"sh", "-c", "printf '%s' \"$TEST_VALUE\" > own.txt"}, Environment: map[string]string{"FROM_FIX": "fix"}}, Rewrites: []string{"*.txt"}}}, Command: &pluginv1.Command{Arguments: []string{"true"}, Environment: map[string]string{"FROM_CHECK": "check"}}},
			}
			changes, err := (fix.Runner{Tools: &tools.Provisioner{}, Kept: t.TempDir()}).Run(t.Context(), snapshot, action)
			if tc.refused == "" {
				if err != nil || len(changes) != 1 || string(changes[0].Content) != "configured" {
					t.Fatalf("a name nothing else sets was not given: %+v, %v", changes, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.refused) {
				t.Fatalf("given %v, the fix ran: %+v, %v; want a refusal saying %q", tc.given, changes, err, tc.refused)
			}
			if strings.Contains(err.Error(), "v4lue") {
				t.Fatalf("the refusal repeats what it refused: %v", err)
			}
		})
	}
}
