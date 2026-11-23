package build

import (
	"errors"

	"github.com/frontseat-dev/frontseat/server/internal/directory"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
)

// sourceOf returns where a directory's repositories are, or an error if
// it holds none to build: what a build is given is what is there, as the
// directory's manifest records it, whoever filled it and however that is
// getting on.
func sourceOf(d *directory.Directory) (source.Tree, error) {
	tree := source.Tree{Root: d.Root(), Paths: map[string]string{}}
	if d.Alone() {
		tree.Paths[""] = "."
		return tree, nil
	}
	held, err := d.Repositories()
	if err != nil {
		return source.Tree{}, err
	}
	if len(held) == 0 {
		return source.Tree{}, errors.New("the workspace holds no repository that is ready to build")
	}
	for _, r := range held {
		tree.Paths[r.Name] = r.Name
	}
	return tree, nil
}
