package graph

import (
	"path"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
)

// Provide is something a project can be required by, and what a project
// that requires it is handed.
type Provide struct {
	// Coordinate is what it is required by.
	Coordinate model.Coordinate
	// Sources are the patterns of the files it hands, relative to the
	// project; none for a project that hands nothing.
	Sources []string
	// Generated retains the source selections accepting generated contributions.
	Generated []pattern.Selection
	// Artifacts are exact outputs of this project's finite actions.
	Artifacts []Produced
}

// Produced is an output a provider hands to consumers of its coordinate.
type Produced struct {
	Action, Output string
	// Optional is inherited from the producer's output declaration.
	Optional bool
}

// Requirement is something a project requires.
type Requirement struct {
	// Coordinate is what is required.
	Coordinate model.Coordinate
	// Origin is where the ecosystem's own files say so: a file relative
	// to the workspace, and its line.
	Origin string
	// Version is what the project's lock resolves it to, as the plugin
	// read it; empty where no lock says one.
	Version string
}

// Project is one project of a workspace, as a plugin found it: a Go
// module, a Maven module, a package of a JavaScript workspace.
type Project struct {
	// Repository is the workspace's name for the repository it is in.
	Repository string
	// Root is its directory, relative to the repository.
	Root string
	// Plugin is the plugin that found it.
	Plugin string
	// Kind is what it is in its ecosystem; Name is its name there.
	Kind, Name string
	// Manifest is the file it was found from, relative to the repository.
	Manifest string
	// Options are the options it is built with: every option its plugin
	// takes, each with the value that holds for it and where it is set.
	Options []config.Option
	// Platforms are the platforms it is shipped for, as its entity says.
	Platforms []string
	// Provides are what it can be required by.
	Provides []Provide
	// Requires are what it requires, as its ecosystem's own files say.
	Requires []Requirement
	// AlwaysArranged is whether its plugin arranges it before every run,
	// handed something or not.
	AlwaysArranged bool
}

// Path is where the project is in the workspace: its repository and its
// directory there.
func (p Project) Path() string { return path.Join(p.Repository, p.Root) }

// Ref identifies this project independently of other plugins in its directory.
func (p Project) Ref() model.ProjectRef {
	return model.NewProjectRef(p.Repository, p.Root, p.Plugin)
}
