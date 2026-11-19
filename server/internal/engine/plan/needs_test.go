package plan_test

import (
	"errors"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan/plantest"
	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// needing is an action of the project "p", by name, with what it needs.
var needing = plantest.Action

func names(actions []plan.Action) []string {
	var out []string
	for _, a := range actions {
		out = append(out, a.Label.Name)
	}
	return out
}

func TestAnActionComesAfterAllItNeeds(t *testing.T) {
	all := []plan.Action{needing("package", "compile", "notice"), needing("compile", "generate"), needing("generate"), needing("notice"), needing("lint")}
	got := names(plan.With(all, all[:1]))
	want := []string{"generate", "compile", "notice", "package"}
	if len(got) != len(want) {
		t.Fatalf("With = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("With = %v, want %v", got, want)
		}
	}
}

func TestWhatIsNeededTwiceIsThereOnce(t *testing.T) {
	all := []plan.Action{needing("a", "c"), needing("b", "c"), needing("c")}
	if got := names(plan.With(all, all[:2])); len(got) != 3 {
		t.Fatalf("With = %v, want each action once", got)
	}
}

func TestMistakesAreNamed(t *testing.T) {
	for name, c := range map[string]struct {
		actions []plan.Action
		says    string
	}{
		"a need the workspace does not hold": {[]plan.Action{needing("a", "b")}, "//p:x.a reads what //p:x.b leaves, which is not in the workspace"},
		"an action that needs itself":        {[]plan.Action{needing("a", "a")}, "//p:x.a reads what //p:x.a leaves"},
		"a circle of two":                    {[]plan.Action{needing("a", "b"), needing("b", "a")}, "//p:x.a reads what //p:x.b leaves, which reads what //p:x.a leaves"},
		"a circle reached from outside it":   {[]plan.Action{needing("a", "b"), needing("b", "c"), needing("c", "b")}, "//p:x.b reads what //p:x.c leaves, which reads what //p:x.b leaves"},
	} {
		t.Run(name, func(t *testing.T) {
			err := plan.Check(c.actions)
			var mistake *model.Mistake
			if !errors.As(err, &mistake) || mistake.Message != c.says {
				t.Fatalf("Check = %v, want %q", err, c.says)
			}
		})
	}
}

// A circle names the catalog file one of its references was written in,
// whichever action the walk begins at; a circle of plugins' actions
// alone names none.
func TestACircleNamesTheCatalogFileThatWroteIt(t *testing.T) {
	plugin, own := needing("gen", "first"), needing("first", "gen")
	own.References = []plan.Reference{{File: "lab/p/catalog-info.yaml"}}
	for _, actions := range [][]plan.Action{{plugin, own}, {own, plugin}} {
		var mistake *model.Mistake
		if err := plan.Check(actions); !errors.As(err, &mistake) || mistake.File != "lab/p/catalog-info.yaml" {
			t.Errorf("Check = %v naming %q, want the catalog file", err, mistake)
		}
	}
	var mistake *model.Mistake
	if err := plan.Check([]plan.Action{needing("a", "b"), needing("b", "a")}); !errors.As(err, &mistake) || mistake.File != "" {
		t.Errorf("a circle of plugins' actions names %q", mistake)
	}
}

func TestAGraphWithNoMistakePasses(t *testing.T) {
	if err := plan.Check([]plan.Action{needing("a", "b", "c"), needing("b", "c"), needing("c")}); err != nil {
		t.Fatal(err)
	}
}
