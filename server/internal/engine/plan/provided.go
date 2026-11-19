package plan

import "slices"

// BindProvided schedules exactly the outputs handed through coordinates.
// A producer required only for a handoff contributes no other outputs at
// their original paths. Explicit reads of that producer remain independent.
func BindProvided(actions []Action) error {
	bound := false
	for i := range actions {
		action := &actions[i]
		for _, handed := range action.Handed {
			for _, artifact := range handed.Artifacts {
				bound = true
				if !slices.Contains(action.Needs, artifact.From) {
					action.Needs = append(action.Needs, artifact.From)
					action.ArtifactOnly = append(action.ArtifactOnly, artifact.From)
				}
			}
		}
	}
	if !bound {
		return nil
	}
	return Check(actions)
}
