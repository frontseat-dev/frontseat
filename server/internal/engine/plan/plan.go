package plan

import (
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
)

// Action is one action of a workspace, as a plugin declared it and as the
// host addresses it.
type Action struct {
	// Label is its address.
	Label model.Label
	// Repository is the workspace's name for the repository its project
	// is in.
	Repository string
	// ProjectRoot is its project's directory, relative to the repository.
	ProjectRoot string
	// Declared is what the plugin said of it.
	Declared *pluginv1.Action
	// Context is what the plugin was told of the repository when it
	// declared the action: the same is told it when it is asked what
	// the action found. Nil for an action no plugin declared.
	Context *pluginv1.Context
	// Sources are the source selections it reads, each with its literal
	// repository-relative base. Plugin inputs keep the project as their
	// base; catalog test inputs keep their Component's directory.
	Sources []pattern.Group
	// References are the inputs that name what other actions left, as
	// written; Resolve turns them into Reads and Needs.
	References []Reference
	// Reads are what it reads of what other actions left: all of an
	// action, or one output of one. What a generated-source input
	// selects is in Artifacts instead.
	Reads []Read
	// IdleWithout are the outputs its own plugin named one by one, for an
	// action that has nothing to do without what it reads
	// (Action.idle_without_inputs): where one was not left, the action
	// is not run. They are kept apart from Reads, which what a catalog
	// file adds may extend, and may fold into a read of a whole action:
	// neither says what the action's tool has nothing to do without.
	IdleWithout []Read
	// Needs are the actions it waits for: it runs when each has passed.
	// Each is one it reads something of (Reads), or whose generated
	// source its inputs select (Artifacts); nothing else is a need.
	Needs []model.Label
	// ArtifactOnly are inferred ordering edges with selective output bindings;
	// they do not import every output at its original path.
	ArtifactOnly []model.Label
	Artifacts    []ArtifactInput
	// GeneratedSources excludes authoritative generated paths from the snapshot.
	GeneratedSources []GeneratedSource
	// WorkspaceFiles are files above the project, relative to its
	// repository, that its tool reads for the whole workspace: staged
	// where they are, and inputs of the action.
	WorkspaceFiles []string
	// Handed are the files of other projects it is handed: the sources of
	// what its project requires.
	Handed []Handed
	// Arranged are the files its plugin wrote so that its tool finds
	// what it was handed.
	Arranged []Arranged
	// Environment is catalog runtime data injected as written inside
	// confinement. Duplicate adapter, mise or service values are refused.
	Environment map[string]string
	// InStep says the action is the host's own, and runs on no grid: it
	// holds an entity's catalog file to what the source says.
	InStep *InStep
	// Beside is what runs beside a test in a runner sandbox of its own,
	// where its component depends on services started for tests; nil
	// where nothing does.
	Beside *Beside
}

// Beside is a test's services, as the grid's runner is told them and
// the test is told of them.
type Beside struct {
	// Sandbox is the runner sandbox, encoded (frontseat.runner.v1alpha1.
	// RunnerSandbox): written into the action's root, and so in its key.
	Sandbox []byte
	// Environment is what the services export, as the test is given it.
	Environment map[string]string
	// Programs are prepared in their own roots and never executed as build
	// actions. Names match the runner description's service names.
	Programs map[string]Action
	// TestNeeds preserves the test's own input dependencies before the
	// programs' finite preparation dependencies are added to scheduling.
	TestNeeds []model.Label
}

// InStep is a catalog file held to what the source says of the entities
// it describes. A file may describe several, and is one action for all
// of them: it is one file to read and, where it is out of step, one to
// write.
type InStep struct {
	// File is the catalog file, relative to its repository.
	File string
	// Entities are the entities the file describes that say what they
	// depend on, in the order the workspace holds them.
	Entities []Described
	// From is the hash of the file as it was read, which what it should
	// say was worked out from.
	From string
	// Fixed is the file saying what it should of every entity; nil for
	// one in step.
	Fixed []byte
}

