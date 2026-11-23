package pluginhosttest

import pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"

// SourceInputs selects several source patterns in their declared order,
// as a plugin names them.
func SourceInputs(patterns ...string) []*pluginv1.Input {
	inputs := make([]*pluginv1.Input, len(patterns))
	for i, pattern := range patterns {
		inputs[i] = &pluginv1.Input{Kind: &pluginv1.Input_Source{Source: pattern}}
	}
	return inputs
}

// OutputInput selects an action's declared output, or all outputs of an
// action, as a plugin names it.
func OutputInput(name string) *pluginv1.Input {
	return &pluginv1.Input{Kind: &pluginv1.Input_Output{Output: name}}
}

// AllDependencies selects every project requirement's workspace
// handoffs, as a plugin names them.
func AllDependencies(into string) *pluginv1.Input {
	return &pluginv1.Input{Kind: &pluginv1.Input_Dependency{Dependency: &pluginv1.DependencyInput{
		Selection: &pluginv1.DependencyInput_All{All: &pluginv1.AllRequirements{}}, Into: into,
	}}}
}
