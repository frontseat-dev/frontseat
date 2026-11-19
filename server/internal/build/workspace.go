package build

import (
	"errors"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	sourced "github.com/frontseat-dev/frontseat/server/internal/source"
)

// sourceOf returns where a workspace's repositories are, or an error if
// it cannot be built.
func sourceOf(w *sourced.Workspace) (source.Tree, error) {
	now, _ := w.Now()
	if now.State != sourced.Ready {
		return source.Tree{}, errors.New("the workspace holds no repository that is ready to build")
	}
	tree := source.Tree{Root: w.Root(), Paths: map[string]string{}}
	if w.Mounted() {
		tree.Paths[""] = "."
	}
	for _, r := range now.Repositories {
		tree.Paths[r.Name] = r.Name
	}
	return tree, nil
}
