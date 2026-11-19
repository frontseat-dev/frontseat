package plan

import (
	"fmt"
	"path"
	"strings"

	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// CheckHanded holds dependency placements to the repository and to a
// one-to-one mapping. A placement may lead above the project, where a
// tool finds a dependency by the path its manifest names, but never out
// of the repository; and two dependencies cannot silently replace one
// another in the staging map, even when their declared destination
// paths use different spellings. It allocates nothing for a placement
// that is spelled as it is placed: a plan of a thousand is checked on
// every run.
func CheckHanded(action Action) error {
	seen := make(map[string]bool, len(action.Handed))
	depth := 0
	if action.ProjectRoot != "" {
		depth = strings.Count(action.ProjectRoot, "/") + 1
	}
	for _, handed := range action.Handed {
		at := path.Clean(handed.Into)
		if handed.Into == "" || path.IsAbs(at) || up(at) > depth {
			return &model.Mistake{Message: fmt.Sprintf("%s places a dependency at %q, which is not within the repository", action.Label, handed.Into)}
		}
		if seen[at] {
			return &model.Mistake{Message: fmt.Sprintf("%s places more than one dependency at %s", action.Label, at)}
		}
		seen[at] = true
	}
	return nil
}

// Shadows is the mistake of an action that is handed something at a
// place where a file of the repository it reads is, or is within: two
// sources of one path. The place is as the action's root names it.
func Shadows(action Action, at string) error {
	return &model.Mistake{Message: fmt.Sprintf("%s is handed %s, which is also a file of the repository it reads", action.Label, at)}
}

// up is how many directories a clean relative path leads up before it
// leads down: its leading ".." steps.
func up(clean string) int {
	n := 0
	for clean == ".." || strings.HasPrefix(clean, "../") {
		n++
		clean = strings.TrimPrefix(strings.TrimPrefix(clean, ".."), "/")
	}
	return n
}
