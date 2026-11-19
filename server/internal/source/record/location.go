package record

import (
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"path"
)

// Located is an entity and where it lives in a workspace.
type Located struct {
	// Ref names the entity.
	Ref entity.Ref
	// Repository is the workspace's name for the repository it is in.
	Repository string
	// Root is its directory in that repository; empty for the root.
	Root string
}

// Path is the entity's directory in the workspace: its repository's name
// and its directory there.
func (l Located) Path() string { return path.Join(l.Repository, l.Root) }
