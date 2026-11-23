package model

import "path"

// ProjectRef identifies a plugin's project. Two plugins may discover different
// projects in one directory; repository and root remain separate identity parts.
type ProjectRef struct {
	Repository string
	Root       string
	Plugin     string
}

// NewProjectRef canonicalizes a validated project directory. The repository
// root is the empty string, including when a plugin wrote it as ".".
func NewProjectRef(repository, root, plugin string) ProjectRef {
	root = path.Clean(root)
	if root == "." {
		root = ""
	}
	return ProjectRef{Repository: repository, Root: root, Plugin: plugin}
}

// String describes the project for diagnostics, retaining its plugin identity.
func (r ProjectRef) String() string {
	return path.Join(r.Repository, r.Root) + " (" + r.Plugin + ")"
}
