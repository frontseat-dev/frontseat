package build

import (
	"errors"

	"github.com/frontseat-dev/frontseat/build/daemon/directory"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/common/record"
)

// sourceOf returns where a directory's repositories are, or an error if
// it holds none to build: what a build is given is what is there, as the
// directory's manifest records it, whoever filled it and however that is
// getting on.
//
// The entities the directory knows are returned beside it, of the same
// reading: a build is of one state of its directory.
func sourceOf(d *directory.Directory) (source.Tree, []record.Located, error) {
	tree := source.Tree{Root: d.Root(), Paths: map[string]string{}}
	if d.Alone() {
		tree.Paths[""] = "."
		return tree, nil, nil
	}
	held, err := d.Holds()
	if err != nil {
		return source.Tree{}, nil, err
	}
	if len(held.Repositories) == 0 {
		return source.Tree{}, nil, errors.New("the workspace holds no repository that is ready to build")
	}
	for _, r := range held.Repositories {
		tree.Paths[r.Name] = r.Name
	}
	return tree, held.Entities, nil
}

// Source returns where the repositories of a directory are, as a build
// of it would read them now (sourceOf): for whoever would know when a
// build of the directory would read otherwise, and builds it again then
// (life.Look). Alone, the directory is one repository the build was
// given.
func Source(root string, alone bool, system directory.System) (source.Tree, error) {
	d, err := directory.Open(root, alone, system)
	if err != nil {
		return source.Tree{}, err
	}
	tree, _, err := sourceOf(d)
	return tree, err
}
