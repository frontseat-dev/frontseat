package record

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrUnfit is what a workspace is asked to hold and cannot: a repository
// whose name is not one directory of it, a path that leads out of its
// repository, a commit that is not one. Whoever asked is at fault, and
// not the workspace.
var ErrUnfit = errors.New("a workspace cannot hold it")

// Directory holds a repository's name to what it is: the one directory
// of the workspace the repository is cloned into. A name comes from
// whoever asks, from a catalog, and from a
// manifest whatever runs in the workspace can write, and it is joined to
// the workspace's directory.
func Directory(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: a repository with no name: it is cloned into the directory of the workspace it is named for", ErrUnfit)
	case name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00"):
		return fmt.Errorf("%w: the repository %q is outside the workspace: a repository is one directory of it", ErrUnfit, name)
	case strings.EqualFold(name, ".frontseat"):
		return fmt.Errorf("%w: the repository %q is where the workspace keeps its own record", ErrUnfit, name)
	}
	return nil
}

// Beneath holds a path to the repository it is relative to; what says
// what the path is of, for whoever is told it is refused.
func Beneath(repository, what, at string) error {
	if !filepath.IsLocal(filepath.FromSlash(at)) {
		return fmt.Errorf("%w: %s %q of the repository %s is outside it", ErrUnfit, what, at, repository)
	}
	return nil
}

// At holds the commit a repository is said to stand at to one its
// version control system writes in full, which revision says: the
// system is asked for it by that name.
func At(repository, commit string, revision func(name string) error) error {
	if err := revision(commit); err != nil {
		return fmt.Errorf("%w: the repository %s is at %q, which %v", ErrUnfit, repository, commit, err)
	}
	return nil
}

// Repository is one repository of a workspace.
type Repository struct {
	// Name is the workspace's name for it, and its directory there.
	Name string `yaml:"name"`
	// Remote is where it was cloned from.
	Remote string `yaml:"remote"`
	// Ref is the branch or tag it was asked for at; empty for the
	// remote's default branch.
	Ref string `yaml:"ref,omitempty"`
	// Commit is the commit it stands at.
	Commit string `yaml:"commit"`
	// Roots are the directories taken of it; empty for all of it.
	Roots []string `yaml:"roots,omitempty"`
}

// Within holds a repository to one directory of the workspace, at a
// commit, with the directories taken of it beneath it.
func (r Repository) Within(revision func(name string) error) error {
	if err := Directory(r.Name); err != nil {
		return err
	}
	if err := At(r.Name, r.Commit, revision); err != nil {
		return err
	}
	for _, root := range r.Roots {
		if err := Beneath(r.Name, "the directory", root); err != nil {
			return err
		}
	}
	return nil
}
