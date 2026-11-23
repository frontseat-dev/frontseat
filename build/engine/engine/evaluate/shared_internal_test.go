package evaluate

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/graph"
	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// A shared fetch is labelled with its repository's directory, which an
// entity at the repository's root owns. Needing the fetch makes no
// entity depend on that one: what an entity depends on is what its
// projects require. The control: the same need, of an action that is not
// shared, does.
func TestNeedingASharedFetchDependsOnNoEntity(t *testing.T) {
	root := record.Located{Ref: entity.New("component", "default", "root"), Repository: "lab"}
	tool := record.Located{Ref: entity.New("component", "default", "tool"), Repository: "lab", Root: "tool"}
	for _, shared := range []bool{false, true} {
		fetch := plan.Action{
			Label:    model.Label{Path: "lab", Plugin: "go", Name: "fetch/example.com/dep@v1.0.0"},
			Declared: &pluginv1.Action{Name: "fetch/example.com/dep@v1.0.0", Kind: &pluginv1.Action_Fetch{Fetch: &pluginv1.Fetch{}}},
		}
		if shared {
			fetch.Declared.Kind = &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{}}
		}
		compile := plan.Action{
			Label:    model.Label{Path: "lab/tool", Plugin: "go", Name: "compile"},
			Declared: &pluginv1.Action{Name: "compile"},
			Needs:    []model.Label{fetch.Label},
		}
		on := dependsOn(&graph.Graph{}, nil, []record.Located{root, tool}, []plan.Action{fetch, compile})
		if depends := len(on[tool.Ref]) > 0; depends == shared {
			t.Errorf("shared %v: tool depends on %v", shared, on[tool.Ref])
		}
	}
}

// Two projects of a repository that declare one shared fetch alike share
// it; two that declare it differently are refused, and both are named.
func TestASharedFetchIsTakenOnceAndOnlyAlike(t *testing.T) {
	declared := func(sum string) *pluginv1.Action {
		return &pluginv1.Action{Name: "fetch/example.com/dep@v1.0.0", Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{Files: []*pluginv1.File{{Path: "go.sum", Content: []byte(sum)}}}}}
	}
	var out Evaluation
	for _, root := range []string{"tool", "tool/kit"} {
		if err := out.share("lab", "go", root, declared("a")); err != nil {
			t.Fatalf("declared alike by %s: %v", root, err)
		}
	}
	if len(out.Actions) != 1 || out.Actions[0].Label.String() != "//lab:go.fetch/example.com/dep@v1.0.0" {
		t.Fatalf("taken as %v, want one action of the repository", out.Actions)
	}
	err := out.share("lab", "go", "other", declared("b"))
	if err == nil || !contains(err.Error(), `"lab/other"`, `"lab/tool"`) {
		t.Errorf("declared differently: %v, want both projects named", err)
	}
}

// A shared fetch's directory is where the repository's tool pins are
// staged; a file of its own there is refused, and one beside them is not.
func TestASharedFetchIsGivenNoFileWhereThePinsAre(t *testing.T) {
	for _, at := range []string{"mise.toml", "./mise.lock", "mise.toml.d/go.sum"} {
		var out Evaluation
		err := out.share("lab", "go", "tool", &pluginv1.Action{Name: "fetch/a.com/x@v1.0.0", Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{Files: []*pluginv1.File{{Path: at}}}}})
		if beside := at == "mise.toml.d/go.sum"; beside != (err == nil) {
			t.Errorf("a file at %s: %v", at, err)
		}
	}
}

func contains(s string, all ...string) bool {
	for _, each := range all {
		if !strings.Contains(s, each) {
			return false
		}
	}
	return true
}
