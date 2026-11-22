package run

import (
	"context"
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/schedule"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
)

type listing struct {
	exec.Executor
	files map[string]exec.Held
}

func (l *listing) List(context.Context, exec.Held) (map[string]exec.Held, error) {
	return l.files, nil
}

// everyFile is every file an action is handed, by where it is placed.
func everyFile(handed []schedule.Handed) map[string]exec.Held {
	files := map[string]exec.Held{}
	for _, each := range handed {
		for at, held := range each.Left {
			files[at] = held
		}
	}
	return files
}

func TestAGeneratedHandoffSelectsAndRemapsOnlyDeclaredFiles(t *testing.T) {
	producer := model.Label{Path: "api", Plugin: "buf", Name: "generate"}
	for _, tc := range []struct {
		name, repository, root, into string
		standalone                   bool
	}{
		{"own module", "", "api/gen/go", ".", true},
		{"transitive across repository", "apprepo", "app", ".deps/example/module", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := exec.Held{Digest: exec.Digest{Hash: "selected", Size: 1}, Executable: true}
			grid := &listing{files: map[string]exec.Held{"pkg/a.pb.go": selected, "pkg/a_test.pb.go": {Digest: exec.Digest{Hash: "test", Size: 1}}}}
			runner := Runner{Answers: &exec.Remembering{Executor: grid}}
			claim := plan.GeneratedSource{ProjectRoot: "api", Directory: "api/gen/go", Output: "out/", Producer: producer, Patterns: []string{"**/*.pb.go"}}
			action := plan.Action{Repository: tc.repository, ProjectRoot: tc.root, ArtifactOnly: []model.Label{producer}, Artifacts: []plan.ArtifactInput{{Source: claim, Into: tc.into, Patterns: []string{"**/*.go", "!**/*_test.pb.go"}, Generated: []pattern.Selection{{{"**/*.go"}}}}}}
			handed := []schedule.Handed{{From: producer, Left: map[string]exec.Held{"api/out": {Directory: true, Digest: exec.Digest{Hash: "tree", Size: 1}}, "api/secret": {Digest: exec.Digest{Hash: "other", Size: 1}}}}}
			got, err := runner.generated(t.Context(), source.Tree{Paths: map[string]string{"": ".", "apprepo": "checkout"}}, action, handed)
			if err != nil {
				t.Fatal(err)
			}
			files := everyFile(got)
			if len(files) != 1 {
				t.Fatalf("extra transferred output: %v", files)
			}
			want := "api/gen/go/pkg/a.pb.go"
			if tc.repository != "" {
				want = "checkout/app/.deps/example/module/pkg/a.pb.go"
			}
			if files[want] != selected {
				t.Fatalf("output not remapped with its mode: %v", files)
			}
		})
	}
}

// An action that names one output of a producer, and whose inputs also
// select that producer's generated source, is handed both: the output it
// named, and the generated files, found in all the producer left. What
// it named nothing of is not handed. The control is the same action
// naming nothing: it is handed the generated files alone.
func TestNamingOneOutputOfAProducerHidesNoneOfItsGeneratedSource(t *testing.T) {
	producer := model.Label{Path: "api", Plugin: "buf", Name: "generate"}
	generated := exec.Held{Digest: exec.Digest{Hash: "generated", Size: 1}}
	report := exec.Held{Directory: true, Digest: exec.Digest{Hash: "report", Size: 1}}
	grid := &listing{files: map[string]exec.Held{"pkg/a.pb.go": generated}}
	runner := Runner{Answers: &exec.Remembering{Executor: grid}}
	claim := plan.GeneratedSource{ProjectRoot: "api", Directory: "api/gen/go", Output: "out/", Producer: producer, Patterns: []string{"**/*.pb.go"}}
	binding := plan.ArtifactInput{Source: claim, Into: ".", Patterns: []string{"**/*.go"}, Generated: []pattern.Selection{{{"**/*.go"}}}}
	handed := []schedule.Handed{{From: producer, Left: map[string]exec.Held{
		"api/out":    {Directory: true, Digest: exec.Digest{Hash: "tree", Size: 1}},
		"api/report": report,
		"api/secret": {Digest: exec.Digest{Hash: "other", Size: 1}},
	}}}
	tree := source.Tree{Paths: map[string]string{"": "."}}
	for name, c := range map[string]struct {
		action plan.Action
		want   map[string]exec.Held
	}{
		"one output named beside the binding": {
			plan.Action{ProjectRoot: "api/gen/go", Artifacts: []plan.ArtifactInput{binding}, Needs: []model.Label{producer},
				Reads: []plan.Read{{From: producer, ProjectRoot: "api", Output: "report/"}}},
			map[string]exec.Held{"api/gen/go/pkg/a.pb.go": generated, "api/report": report},
		},
		"nothing named": {
			plan.Action{ProjectRoot: "api/gen/go", Artifacts: []plan.ArtifactInput{binding}, Needs: []model.Label{producer}, ArtifactOnly: []model.Label{producer}},
			map[string]exec.Held{"api/gen/go/pkg/a.pb.go": generated},
		},
	} {
		got, err := runner.generated(t.Context(), tree, c.action, handed)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		files := everyFile(got)
		if len(files) != len(c.want) {
			t.Errorf("%s: handed %v, want %v", name, files, c.want)
		}
		for at, held := range c.want {
			if files[at] != held {
				t.Errorf("%s: %s is %v, want %v", name, at, files[at], held)
			}
		}
	}
}

