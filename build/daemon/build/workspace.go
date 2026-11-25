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
// reading: a build is of one state of its directory. Where through is
// given, the manifest is read through it (directory.Directory.HoldsAsRead);
// nil, it is read as it is.
func sourceOf(d *directory.Directory, through directory.Through) (source.Tree, []record.Located, error) {
	tree := source.Tree{Root: d.Root(), Paths: map[string]string{}}
	if d.Alone() {
		tree.Paths[""] = "."
		return tree, nil, nil
	}
	held, err := d.HoldsAsRead(through)
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
