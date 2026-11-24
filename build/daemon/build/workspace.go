package build

import (
	"errors"
	"path/filepath"

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
//
// A repository something else mounted is said so, with what its layer
// says now, beneath layers by the repository's name: its stamp, by
// which a run takes a file the mount gave at the hash it says
// (source.HashAttribute). Told of no layers, as a daemon outside a
// sandbox is, none is read, and every file of the repository is.
func sourceOf(d *directory.Directory, layers string) (source.Tree, []record.Located, error) {
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
		if !r.Mounted {
			continue
		}
		if tree.Mounted == nil {
			tree.Mounted = map[string]source.Mount{}
		}
		tree.Mounted[r.Name] = source.Mount{}
		if layers != "" {
			// The name is one directory, as the manifest was held to
			// when it was read.
			tree.Mounted[r.Name] = source.Layer(filepath.Join(layers, r.Name))
		}
	}
	return tree, held.Entities, nil
}
