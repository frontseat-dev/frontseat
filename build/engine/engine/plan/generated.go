package plan

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/frontseat-dev/frontseat/build/engine/model"
	"github.com/frontseat-dev/frontseat/build/engine/pattern"
)

// GeneratedSource is authoritative source contributed by a producing action.
// Directory is repository-relative; Output is relative to ProjectRoot.
type GeneratedSource struct {
	Repository, ProjectRoot, Directory, Output string
	Producer                                   model.Label
	Patterns                                   []string
}

// Covers reports whether a checkout file belongs to generated authority.
func (g GeneratedSource) Covers(repository, file string) bool {
	return repository == g.Repository && pattern.Within(g.Directory, path.Clean(file), g.Patterns)
}

// AllowsOutput holds worker-produced file names to their declared authority.
func (g GeneratedSource) AllowsOutput(file string) bool { return pattern.Select(g.Patterns, file) }

// ArtifactInput binds one producer output to a consumer's input selection.
// Into is relative to the consumer project; source and destination roots need
// not coincide, as when a dependent receives another module's composed source.
// Within is where the claim is beneath the root it is bound at, empty where
// it is that root: Patterns and Generated select files relative to the root,
// and the producer's files are placed at Into/Within.
type ArtifactInput struct {
	Source    GeneratedSource
	Into      string
	Within    string
	Patterns  []string
	Generated []pattern.Selection
}

// AcceptsSnapshot applies generated authority before source is read or copied.
func (a Action) AcceptsSnapshot(repository, file string) bool {
	for _, claim := range a.GeneratedSources {
		if claim.Covers(repository, file) {
			return false
		}
	}
	return true
}

// Accepts applies both the input set's generated selection and all exclusions
// in the final action/provider selection. Producer ownership is checked first.
// The file is relative to the root the claim is bound at (Within joined).
func (a ArtifactInput) Accepts(file string) bool {
	if !pattern.Select(a.Patterns, file) {
		return false
	}
	for _, selection := range a.Generated {
		if selection.Accepts(file) {
			return true
		}
	}
	return false
}

// BindGenerated adds inferred producer edges and selective content bindings.
// A claim binds to the source root that holds it, at it or beneath it (a
// directory inside a module), never by intersecting arbitrary globs; where
// several roots hold it, the deepest alone. Overlapping authoritative
// directories are refused before any action runs.
func BindGenerated(actions []Action, generated []GeneratedSource) error {
	if len(generated) == 0 {
		return nil
	}
	for i, claim := range generated {
		for _, before := range generated[:i] {
			if claim.Repository == before.Repository && (path.Clean(claim.Directory) == "." || path.Clean(before.Directory) == "." || claim.Directory == before.Directory || strings.HasPrefix(claim.Directory+"/", before.Directory+"/") || strings.HasPrefix(before.Directory+"/", claim.Directory+"/")) {
				return &model.Mistake{Message: fmt.Sprintf("%s and %s claim overlapping generated source directories %s and %s", before.Producer, claim.Producer, before.Directory, claim.Directory)}
			}
		}
		// What holds the claim, and how deep: the claim is bound at the
		// deepest root that holds it, the module it is in, in every
		// action that reads that root, as Go reads a file in the nearest
		// module alone. A root equal to the claim's directory is deepest.
		depth := func(repository, root string, selected []pattern.Selection) (int, string) {
			if repository != claim.Repository || len(selected) == 0 {
				return -1, ""
			}
			root = path.Clean(root)
			within, beneath := holds(root, claim.Directory)
			switch {
			case !beneath:
				return -1, ""
			case root == ".":
				return 0, within
			}
			return 1 + strings.Count(root, "/"), within
		}
		best := -1
		for i := range actions {
			for _, group := range actions[i].Sources {
				if d, _ := depth(actions[i].Repository, group.Directory, group.Generated); d > best {
					best = d
				}
			}
			for _, handed := range actions[i].Handed {
				if d, _ := depth(handed.Repository, handed.Root, handed.Generated); d > best {
					best = d
				}
			}
		}
		bound := false
		for i := range actions {
			action := &actions[i]
			bind := func(into, within string, patterns []string, selections []pattern.Selection) {
				bound = true
				action.Artifacts = append(action.Artifacts, ArtifactInput{Source: claim, Into: into, Within: within, Patterns: patterns, Generated: selections})
				if !slices.Contains(action.Needs, claim.Producer) {
					action.Needs = append(action.Needs, claim.Producer)
					action.ArtifactOnly = append(action.ArtifactOnly, claim.Producer)
				}
			}
			for _, group := range action.Sources {
				d, within := depth(action.Repository, group.Directory, group.Generated)
				if d < 0 || d != best {
					continue
				}
				into, err := filepath.Rel(filepath.FromSlash("/"+action.ProjectRoot), filepath.FromSlash("/"+group.Directory))
				if err != nil {
					return err
				}
				bind(filepath.ToSlash(into), within, group.Patterns, group.Generated)
			}
			for _, handed := range action.Handed {
				if d, within := depth(handed.Repository, handed.Root, handed.Generated); d >= 0 && d == best {
					bind(handed.Into, within, handed.Patterns, handed.Generated)
				}
			}
		}
		if !bound {
			return &model.Mistake{Message: fmt.Sprintf("%s contributes generated source at %s, which has no accepting project", claim.Producer, claim.Directory)}
		}
	}
	for i := range actions {
		actions[i].GeneratedSources = generated
	}
	return Check(actions)
}

// holds reports whether a root holds a directory, at it or beneath it, and
// where the directory is relative to the root: empty where it is the root.
// A root of "." holds every directory of its repository.
func holds(root, directory string) (string, bool) {
	directory = path.Clean(directory)
	switch {
	case root == directory:
		return "", true
	case root == ".":
		return directory, true
	case strings.HasPrefix(directory, root) && len(directory) > len(root) && directory[len(root)] == '/':
		return directory[len(root)+1:], true
	}
	return "", false
}
