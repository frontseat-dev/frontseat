package manifest

import (
	"fmt"

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
	// State is what the workspace is, where what the manifest records is
	// not whole: Filling, from before anything of a fill is brought until
	// all of it is recorded, or Failed, after a fill that did not end.
	// Empty, what is recorded is whole. Whoever fills the workspace
	// writes it; a build, which reads the directory and no one else,
	// takes nothing of a workspace that says either.
	State string `yaml:"state,omitempty"`
}

// The states a manifest says.
const (
	Filling = "filling"
	Failed  = "failed"
)

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
	if m.State != "" && m.State != Filling && m.State != Failed {
		return fmt.Errorf("%w: its state is none a workspace is in", record.ErrUnfit)
	}
	named := map[string]bool{}
	for _, r := range m.Repositories {
		if err := r.Within(revision); err != nil {
			return err
		}
		// A repository is one directory of the workspace, by its name:
		// two of one name are one directory said to be two things.
		if named[r.Name] {
			return fmt.Errorf("%w: a repository is recorded twice, and a name is one directory of the workspace", record.ErrUnfit)
		}
		named[r.Name] = true
	}
	for _, e := range m.Known {
		if err := e.Within(); err != nil {
			return err
		}
	}
	return nil
}
