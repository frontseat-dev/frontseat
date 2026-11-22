package evaluate

import (
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
	"strings"
	"testing"
)

func TestProvidedArtifactsUseTheExistingGraphAndRootPlacement(t *testing.T) {
	coordinate := model.Coordinate{Ecosystem: "fixture-install", Name: "web"}
	for _, tc := range []struct {
		name, repo, root, into string
		refused                bool
	}{
		{"co-located", "", "app", ".", false},
		{"different root", "", "other", "../other", false},
		{"foreign repository", "foreign", "app", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := graph.Project{Repository: tc.repo, Root: tc.root, Plugin: "prepare", Provides: []graph.Provide{{Coordinate: coordinate, Artifacts: []graph.Produced{{Action: "install", Output: "modules/"}}}}}
			consumer := graph.Project{Root: "app", Plugin: "framework", Requires: []graph.Requirement{{Coordinate: coordinate}}}
			out := Evaluation{Projects: []graph.Project{provider, consumer}, Actions: []plan.Action{
				{Label: model.Label{Path: provider.Path(), Plugin: "prepare", Name: "install"}, Repository: tc.repo, ProjectRoot: tc.root, Declared: &pluginv1.Action{Name: "install"}},
				{Label: model.Label{Path: "app", Plugin: "framework", Name: "test"}, ProjectRoot: "app", Declared: &pluginv1.Action{Name: "test", Inputs: append([]*pluginv1.Input{}, pluginhosttest.AllDependencies("{root}"))}},
			}}
			_, err := hand(t.Context(), nil, nil, nil, typev1.Profile_PROFILE_DEVELOPMENT, nil, &out)
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "another repository") {
					t.Fatalf("foreign root: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if h := out.Actions[1].Handed; len(h) != 1 || h[0].Into != tc.into || len(h[0].Artifacts) != 1 {
				t.Fatalf("handoff: %+v", h)
			}
			if err := plan.BindProvided(out.Actions); err != nil {
				t.Fatal(err)
			}
			if needs := out.Actions[1].Needs; len(needs) != 1 || needs[0] != out.Actions[0].Label {
				t.Fatalf("provider not scheduled: %v", needs)
			}
			out.Actions[0].Needs = []model.Label{out.Actions[1].Label}
			if err := plan.Check(out.Actions); err == nil {
				t.Fatal("artifact cycle accepted")
			}
		})
	}
}
