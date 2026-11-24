// Package sourcestest holds a workspace's stored repositories in memory,
// for a test: what a control plane is given in place of what serves a
// snapshot store (sources.Sources). It mounts nothing. Each repository
// asked for stands at a changeset named for it, and is said to be a
// directory beneath the one the test gives; what was asked of it is
// kept, for the test to read.
package sourcestest

import (
	"context"
	"errors"
	"path"
	"slices"
	"sync"

	"github.com/frontseat-dev/frontseat/workspace/server/workspaces/sources"
)

// Memory is the stored repositories of the tests' workspaces.
type Memory struct {
	// Beneath is the directory each repository is said to be mounted
	// beneath, on the machine sandboxes are made on: an absolute path.
	Beneath string
	// Layers is the directory each repository's layer is said to be
	// beneath, as Beneath is of the repository; empty for a mount that
	// keeps no layer.
	Layers string

	mu       sync.Mutex
	fails    error
	held     map[string]sources.Workspace
	holds    []sources.Workspace
	releases []sources.Workspace
}

var _ sources.Sources = (*Memory)(nil)

// New holds nothing yet, and says its repositories are beneath a
// directory.
func New(beneath string) *Memory {
	return &Memory{Beneath: beneath, held: map[string]sources.Workspace{}}
}

// Changeset is the changeset a repository asked for stands at: one
// named for what was asked, so that a test reads which it was.
func Changeset(asked sources.Stored) string {
	at := asked.At
	if asked.Task != "" {
		at = "task/" + asked.Task
	}
	if at == "" {
		at = "main"
	}
	return "changeset-of-" + asked.Repository + "-at-" + at
}

// Mounted is the directory a repository of a workspace is said to be
// mounted at, beneath a directory.
func Mounted(beneath, workspace, repository string) string {
	return path.Join(beneath, workspace, repository)
}

// Fail has every Hold from now on fail with an error; nil for none.
func (m *Memory) Fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fails = err
}

// Hold implements sources.Sources.
func (m *Memory) Hold(_ context.Context, w sources.Workspace) (sources.Held, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.holds = append(m.holds, w)
	if m.fails != nil {
		return sources.Held{}, m.fails
	}
	if _, held := m.held[w.Name]; held {
		return sources.Held{}, errors.New("sourcestest: the workspace is held already, and a workspace is held once")
	}
	var out sources.Held
	for _, asked := range w.Stored {
		out.Repositories = append(out.Repositories, sources.Standing{Name: asked.Name, Repository: asked.Repository, Changeset: Changeset(asked), Roots: slices.Clone(asked.Roots)})
		volume := sources.Volume{Repository: asked.Name, Source: Mounted(m.Beneath, w.Name, asked.Name)}
		if m.Layers != "" {
			volume.Layer = Mounted(m.Layers, w.Name, asked.Name)
		}
		out.Volumes = append(out.Volumes, volume)
	}
	m.held[w.Name] = w
	return out, nil
}

// Release implements sources.Sources.
func (m *Memory) Release(_ context.Context, w sources.Workspace) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releases = append(m.releases, w)
	delete(m.held, w.Name)
	return nil
}

// Holds are the workspaces Hold was asked for, in order, held or not.
func (m *Memory) Holds() []sources.Workspace {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.holds)
}

// Releases are the workspaces Release was asked for, in order.
func (m *Memory) Releases() []sources.Workspace {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.releases)
}

// Held is whether a workspace of a name is held now.
func (m *Memory) Held(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, held := m.held[name]
	return held
}
