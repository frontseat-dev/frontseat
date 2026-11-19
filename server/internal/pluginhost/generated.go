package pluginhost

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
)

// checkGenerated validates producer-local references before they can become
// dependencies or authoritative regions in another project's source. Where
// a contribution goes is said once: by its directory beneath the project,
// or by its destination in the repository outside the project. Whether a
// destination's Component lets it go there is the evaluation's to say,
// which knows the catalog; here it is held only to be written plainly.
func checkGenerated(project *pluginv1.Project) error {
	seen := map[string]bool{}
	root := path.Clean(project.GetRoot())
	for _, generated := range project.GetGeneratedSources() {
		// directory names it in what is said of it; destination is where
		// it is in the repository.
		directory, destination := generated.GetDirectory(), ""
		switch {
		case generated.GetDestination() == "":
			if directory != "." && !plainDirectory(directory) {
				return fmt.Errorf("generated source directory %q is not a canonical project-relative directory", directory)
			}
			destination = path.Join(root, directory)
		case directory != "":
			return fmt.Errorf("generated source names both directory %q and destination %q; it goes to one of them", directory, generated.GetDestination())
		default:
			destination, directory = generated.GetDestination(), generated.GetDestination()
			if !plainDirectory(destination) {
				return fmt.Errorf("generated source destination %q is not a canonical repository-relative directory", destination)
			}
			if root == "." || destination == root || strings.HasPrefix(destination, root+"/") {
				return fmt.Errorf("generated source destination %q is within its project at %q; a place within the project is its directory", destination, project.GetRoot())
			}
			if strings.HasPrefix(root, destination+"/") {
				return fmt.Errorf("generated source destination %q holds its own project at %q", destination, project.GetRoot())
			}
		}
		// .frontseat, wherever it is, is the host's: where an action's
		// root keeps its home and what it is handed.
		if slices.Contains(strings.Split(destination, "/"), ".frontseat") {
			return fmt.Errorf("generated source %q is in a .frontseat directory, which is the host's", destination)
		}
		if seen[destination] {
			return fmt.Errorf("generated source %q is contributed twice", destination)
		}
		seen[destination] = true
		output := generated.GetOutput()
		at := strings.TrimSuffix(output, "/")
		if !strings.HasSuffix(output, "/") || !pattern.Local(at) || at == "." || path.Clean(at) != at || strings.HasPrefix(at, "~/") || strings.ContainsAny(at, "\\\x00") {
			return fmt.Errorf("generated source output %q must name a directory beneath its project", output)
		}
		declared := false
		for _, action := range project.GetActions() {
			if action.GetName() != generated.GetAction() {
				continue
			}
			for _, left := range action.GetOutputs() {
				if left.GetPath() == output && !left.GetOptional() {
					declared = true
				}
			}
		}
		if !declared {
			return fmt.Errorf("generated source names %s output %q, which is not a required declared output", generated.GetAction(), output)
		}
		positive := false
		for _, selection := range generated.GetPatterns() {
			// GeneratedSource.patterns remains a pattern-only string field.
			// Preserve its refusal of reference shorthand; typed source inputs
			// are explicit and do not need that disambiguation.
			if strings.HasPrefix(selection, "=") || pattern.IsReference(selection) {
				return fmt.Errorf("generated source %q declares reference %q instead of a source pattern", directory, selection)
			}
			if err := checkSourcePattern("generated source "+directory, selection); err != nil {
				return err
			}
			positive = positive || !strings.HasPrefix(selection, "!")
		}
		if !positive {
			return fmt.Errorf("generated source %q declares no included files", directory)
		}
	}
	return nil
}
