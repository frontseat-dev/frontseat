package evaluate

import (
	"github.com/frontseat-dev/frontseat/build/engine/engine/config"
	"github.com/frontseat-dev/frontseat/build/engine/engine/graph"
	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/build/engine/wire"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"testing"
)

func TestTypedInputsKeepCatalogReferenceScope(t *testing.T) {
	producer, err := custom("", config.Action{Name: "make", Directory: "producer", Command: []string{"true"}, Outputs: []string{"out"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input string
		accepted    bool
	}{
		{"cross-project control", "//producer:custom.make", true},
		{"local prefix cannot become cross-project", "://producer:custom.make", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			consumer, err := custom("", config.Action{Name: "read", Directory: "consumer", Command: []string{"true"}, Inputs: []string{tc.input}})
			if err == nil {
				err = plan.Resolve([]plan.Action{producer, consumer})
			}
			if (err == nil) != tc.accepted {
				t.Fatalf("input %q accepted=%v, want %v; resolved references=%+v; err=%v", tc.input, err == nil, tc.accepted, consumer.References, err)
			}
		})
	}
}

func TestTypedDependencySelectionsCompareActualPlacement(t *testing.T) {
	coordinate := model.Coordinate{Ecosystem: "data", Name: "fixture"}
	consumer := graph.Project{Root: "app", Plugin: "consumer", Requires: []graph.Requirement{{Coordinate: coordinate}}}
	provider := graph.Project{Root: "fixture", Plugin: "producer", Provides: []graph.Provide{{Coordinate: coordinate, Sources: []string{"**"}}}}
	for _, tc := range []struct {
		name, second string
		accepted     bool
	}{
		{"same spelling control", "deps/{name}", true},
		{"same actual destination", "deps/fixture", true},
		{"same canonical destination", "deps/./fixture", true},
		{"conflicting destination control", "other/fixture", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := Evaluation{Projects: []graph.Project{consumer, provider}, Actions: []plan.Action{{Label: model.Label{Path: "app", Plugin: "consumer", Name: "read"}, ProjectRoot: "app", Declared: &pluginv1.Action{Name: "read", Inputs: []*pluginv1.Input{pluginhost.DependencyInput(wire.Coordinate(coordinate), "deps/{name}"), pluginhost.DependencyInput(wire.Coordinate(coordinate), tc.second)}}}}}
			_, err := hand(t.Context(), nil, nil, nil, typev1.Profile_PROFILE_DEVELOPMENT, nil, &out)
			if (err == nil) != tc.accepted {
				t.Fatalf("second placement %q accepted=%v, want %v: %v", tc.second, err == nil, tc.accepted, err)
			}
			if err == nil && len(out.Actions[0].Handed) != 1 {
				t.Fatalf("same provider handed more than once: %+v", out.Actions[0].Handed)
			}
		})
	}
}

func TestTypedArtifactPlacementAliasesRetainRootRestrictions(t *testing.T) {
	coordinate := model.Coordinate{Ecosystem: "data", Name: "fixture"}
	for _, tc := range []struct {
		name, repository string
		sources          bool
		accepted         bool
	}{
		{"co-located artifact control", "", false, true},
		{"source cannot cover its own root", "", true, false},
		{"foreign root stays foreign", "other", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			consumer := graph.Project{Root: "app", Plugin: "consumer", Requires: []graph.Requirement{{Coordinate: coordinate}}}
			provided := graph.Provide{Coordinate: coordinate, Artifacts: []graph.Produced{{Action: "make", Output: "fixture"}}}
			if tc.sources {
				provided.Sources = []string{"**"}
			}
			provider := graph.Project{Repository: tc.repository, Root: "app/.", Plugin: "producer", Provides: []graph.Provide{provided}}
			out := Evaluation{Projects: []graph.Project{consumer, provider}, Actions: []plan.Action{{Label: model.Label{Path: "app", Plugin: "consumer", Name: "read"}, ProjectRoot: "app", Declared: &pluginv1.Action{Name: "read", Inputs: []*pluginv1.Input{pluginhost.DependencyInput(wire.Coordinate(coordinate), "{root}"), pluginhost.DependencyInput(wire.Coordinate(coordinate), ".")}}}}}
			_, err := hand(t.Context(), nil, nil, nil, typev1.Profile_PROFILE_DEVELOPMENT, nil, &out)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v, want %v: %v", err == nil, tc.accepted, err)
			}
			if err == nil && (len(out.Actions[0].Handed) != 1 || out.Actions[0].Handed[0].Into != ".") {
				t.Fatalf("equivalent artifact placements changed the handoff: %+v", out.Actions[0].Handed)
			}
		})
	}
}
