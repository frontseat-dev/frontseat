package model

import (
	"fmt"
	"strings"
)

// Reference is an input of an action that names what another action
// left, as it is written: ":what" for something of the same project, or
// "//<path>:what" for something of another. What follows the colon is
// an action, for all it leaves, or one output of an action, spelled as
// that action declares it; which of the two it is, is known only among
// the workspace's actions, so a reference is resolved there (plan) and
// only parsed here.
type Reference struct {
	// Path is the directory of the project whose actions are named, as
	// a Label's Path: the repository's name first.
	Path string
	// Rest is what follows the colon, as written.
	Rest string
	// Written is the reference as it was written.
	Written string
}

// ParseReference reads a reference written in an action of the project
// at a path: ":what" names something of that project, "//path:what"
// something of the project at that path. The path is as a label prints
// it, so it is empty for the root of a lone repository: "//:what".
func ParseReference(at, written string) (Reference, error) {
	if rest, same := strings.CutPrefix(written, ":"); same {
		if rest == "" {
			return Reference{}, fmt.Errorf("model: the input %q names nothing after the colon", written)
		}
		// Nothing an action is named by, or leaves, starts so: it is
		// another project's, written with a colon too many.
		if strings.HasPrefix(rest, "//") {
			return Reference{}, fmt.Errorf("model: the input %q names another project after a colon: write //path:what", written)
		}
		return Reference{Path: at, Rest: rest, Written: written}, nil
	}
	rest, ok := strings.CutPrefix(written, "//")
	if !ok {
		return Reference{}, fmt.Errorf("model: the input %q is no reference: one starts with : or //", written)
	}
	path, what, ok := strings.Cut(rest, ":")
	switch {
	case !ok || what == "":
		return Reference{}, fmt.Errorf("model: the input %q names a project and nothing of it: write //path:what", written)
	case strings.HasPrefix(path, "/") || strings.Contains(path, "//") || strings.HasSuffix(path, "/"):
		return Reference{}, fmt.Errorf("model: the input %q names no project: a path is written as a label prints it", written)
	}
	return Reference{Path: path, Rest: what, Written: written}, nil
}
