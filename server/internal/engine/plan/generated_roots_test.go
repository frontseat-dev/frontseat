package plan

import "github.com/frontseat-dev/frontseat/server/internal/pattern"

import (
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"slices"
	"testing"
)

func TestGeneratedRootAuthorityCannotOverlapChild(t *testing.T) {
	for _, root := range []string{".", "root"} {
		t.Run(root, func(t *testing.T) {
			producer := model.Label{Path: "producer", Plugin: "fixture", Name: "generate"}
			childProducer := model.Label{Path: "other", Plugin: "fixture", Name: "generate"}
			child := "child"
			if root != "." {
				child = "root/child"
			}
			actions := []Action{{Label: producer}, {Label: childProducer},
				{Label: model.Label{Path: root, Plugin: "go", Name: "compile"}, ProjectRoot: root, Sources: []pattern.Group{{Directory: root, Patterns: []string{"**/*.go"}, Generated: []pattern.Selection{{{"**/*.go"}}}}}},
				{Label: model.Label{Path: child, Plugin: "go", Name: "compile"}, ProjectRoot: child, Sources: []pattern.Group{{Directory: child, Patterns: []string{"**/*.go"}, Generated: []pattern.Selection{{{"**/*.go"}}}}}},
			}
			claims := []GeneratedSource{{Producer: producer, Directory: root, Output: "out/", Patterns: []string{"**/*.go"}}, {Producer: childProducer, Directory: child, Output: "out/", Patterns: []string{"**/*.go"}}}
			if err := BindGenerated(actions, claims); err == nil {
				t.Fatal("accepted overlapping root and child generated authority")
			}
		})
	}
	t.Run("separate directories control", func(t *testing.T) {
		a := model.Label{Path: "p", Plugin: "x", Name: "a"}
		b := model.Label{Path: "p", Plugin: "x", Name: "b"}
		actions := []Action{{Label: a}, {Label: b}, {Label: model.Label{Path: "one", Plugin: "go", Name: "compile"}, ProjectRoot: "one", Sources: []pattern.Group{{Directory: "one", Patterns: []string{"**/*.go"}, Generated: []pattern.Selection{{{"**/*.go"}}}}}}, {Label: model.Label{Path: "two", Plugin: "go", Name: "compile"}, ProjectRoot: "two", Sources: []pattern.Group{{Directory: "two", Patterns: []string{"**/*.go"}, Generated: []pattern.Selection{{{"**/*.go"}}}}}}}
		if err := BindGenerated(actions, []GeneratedSource{{Producer: a, Directory: "one", Output: "out/", Patterns: []string{"**/*.go"}}, {Producer: b, Directory: "two", Output: "out/", Patterns: []string{"**/*.go"}}}); err != nil {
			t.Fatal(err)
		}
	})
}

// A claim beneath a module's root binds to that module and to what is
// handed it, its files placed where the claim is in it; a module nearer
// the repository's root that also holds it, and a neighbour whose name
// begins as the module's, are not bound; a claim that only holds a root,
// never beneath one, binds nothing.
func TestAGeneratedClaimBindsBeneathTheDeepestRootThatHoldsIt(t *testing.T) {
	producer := model.Label{Path: "api", Plugin: "buf", Name: "generate/lib"}
	selected := []pattern.Selection{{{"**/*.go"}}}
	module := func(root string) Action {
		return Action{Label: model.Label{Path: root, Plugin: "go", Name: "compile"}, ProjectRoot: root, Sources: []pattern.Group{{Directory: root, Patterns: []string{"**/*.go"}, Generated: selected}}}
	}
	actions := []Action{{Label: producer}, module("."), module("lib"), module("libx"),
		{Label: model.Label{Path: "app", Plugin: "go", Name: "compile"}, ProjectRoot: "app", Handed: []Handed{{Root: "lib", Into: ".frontseat/go/lib", Patterns: []string{"**/*.go"}, Generated: selected}}}}
	claim := GeneratedSource{Producer: producer, ProjectRoot: "api", Directory: "lib/gen", Output: ".gen-lib/lib/gen/", Patterns: []string{"**/*.pb.go"}}
	if err := BindGenerated(actions, []GeneratedSource{claim}); err != nil {
		t.Fatal(err)
	}
	for _, each := range actions[1:] {
		bound := len(each.Artifacts) > 0
		switch each.Label.Path {
		case "lib":
			if !bound || each.Artifacts[0].Within != "gen" || each.Artifacts[0].Into != "." || !slices.Contains(each.Needs, producer) {
				t.Errorf("lib is bound %+v, needs %v", each.Artifacts, each.Needs)
			}
		case "app":
			if !bound || each.Artifacts[0].Within != "gen" || each.Artifacts[0].Into != ".frontseat/go/lib" {
				t.Errorf("app is bound %+v", each.Artifacts)
			}
		default:
			if bound || slices.Contains(each.Needs, producer) {
				t.Errorf("%s is bound %+v, needs %v", each.Label, each.Artifacts, each.Needs)
			}
		}
	}
	// A root beneath the claim does not hold it.
	inner := []Action{{Label: producer}, module("lib/gen/inner")}
	if err := BindGenerated(inner, []GeneratedSource{claim}); err == nil {
		t.Fatal("a claim bound to a root beneath it")
	}
	// Where a root is the claim's directory, it alone is bound, as before.
	exact := []Action{{Label: producer}, module("lib"), module("lib/gen")}
	if err := BindGenerated(exact, []GeneratedSource{claim}); err != nil {
		t.Fatal(err)
	}
	if len(exact[1].Artifacts) != 0 || len(exact[2].Artifacts) != 1 || exact[2].Artifacts[0].Within != "" {
		t.Fatalf("lib %+v, lib/gen %+v", exact[1].Artifacts, exact[2].Artifacts)
	}
}