// Described is an entity as its catalog file describes it.
type Described struct {
	// Entity is the entity.
	Entity entity.Ref
	// Inverse says this is the explicit dependencyOf field: components that
	// depend on this entity, rather than components it depends on.
	Inverse bool
	// Says are the components the file says the entity depends on, and
	// Should the ones the source says it does, each sorted as references
	// are written. The entity is in step where the two are the same.
	Says, Should []entity.Ref
}

// Handed is files of another project that an action is handed.
type Handed struct {
	// Repository and Root are the project they are of.
	Repository, Root string
	// Patterns are the files, relative to that project.
	Patterns  []string
	Generated []pattern.Selection
	// Artifacts are declared outputs of the providing project. Their paths
	// remain relative to that project under Into, alongside any source.
	Artifacts []Read
	// Into is the directory, relative to the action's own project, they
	// are placed in.
	Into string
}

// Arranged is a file a plugin wrote for an action's tool.
type Arranged struct {
	// Path is where it sits, relative to the action's repository.
	Path string
	// Content is its bytes.
	Content []byte
	// Executable is whether it may be run.
	Executable bool
}

// Outputs are the paths of what it leaves, relative to its project,
// without the slash a directory's is written with.
func (a Action) Outputs() []string {
	var out []string
	for _, output := range a.Declared.GetOutputs() {
		out = append(out, strings.TrimSuffix(output.GetPath(), "/"))
	}
	return out
}

// Name is the action's name within its plugin: "test", "lint".
func (a Action) Name() string { return a.Declared.GetName() }

// IsShared reports whether the action belongs to no project: a fetch of
// one thing a lock holds, the repository's, run once for every project
// that locks it alike. No entity owns it, and it is the project of no
// file.
func (a Action) IsShared() bool { return a.Declared.GetShared() }

// IsTest reports whether the action is a test: one whose failure is a
// test that failed, and not a build that broke.
func (a Action) IsTest() bool { return a.Declared.GetTest() }

// HasSources distinguishes source-reading programs from artifact-only ones,
// including a declaration normalized to an empty source group.
func (a Action) HasSources() bool {
	for _, group := range a.Sources {
		if len(group.Patterns) > 0 || len(group.Generated) > 0 {
			return true
		}
	}
	return false
}

// IsCheck reports whether the action is a check: one asked what it found
// once it has ended.
func (a Action) IsCheck() bool { return a.Declared.GetCheck() }

// IsPackage reports whether the action makes what its project ships:
// each file it leaves is an artifact of a release.
func (a Action) IsPackage() bool { return a.Declared.GetPackage() }

// IsProgram reports whether the action is a program to run: no step of
// a build, which no build runs.
func (a Action) IsProgram() bool { return a.Declared.GetProgram() }

// IsPublish reports whether the action publishes what a release
// shipped: no step of a build, which a release runs where the daemon is.
func (a Action) IsPublish() bool { return a.Declared.GetPublish() != nil }

// Built reports whether the action is a step of a build: neither a
// program to run nor what publishes a release.
func (a Action) Built() bool { return !a.IsProgram() && !a.IsPublish() }

// Rewrites are the patterns, relative to its project, of the inputs the
// action's tool may change on the copy it is given.
func (a Action) Rewrites() []string { return a.Declared.GetRewrites() }

// Of returns what a run does. Of a workspace's actions it takes those
// its targets point at, keeping only the names in only: these are what
// was asked for. It adds whatever those need, each after all it needs:
// these are what runs.
//
// A need the workspace does not hold, actions that need each other in a
// circle, or a target that points at nothing is a model.Mistake.
func Of(actions []Action, entities []record.Located, targets, only []string) (runs, asked []Action, err error) {
	if err := Check(actions); err != nil {
		return nil, nil, err
	}
	asked, err = Pointed(actions, entities, targets, only)
	if err != nil {
		return nil, nil, err
	}
	return With(actions, asked), asked, nil
}
