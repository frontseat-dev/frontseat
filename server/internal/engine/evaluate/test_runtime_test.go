package evaluate

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"google.golang.org/protobuf/proto"
)

func TestRuntimeDataFollowsComponentOwnershipAndKeepsItsLiteralBase(t *testing.T) {
	owner := entity.New("component", "default", "parent")
	nested := entity.New("component", "default", "nested")
	declaration := config.TestRuntime{Entity: owner, Directory: "parent[1]", File: "repo/parent[1]/catalog-info.yaml",
		Inputs: []string{"fixtures/**"}, Needs: []string{":custom.prepare"}, Tools: []string{"node", "node"}, Environment: map[string]string{"FIXTURE": "value"}}
	base := &pluginv1.Action{Name: "test/cases@1of2", Test: true, Command: &pluginv1.Command{Arguments: []string{"native", "selection"}, Directory: "cases"}}
	before := proto.Clone(base)
	makeAction := func(root, plugin string, declared *pluginv1.Action) plan.Action {
		return plan.Action{Repository: "repo", ProjectRoot: root, Label: model.Label{Path: "repo/" + root, Plugin: plugin, Name: declared.GetName()}, Declared: declared,
			Sources: []pattern.Group{{Directory: root, Patterns: []string{"testdata/**"}}}}
	}
	out := Evaluation{Entities: []record.Located{{Repository: "repo", Root: "parent[1]", Ref: owner}, {Repository: "repo", Root: "parent[1]/nested", Ref: nested}}, Actions: []plan.Action{
		makeAction("parent[1]/go", "go", base), makeAction("parent[1]/js", "jest", base),
		makeAction("parent[1]/nested", "go", base), makeAction("parent1", "go", base),
		makeAction("parent[1]/go", "go", &pluginv1.Action{Name: "compile", Command: base.Command}),
	}}
	if err := testRuntimes(&out, map[string][]config.TestRuntime{"repo": {declaration}}, map[string]map[string]string{"repo": {"node": "locked"}}); err != nil {
		t.Fatal(err)
	}
	for i, action := range out.Actions {
		if i > 1 {
			if len(action.Sources) != 1 || len(action.References) != 0 || len(action.Environment) != 0 || len(action.Declared.GetTools()) != 0 {
				t.Fatalf("runtime data escaped its Component or test: %s", action.Label)
			}
			continue
		}
		if len(action.Sources) != 2 || action.Sources[1].Directory != "parent[1]" || len(action.Sources[1].Generated) != 0 {
			t.Fatalf("source base or generated acceptance changed: %+v", action.Sources)
		}
		ref := action.References[0]
		if ref.Path != "repo/parent[1]" || ref.Plugin != "" || ref.File != declaration.File {
			t.Fatalf("reference lost catalog scope or provenance: %+v", ref)
		}
		if !proto.Equal(action.Declared.GetCommand(), base.Command) || !slices.Equal(action.Declared.Tools, []string{"node"}) || !reflect.DeepEqual(action.Environment, declaration.Environment) {
			t.Fatalf("test command or runtime data changed: %+v", action)
		}
	}
	if !proto.Equal(base, before) {
		t.Fatal("cached plugin declaration was modified")
	}
}

// Four hundred atoms share forty Component settings. Each evaluation starts
// from the immutable plugin declarations, just as a warm analysis does.
func BenchmarkTestRuntimeInputs(b *testing.B) {
	var base Evaluation
	configured := map[string][]config.TestRuntime{"repo": {}}
	pinned := map[string]map[string]string{"repo": {"go": "locked"}}
	for i := range 40 {
		root := "component" + strconv.Itoa(i)
		owner := entity.New("component", "default", root)
		base.Entities = append(base.Entities, record.Located{Repository: "repo", Root: root, Ref: owner})
		configured["repo"] = append(configured["repo"], config.TestRuntime{
			Entity: owner, Directory: root, File: "repo/" + root + "/catalog-info.yaml",
			Inputs: []string{"fixtures/**"}, Needs: []string{":data.txt"}, Tools: []string{"go"}, Environment: map[string]string{"DATA": "../data.txt"},
		})
		for j := range 10 {
			base.Actions = append(base.Actions, plan.Action{Repository: "repo", ProjectRoot: root,
				Label:    model.Label{Path: "repo/" + root, Plugin: "go", Name: "test/p" + strconv.Itoa(j)},
				Declared: &pluginv1.Action{Test: true, Command: &pluginv1.Command{Arguments: []string{"test-program"}}},
				Sources:  []pattern.Group{{Directory: root, Patterns: []string{"testdata/**"}}},
			})
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		out := Evaluation{Entities: base.Entities, Actions: slices.Clone(base.Actions)}
		if err := testRuntimes(&out, configured, pinned); err != nil {
			b.Fatal(err)
		}
	}
}

// Measured on 2026-11-15, Apple M4 Max: 253631–264960 ns/op,
// 505868–505915 B/op and 4866 allocs/op over three samples of:
// mise exec -- go test ./server/internal/engine/evaluate -run '^$' -bench '^BenchmarkTestRuntimeInputs$' -benchmem -count=3
// Time allows scheduling variation; allocations allow less than 7 percent.
func TestBudgetOfTestRuntimeInputs(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 3 * time.Millisecond, Allocs: 5200}, BenchmarkTestRuntimeInputs)
}

func TestRuntimeDataRefusesUnknownOwnershipInputsAndTools(t *testing.T) {
	owner := entity.New("component", "default", "app")
	for _, tc := range []struct{ name, input, tool, want string }{
		{"valid", "data/**", "node", ""}, {"path escape", "../outside", "", "input"},
		{"malformed reference", "://other:out", "", "input"}, {"missing tool", "", "not-pinned", "mise.toml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := Evaluation{Entities: []record.Located{{Root: "app", Ref: owner}}}
			written := config.TestRuntime{Entity: owner, Directory: "app", File: "app/catalog-info.yaml"}
			if tc.input != "" {
				written.Inputs = []string{tc.input}
			}
			if tc.tool != "" {
				written.Tools = []string{tc.tool}
			}
			err := testRuntimes(&out, map[string][]config.TestRuntime{"": {written}}, map[string]map[string]string{"": {"node": "locked"}})
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("runtime configuration: %v, want %q", err, tc.want)
			}
		})
	}
}
