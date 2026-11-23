package manifest

import (
	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

const File = ".frontseat/workspace.yaml"

// Manifest is what a workspace holds.
type Manifest struct {
	// Version is the manifest's version: 1.
	Version int `yaml:"version"`
	// Repositories are the repositories it holds.
	Repositories []record.Repository `yaml:"repositories"`
	// Known are the entities the workspace knows where to find: those
	// asked for and those they depend on.
	Known []Entity `yaml:"known,omitempty"`
}

// Entity is an entity a workspace knows, and where it lives.
type Entity struct {
	// Ref is its reference, written kind:namespace/name.
	Ref entity.Ref `yaml:"ref"`
	// Repository is the workspace's name for the repository it is in.
	Repository string `yaml:"repository"`
	// Root is its directory in that repository; empty for the root.
	Root string `yaml:"root,omitempty"`
}

// Located is the entity and where it lives, as the host speaks of one.
func (e Entity) Located() record.Located {
	return record.Located{Ref: e.Ref, Repository: e.Repository, Root: e.Root}
}

// Within holds an entity to a directory of a repository of the
// workspace.
func (e Entity) Within() error {
	if err := record.Directory(e.Repository); err != nil {
		return err
	}
	if e.Root == "" {
		return nil
	}
	return record.Beneath(e.Repository, "the entity's directory", e.Root)
}

// Within holds a manifest to the workspace it is of: every repository
// and every entity it names is within it.
func (m Manifest) Within(revision func(name string) error) error {
	for _, r := range m.Repositories {
		if err := r.Within(revision); err != nil {
			return err
		}
	}
	for _, e := range m.Known {
		if err := e.Within(); err != nil {
			return err
		}
	}
	return nil
}
