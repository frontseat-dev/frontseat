package pluginhost_test

import (
	"slices"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/pluginhosttest"
)

func TestTypedSourceInputsCannotBecomeReferences(t *testing.T) {
	answer := sound()
	action := answer.Analyses[0].Projects[0].Actions[1]
	names := []string{":literal", "=literal", "@literal"}
	action.Inputs = pluginhosttest.SourceInputs(names...)
	if err := pluginhost.CheckAnalysis("go", []string{"services"}, answer); err != nil {
		t.Fatal(err)
	}
	patterns, references := pluginhost.SplitInputs(action.Inputs)
	if !slices.Equal(patterns, names) || len(references) != 0 {
		t.Fatalf("source meaning changed: %v, %v", patterns, references)
	}
	// The same name in the output variant must resolve to a producer.
	action.Inputs = []*pluginv1.Input{pluginhosttest.OutputInput(":literal")}
	if err := pluginhost.CheckAnalysis("go", []string{"services"}, answer); err == nil {
		t.Fatal("unknown output accepted as source")
	}
}

func TestTypedDependencyInputsAreHeldToTheirProject(t *testing.T) {
	coordinate := &pluginv1.Coordinate{Ecosystem: "go", Name: "example.com/greet"}
	valid := pluginhost.DependencyInput(coordinate, "deps/{name}")
	check := func(input *pluginv1.Input, inSet bool) error {
		answer := sound()
		project := answer.Analyses[0].Projects[0]
		if inSet {
			project.InputSets[0].Inputs = []*pluginv1.Input{input}
		} else {
			project.Actions[1].Inputs = []*pluginv1.Input{input}
		}
		return pluginhost.CheckAnalysis("go", []string{"services"}, answer)
	}
	if err := check(valid, false); err != nil {
		t.Fatalf("valid selection refused: %v", err)
	}
	for name, input := range map[string]*pluginv1.Input{
		"nil input":             nil,
		"missing kind":          {},
		"nil dependency":        {Kind: &pluginv1.Input_Dependency{}},
		"missing selection":     {Kind: &pluginv1.Input_Dependency{Dependency: &pluginv1.DependencyInput{Into: "deps"}}},
		"an unset all":          {Kind: &pluginv1.Input_Dependency{Dependency: &pluginv1.DependencyInput{Selection: &pluginv1.DependencyInput_All{}, Into: "deps"}}},
		"empty coordinate":      pluginhost.DependencyInput(&pluginv1.Coordinate{}, "deps"),
		"undeclared coordinate": pluginhost.DependencyInput(&pluginv1.Coordinate{Ecosystem: "go", Name: "unknown"}, "deps"),
		"empty placement":       pluginhost.DependencyInput(coordinate, ""),
		"parent placement":      pluginhost.DependencyInput(coordinate, "../deps"),
		"absolute placement":    pluginhost.DependencyInput(coordinate, "/tmp/deps"),
		"mixed root":            pluginhost.DependencyInput(coordinate, "{root}/child"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := check(input, false); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	if err := check(valid, true); err == nil {
		t.Fatal("dependency allowed inside a source input set")
	}
}