// What an action names of what another left is all it is handed of it:
// one output where it named one, nothing where that was not left, and
// all of it where it named the action.
func TestAnActionIsHandedWhatItNamedAndNothingElse(t *testing.T) {
	producer := model.Label{Path: "lab/a", Plugin: "custom", Name: "both"}
	other := model.Label{Path: "lab/b", Plugin: "custom", Name: "write"}
	made, spare := exec.Held{Digest: exec.Digest{Hash: "made", Size: 1}}, exec.Held{Digest: exec.Digest{Hash: "spare", Size: 1}}
	handed := []schedule.Handed{
		{From: producer, Left: map[string]exec.Held{"lab/a/made": made, "lab/a/spare": spare}},
		{From: other, Left: map[string]exec.Held{"lab/b/made": made}},
	}
	tree := source.Tree{Paths: map[string]string{"lab": "lab"}}
	read := func(output string) plan.Read {
		return plan.Read{From: producer, Repository: "lab", ProjectRoot: "a", Output: output}
	}
	for name, c := range map[string]struct {
		reads []plan.Read
		want  []string
	}{
		"one output":                 {[]plan.Read{read("made/")}, []string{"lab/a/made", "lab/b/made"}},
		"two outputs":                {[]plan.Read{read("made/"), read("spare/")}, []string{"lab/a/made", "lab/a/spare", "lab/b/made"}},
		"the action":                 {[]plan.Read{read("")}, []string{"lab/a/made", "lab/a/spare", "lab/b/made"}},
		"an output it did not leave": {[]plan.Read{read("absent/")}, []string{"lab/b/made"}},
	} {
		var got []string
		for _, each := range selected(tree, plan.Action{Reads: c.reads}, handed) {
			for at := range each.Left {
				got = append(got, at)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: handed %v, want %v", name, got, c.want)
		}
	}
}

// A claim bound beneath a module's root hands its files where the claim is
// in the module, and selects them by the module's patterns from there: an
// exclusion of the module's that names the claim's place holds, beside the
// file it does not exclude.
func TestAClaimBeneathAModuleIsPlacedWhereItIsInTheModule(t *testing.T) {
	producer := model.Label{Path: "api", Plugin: "buf", Name: "generate-lib"}
	kept := exec.Held{Digest: exec.Digest{Hash: "kept", Size: 1}}
	grid := &listing{files: map[string]exec.Held{"hello/v1/hello.pb.go": kept, "skip/v1/skip.pb.go": {Digest: exec.Digest{Hash: "skipped", Size: 1}}}}
	runner := Runner{Answers: &exec.Remembering{Executor: grid}}
	claim := plan.GeneratedSource{ProjectRoot: "api", Directory: "lib/gen", Output: ".gen-lib/lib/gen/", Producer: producer, Patterns: []string{"**/*.pb.go"}}
	action := plan.Action{ProjectRoot: "lib", ArtifactOnly: []model.Label{producer}, Artifacts: []plan.ArtifactInput{{Source: claim, Into: ".", Within: "gen", Patterns: []string{"**/*.go", "!gen/skip/**"}, Generated: []pattern.Selection{{{"**/*.go"}}}}}}
	handed := []schedule.Handed{{From: producer, Left: map[string]exec.Held{"api/.gen-lib/lib/gen": {Directory: true, Digest: exec.Digest{Hash: "tree", Size: 1}}}}}
	got, err := runner.generated(t.Context(), source.Tree{Paths: map[string]string{"": "."}}, action, handed)
	if err != nil {
		t.Fatal(err)
	}
	files := everyFile(got)
	if len(files) != 1 || files["lib/gen/hello/v1/hello.pb.go"] != kept {
		t.Fatalf("handed %v", files)
	}
}
