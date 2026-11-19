package pattern

import "slices"

// CloneSelections gives a caller its own nested pattern scopes.
func CloneSelections(selections []Selection) []Selection {
	out := slices.Clone(selections)
	for i, selection := range out {
		out[i] = slices.Clone(selection)
		for j, scope := range out[i] {
			out[i][j] = slices.Clone(scope)
		}
	}
	return out
}

// CloneGroups gives a caller its own source groups: their patterns and
// their generated-source scopes.
func CloneGroups(groups []Group) []Group {
	out := slices.Clone(groups)
	for i := range out {
		out[i].Patterns = slices.Clone(out[i].Patterns)
		out[i].Generated = CloneSelections(out[i].Generated)
	}
	return out
}
