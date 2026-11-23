package pluginhost

import pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"

// SourceInput selects source by pattern, without interpreting reference prefixes.
func SourceInput(pattern string) *pluginv1.Input {
	return &pluginv1.Input{Kind: &pluginv1.Input_Source{Source: pattern}}
}

// SetInput selects a project's named source input set.
func SetInput(name string) *pluginv1.Input {
	return &pluginv1.Input{Kind: &pluginv1.Input_InputSet{InputSet: name}}
}

// DependencyInput selects one declared requirement's workspace handoffs.
func DependencyInput(coordinate *pluginv1.Coordinate, into string) *pluginv1.Input {
	return &pluginv1.Input{Kind: &pluginv1.Input_Dependency{Dependency: &pluginv1.DependencyInput{
		Selection: &pluginv1.DependencyInput_Coordinate{Coordinate: coordinate}, Into: into,
	}}}
}
