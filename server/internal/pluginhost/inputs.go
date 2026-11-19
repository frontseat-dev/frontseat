package pluginhost

import (
	"fmt"
	"slices"

	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// ResolveInputs expands named input sets after applying the shared cycle checks.
func ResolveInputs(inputs []*pluginv1.Input, sets map[string][]*pluginv1.Input) ([]*pluginv1.Input, error) {
	return resolve(inputs, sets, nil)
}

// GeneratedInputs retains the selections that explicitly accept generated
// source. Their union is intersected with the final resolved input patterns;
// sibling snapshot-only selections never widen it.
func GeneratedInputs(inputs []*pluginv1.Input, sets map[string]*pluginv1.InputSet) ([]pattern.Selection, error) {
	raw := make(map[string][]*pluginv1.Input, len(sets))
	for name, set := range sets {
		raw[name] = set.GetInputs()
	}
	if _, err := ResolveInputs(inputs, raw); err != nil {
		return nil, err
	}
	var selections []pattern.Selection
	var visit func([]*pluginv1.Input, pattern.Selection) error
	visit = func(inputs []*pluginv1.Input, enclosing pattern.Selection) error {
		for _, input := range inputs {
			name := input.GetInputSet()
			if name == "" {
				continue
			}
			set := sets[name]
			resolved, err := ResolveInputs(set.GetInputs(), raw)
			if err != nil {
				return err
			}
			patterns, _ := SplitInputs(resolved)
			scopes := append(slices.Clone(enclosing), patterns)
			if set.GetAcceptsGenerated() {
				selections = append(selections, scopes)
			} else if err := visit(set.GetInputs(), scopes); err != nil {
				return err
			}
		}
		return nil
	}
	err := visit(inputs, nil)
	return selections, err
}

// resolve replaces input-set selections with their contents, preserving the
// kinds of every other input. Source patterns never become references.
func resolve(inputs []*pluginv1.Input, sets map[string][]*pluginv1.Input, within []string) ([]*pluginv1.Input, error) {
	var out []*pluginv1.Input
	for _, input := range inputs {
		_, isSet := input.GetKind().(*pluginv1.Input_InputSet)
		if !isSet {
			out = append(out, input)
			continue
		}
		name := input.GetInputSet()
		set, declared := sets[name]
		if !declared {
			return nil, fmt.Errorf("it reads the input set %q, which its project does not declare", name)
		}
		if slices.Contains(within, name) {
			return nil, fmt.Errorf("the input set %q reads itself", name)
		}
		resolved, err := resolve(set, sets, append(within, name))
		if err != nil {
			return nil, err
		}
		out = append(out, resolved...)
	}
	return out, nil
}

// Dependencies are the dependency selections in an action's one input list.
func Dependencies(inputs []*pluginv1.Input) []*pluginv1.DependencyInput {
	var dependencies []*pluginv1.DependencyInput
	for _, input := range inputs {
		if dependency := input.GetDependency(); dependency != nil {
			dependencies = append(dependencies, dependency)
		}
	}
	return dependencies
}
