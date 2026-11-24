// Package sources is the port a control plane holds a workspace's
// stored repositories through: the repositories a remote workspace holds
// from a snapshot store, mounted in its sandbox and not cloned
// (frontseat.workspace.v1alpha1, WorkspaceSpec.stored). What implements
// it knows the store; the control plane knows this port alone, and with
// none refuses a workspace that asks for one, STORE_NOT_SERVED.
// frontseat-enterprise implements it, and gives it to the control plane
// it composes (workspaces.WithSources); this repository holds no
// implementation but the one its tests hold in memory (sourcestest).
//
// Its types are plain, and name nothing of the workspace API's generated
// Go nor of the supervisor's internal source API.
package sources

import "context"

// Sources is what serves a workspace's stored repositories. Each call
// is made by the control plane alone, with the workspace's name and its
// owner, and never on a caller's word alone: the workspace is one the
// control plane keeps, or is about to.
type Sources interface {
	// Hold pins each stored repository a workspace is asked for with,
	// at the changeset its ref or its task stands at now, and mounts it
	// for the workspace. It returns where each stands, and the volumes
	// the workspace's sandbox is given. It is called once, before the
	// workspace is kept and before its sandbox is made; where it fails,
	// nothing is held and no workspace is made.
	Hold(ctx context.Context, w Workspace) (Held, error)
	// Release lets go of what is held for a workspace that ends, once
	// its sandbox is gone, or that was held and could not be kept. It is
	// called until it succeeds, and so again for a workspace it has let
	// go of, and for one it never held: neither is a failure.
	Release(ctx context.Context, w Workspace) error
}

// Workspace is a workspace, as what serves its stored repositories is
// told of it.
type Workspace struct {
	// Name is its name, unique among the workspaces of its control
	// plane.
	Name string
	// Owner is who asked for it first, as the control plane knows its
	// callers.
	Owner string
	// Stored are the stored repositories it is asked for with: those
	// its spec names, and, where the spec names a task, those its
	// entities are in. None for a workspace that is let go of.
	Stored []Stored
	// Land is whether what a passing test in it tested is to be landed
	// on the task of each stored repository it changed.
	Land bool
}

// Stored is a stored repository a workspace is asked for with.
type Stored struct {
	// Repository is the store's name for it.
	Repository string
	// At is a ref of the store, or a changeset by its id; empty for the
	// store's main, and where Task is set.
	At string
	// Name is the workspace's name for it, and the one directory of the
	// workspace it is mounted at: never empty.
	Name string
	// Roots are the directories to take of it; empty for all of it.
	Roots []string
	// Task is the task it works on: its own, or else its workspace's;
	// empty for none.
	Task string
}

// Held is what is held for a workspace.
type Held struct {
	// Repositories are where each stored repository asked for stands,
	// one for each, by the workspace's name for it.
	Repositories []Standing
	// Volumes are what the workspace's sandbox is given: each a
	// directory of the machine its sandboxes are made on, bound at a
	// repository's directory in the workspace.
	Volumes []Volume
}

// Standing is where a stored repository a workspace holds stands.
type Standing struct {
	// Name is the workspace's name for it, as it was asked for.
	Name string
	// Repository is the store's name for it.
	Repository string
	// Changeset is the id of the changeset it is mounted at.
	Changeset string
	// Roots are the directories taken of it; empty for all of it.
	Roots []string
}

// Volume is a directory of the machine a workspace's sandbox is made on
// that the sandbox is given.
type Volume struct {
	// Repository is the workspace's name for the stored repository it
	// is the directory of: it is bound there, at the directory of that
	// name in the workspace.
	Repository string
	// Source is the directory, as the machine names it: an absolute
	// path written plainly.
	Source string
	// Layer is the directory of the repository's layer, as the machine
	// names it, where what mounts the repository keeps one: it is bound
	// where a build looks for it (ports.Mounted, at the repository's
	// name), for the sandbox to read and never to write. It holds the
	// mount's stamp, by which a build takes a file's hash from the mount
	// without reading the file. Empty where there is none: a build then
	// reads every file.
	Layer string
}
