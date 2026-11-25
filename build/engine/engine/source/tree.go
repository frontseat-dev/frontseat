package source

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Tree is where the repositories of a workspace are: what a sandbox
// holds, in directories.
type Tree struct {
	// Root is the workspace's directory.
	Root string
	// Paths are the repositories' directories, relative to Root, by the
	// workspace's name for each.
	Paths map[string]string
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

// Directory is where a repository's directory is, where the daemon runs:
// what an action of the repository reads its files from, its own
// repository being at the top of its root wherever the workspace holds
// it.
func (t Tree) Directory(repository string) string {
	return filepath.Join(t.Root, filepath.FromSlash(t.Paths[repository]))
}

// apart refuses, by name, a tree in which one repository's directory is
// the directory of another or within it. An action's own repository is
// at the top of its root and another's beneath a directory of its name
// (stage), each place of the workspace being in the directory of one
// repository or of none: where one held another, an output of the outer
// within the inner would be handed where its producer did not write it.
// The daemon holds each repository in a directory of its own, by its
// name, or one alone at the top; nesting is refused, not handled.
func (t Tree) apart() error {
	for outer, at := range t.Paths {
		at = path.Clean(at)
		for inner, in := range t.Paths {
			if inner == outer {
				continue
			}
			if in = path.Clean(in); at == "." || in == at || strings.HasPrefix(in, at+"/") {
				return fmt.Errorf("the workspace holds the repository %q in %s, which is within %s, where it holds the repository %q: a repository's directory is within no other's", inner, in, at, outer)
			}
		}
	}
	return nil
}

// where returns a repository's directory as the workspace names it, and
// the same with any link on the way to it followed: where its files are
// read.
func (t Tree) where(repository string) (at, directory string) {
	at = t.Directory(repository)
	directory, err := filepath.EvalSymlinks(at)
	if err != nil {
		directory = at
	}
	return at, directory
}
