package source

import (
	"path/filepath"
	"sort"
)

// Tree is where the repositories of a workspace are: what a sandbox
// holds, in directories.
type Tree struct {
	// Root is the workspace's directory.
	Root string
	// Paths are the repositories' directories, relative to Root, by the
	// workspace's name for each.
	Paths map[string]string
	// Mounted are the repositories something else mounted in the
	// workspace, and that were not cloned, by the workspace's name for
	// each, with what its layer said when the tree was read (Mount). A
	// repository that is not named here is read as a clone is.
	Mounted map[string]Mount
}

// Repositories names the repositories, sorted.
func (t Tree) Repositories() []string {
	names := make([]string, 0, len(t.Paths))
	for name := range t.Paths {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (t Tree) directory(repository string) string {
	return filepath.Join(t.Root, filepath.FromSlash(t.Paths[repository]))
}
