package model

import (
	"fmt"
	"path"
	"strings"
)

// Reference is an input of an action that names what another action
// left, as it is written: ":what" for something of the same project, or
// "//<path>:what" for something of another project of the same
// repository, its path written from the repository's root. What follows
// the colon is an action, for all it leaves, or one output of an action,
// spelled as that action declares it; which of the two it is, is known
// only among the workspace's actions, so a reference is resolved there
// (plan) and only parsed here.
type Reference struct {
	// Path is the directory of the project whose actions are named, as
	// a Label's Path: the repository's name first.
	Path string
	// Rest is what follows the colon, as written.
	Rest string
	// Written is the reference as it was written.
	Written string
}

// ParseReference reads a reference written in the project at directory
// of repository: ":what" names something of that project, "//path:what"
// something of the project at path from the repository's root, as Bazel
// reads "//" within the repository it is written in. A workspace holds a
// repository beneath its name, and nothing written in the repository
// says that name, so the same reference names the same project wherever
// the repository is held. The path is empty for the repository's root,
// "//:what", and is plain: no "." or "..", which would name a project of
// another repository, or none, by where the workspace holds this one.
func ParseReference(repository, directory, written string) (Reference, error) {
	if rest, same := strings.CutPrefix(written, ":"); same {
		if rest == "" {
			return Reference{}, fmt.Errorf("model: the input %q names nothing after the colon", written)
		}
		// Nothing an action is named by, or leaves, starts so: it is
		// another project's, written with a colon too many.
		if strings.HasPrefix(rest, "//") {
			return Reference{}, fmt.Errorf("model: the input %q names another project after a colon: write //path:what", written)
		}
		return Reference{Path: path.Join(repository, directory), Rest: rest, Written: written}, nil
	}
	rest, ok := strings.CutPrefix(written, "//")
	if !ok {
		return Reference{}, fmt.Errorf("model: the input %q is no reference: one starts with : or //", written)
	}
	at, what, ok := strings.Cut(rest, ":")
	switch {
	case !ok || what == "":
		return Reference{}, fmt.Errorf("model: the input %q names a project and nothing of it: write //path:what", written)
	case at != "" && !plainPath(at):
		return Reference{}, fmt.Errorf("model: the input %q names no project of its repository: a path is written from the repository's root, each part a name", written)
	}
	return Reference{Path: path.Join(repository, at), Rest: what, Written: written}, nil
}

// plainPath reports whether p is directories by name alone: no empty
// part, which a leading, doubled or trailing slash makes, and no "." or
// "..".
func plainPath(p string) bool {
	for part := range strings.SplitSeq(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
