package run

import (
	"reflect"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/schedule"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
)

func TestProvidedArtifactsTransferOnlyTheSelectedDigestAndKeepExplicitReads(t *testing.T) {
	producer := model.Label{Path: "producer/root", Plugin: "prepare", Name: "install"}
	artifact := plan.Read{From: producer, Repository: "producer", ProjectRoot: "root", Output: "modules/"}
	modules := exec.Held{Directory: true, Digest: exec.Digest{Hash: "immutable-tree", Size: 1}}
	program := exec.Held{Executable: true, Digest: exec.Digest{Hash: "executable", Size: 2}}
	handed := []schedule.Handed{{From: producer, Left: map[string]exec.Held{"first/root/modules": modules, "first/root/program": program, ".frontseat/home/private": {Digest: exec.Digest{Hash: "not-exported"}}}}}
	tree := source.Tree{Paths: map[string]string{"producer": "first", "consumer": "second"}}
	for _, explicit := range []bool{false, true} {
		action := plan.Action{Label: model.Label{Path: "consumer/app", Plugin: "run", Name: "test"}, Repository: "consumer", ProjectRoot: "app", Handed: []plan.Handed{{Into: ".deps", Artifacts: []plan.Read{artifact}}}}
		want := map[string]exec.Held{"second/app/.deps/modules": modules}
		if explicit {
			action.Reads = []plan.Read{{From: producer, Repository: "producer", ProjectRoot: "root", Output: "program"}}
			action.Needs = []model.Label{producer}
			want["first/root/program"] = program
		}
		actions := []plan.Action{{Label: producer, Declared: &pluginv1.Action{Name: "install"}}, action}
		if err := plan.BindProvided(actions); err != nil {
			t.Fatal(err)
		}
		if len(actions[1].Needs) != 1 || actions[1].Needs[0] != producer {
			t.Fatal("provider was not scheduled once")
		}
		got, err := (Runner{}).generated(t.Context(), tree, actions[1], handed)
		if err != nil {
			t.Fatal(err)
		}
		all := map[string]exec.Held{}
		for _, each := range got {
			for at, held := range each.Left {
				if _, duplicate := all[at]; duplicate {
					t.Fatalf("duplicate %s", at)
				}
				all[at] = held
			}
		}
		if !reflect.DeepEqual(all, want) {
			t.Fatalf("explicit=%t: got %v, want %v", explicit, all, want)
		}
		if _, err := (Runner{}).generated(t.Context(), tree, actions[1], nil); err == nil {
			t.Fatal("missing required output accepted")
		}
		actions[1].Handed[0].Artifacts[0].Optional = true
		absent, err := (Runner{}).generated(t.Context(), tree, actions[1], nil)
		if err != nil || len(absent) != 0 {
			t.Fatalf("optional absence changed: %v, %v", absent, err)
		}
	}
}
