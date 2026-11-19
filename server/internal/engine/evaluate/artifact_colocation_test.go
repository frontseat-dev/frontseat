package evaluate

import (
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
	"testing"
)

func TestReviewProvidedArtifactColocationUsesCanonicalProjectRoots(t *testing.T) {
	coordinate := model.Coordinate{Ecosystem: "fixture-install", Name: "web"}
	for _, roots := range [][2]string{{"", ""}, {".", ""}, {"", "."}, {"app/.", "app"}} {
		t.Run(roots[0]+"-to-"+roots[1], func(t *testing.T) {
			provider := graph.Project{Root: roots[0], Plugin: "prepare", Provides: []graph.Provide{{Coordinate: coordinate, Artifacts: []graph.Produced{{Action: "install", Output: "modules/"}}}}}
			consumer := graph.Project{Root: roots[1], Plugin: "framework", Requires: []graph.Requirement{{Coordinate: coordinate}}}
			// These roots are accepted by the public plugin answer validator.
			for _, p := range []graph.Project{provider, consumer} {
				answer := &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Root: "", Projects: []*pluginv1.Project{{Root: p.Root, Kind: "fixture", Name: "web", Manifest: "package.json"}}}}}
				if err := pluginhost.CheckAnalysis(p.Plugin, []string{""}, answer); err != nil {
					t.Fatalf("root acceptance control %q: %v", p.Root, err)
				}
			}
			out := Evaluation{Projects: []graph.Project{provider, consumer}, Actions: []plan.Action{{Label: model.Label{Path: consumer.Path(), Plugin: "framework", Name: "test"}, ProjectRoot: consumer.Root, Declared: &pluginv1.Action{Name: "test", Inputs: append([]*pluginv1.Input{}, pluginhosttest.AllDependencies("{root}"))}}}}
			if _, err := hand(t.Context(), nil, nil, nil, pluginv1.Profile_PROFILE_DEVELOPMENT, nil, &out); err != nil {
				t.Fatalf("same canonical project root refused: %v", err)
			}
			if h := out.Actions[0].Handed; len(h) != 1 || h[0].Into != "." {
				t.Fatalf("co-located artifact was not handed at its root: %+v", h)
			}
		})
	}
}
