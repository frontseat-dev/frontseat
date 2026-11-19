package evaluate

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"google.golang.org/protobuf/proto"
)

func TestRetainedOwnedCallerCannotChangeALaterResult(t *testing.T) {
	mutations := map[string]func(*testing.T, retainedResult){
		"declared action": func(t *testing.T, r retainedResult) { r.actions[0].Declared.Name = "changed-by-caller" },
		"project":         func(t *testing.T, r retainedResult) { r.evaluation.Projects[0].Name = "changed-by-caller" },
		"option": func(t *testing.T, r retainedResult) {
			if len(r.evaluation.Projects[0].Options) == 0 {
				t.Fatal("fixture needs options")
			}
			value := r.evaluation.Projects[0].Options[0].Value
			before := proto.CloneOf(value)
			// The baseline even aliases a plugin default. Restore the test's
			// mutation so subsequent controls fail only for their own case.
			t.Cleanup(func() { proto.Reset(value); proto.Merge(value, before) })
			value.Value = &typev1.OptionValue_StringValue{StringValue: "changed-by-caller"}
		},
		"provider graph": func(t *testing.T, r retainedResult) {
			coordinate := r.evaluation.Projects[0].Provides[0].Coordinate
			project, ok := r.evaluation.Graph.ProvidedBy(coordinate)
			if !ok || len(project.Provides[0].Sources) == 0 {
				t.Fatal("fixture needs provider sources")
			}
			project.Provides[0].Sources[0] = "changed-by-caller"
		},
		"listed plugins": func(t *testing.T, r retainedResult) { r.evaluation.Listed[""][0] = "changed-by-caller" },
		"action context": func(t *testing.T, r retainedResult) {
			for _, a := range r.actions {
				if a.Context != nil {
					a.Context.Tools["go"] = "changed-by-caller"
					return
				}
			}
			t.Fatal("fixture needs action context")
		},
	}
	for name, mutate := range mutations {
		for _, stage := range []string{"cold", "warm", "selected"} {
			t.Run(name+"/"+stage, func(t *testing.T) {
				root := retainedFixture(t, 2)
				p, files := newRetainedEngine(), &source.Keeper{}
				profile := pluginv1.Profile_PROFILE_DEVELOPMENT
				var targets []string
				if stage != "cold" {
					retainedOracle(t, p, files, root, profile, nil)
				}
				if stage == "selected" {
					targets = []string{"m0000/"}
				}
				got := retainedOracle(t, p, files, root, profile, targets)
				before, err := retainedSemantic(got.evaluation)
				if err != nil {
					t.Fatal(err)
				}
				planBefore, err := json.Marshal(got.actions)
				if err != nil {
					t.Fatal(err)
				}
				mutate(t, got)
				next, err := p.run(t.Context(), retainedTake(t, files, root), profile, targets)
				if err != nil {
					t.Fatal(err)
				}
				after, err := retainedSemantic(next.evaluation)
				if err != nil {
					t.Fatal(err)
				}
				planAfter, err := json.Marshal(next.actions)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) || !bytes.Equal(planBefore, planAfter) {
					t.Fatal("caller mutation reached a later result")
				}
			})
		}
	}
}

// Both sources are real snapshots taken before both files change. Hints select
// the later project first, but the ordinary evaluator owns which error wins.
func TestRetainedOwnedDiagnosticsUseOrdinaryEvaluationOrder(t *testing.T) {
	root, p, files := warmRetained(t, 2)
	profile := pluginv1.Profile_PROFILE_DEVELOPMENT
	ordinary := &Answers{}
	if _, err := retainedFresh(t.Context(), retainedTake(t, files, root), profile, nil, ordinary); err != nil {
		t.Fatal(err)
	}
	old := retainedTake(t, files, root)
	retainedWrite(t, root, "m0000/main.go", "package main\nfunc main() { println(100) }\n")
	retainedWrite(t, root, "m0001/main.go", "package main\nfunc main() { println(200) }\n")
	prioritized := hintsOrderSource{Source: old, hint: func(_, path string) bool { return path == "m0001/main.go" }}
	_, want := retainedFresh(t.Context(), prioritized, profile, nil, ordinary)
	var expected *source.Changed
	if !errors.As(want, &expected) || expected.Path != "m0000/main.go" {
		t.Fatalf("ordinary control: %v", want)
	}
	_, got := p.run(t.Context(), prioritized, profile, nil)
	var actual *source.Changed
	if !errors.As(got, &actual) || !bytes.Equal([]byte(got.Error()), []byte(want.Error())) {
		t.Fatalf("retained error %v; ordinary error %v", got, want)
	}
	// Asked again of the same source, it is refused as it was, never
	// answered from what was kept of the source before.
	if _, again := p.run(t.Context(), prioritized, profile, nil); again == nil || again.Error() != want.Error() {
		t.Fatalf("a refused source was answered from what was kept: %v", again)
	}
	retainedOracle(t, p, files, root, profile, nil)
}
