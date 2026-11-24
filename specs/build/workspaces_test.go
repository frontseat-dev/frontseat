package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// A workspace is the source a supervisor holds and a build daemon
// builds. You make one by naming what you want to work on: an entity
// from the catalog, or a repository. The supervisor finds where that
// source lives and clones it.
//
// Naming an entity is the usual way. The catalog is a Backstage catalog,
// asked over its API: it says which repository and directory an entity
// lives in, and which other entities it depends on. The entity and
// everything it depends on are brought, wherever those live, and of each
// repository only the directories they need.
//
// Nobody writes down what a workspace is made of. You ask for what you
// want, and to get the same workspace as a colleague you ask for the same
// thing. The catalog already groups entities, so you can ask for a whole
// system, or for everything a team owns, and it stays right as the
// catalog changes.
//
// A supervisor does not have to be the one that brings the source. On a
// platform, a sandbox can be given its source before the supervisor in
// it starts: the platform clones what the entities need and writes down
// what it brought, in the same words a supervisor uses. A build daemon
// that starts in such a workspace builds it as it stands.
//
// The specs use the company's source, which support_test.go describes.
// Workspaces / An entity brings its own directory and those it depends on.
func TestAnEntityBringsItsOwnDirectoryAndThoseItDependsOn(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("hello")

	spec.Expect(workspace.directoriesOf("services")).To(ConsistOf("greet", "hello"))
}

// Workspaces / Nothing an entity does not need is brought.
func TestNothingAnEntityDoesNotNeedIsBrought(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("hello")

	spec.Expect(workspace.has("services/docs")).To(BeFalse())
}

// Workspaces / An entity that depends on another repository brings that one too.
func TestAnEntityThatDependsOnAnotherRepositoryBringsThatOneToo(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	spec.Expect(workspace.repositories()).To(Equal([]string{"platform", "services"}))
}

// Workspaces / Of each repository, only what the entity needs is brought.
func TestOfEachRepositoryOnlyWhatTheEntityNeedsIsBrought(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	spec.Expect(workspace.directoriesOf("platform")).To(ConsistOf("gateway"))
	spec.Expect(workspace.directoriesOf("services")).To(ConsistOf("greet"))
}

// Workspaces / A whole repository is brought when asked for.
func TestAWholeRepositoryIsBroughtWhenAskedFor(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.wholeWorkspaceOf("hello")

	spec.Expect(workspace.holdsAllOf("services")).To(BeTrue())
}

// Workspaces / A repository can be named directly, and is brought whole.
func TestARepositoryCanBeNamedDirectlyAndIsBroughtWhole(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOfRepository("services")

	spec.Expect(workspace.holdsAllOf("services")).To(BeTrue())
}

// Workspaces / More can be added to a workspace later.
func TestMoreCanBeAddedToAWorkspaceLater(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	workspace.add("log")

	spec.Expect(workspace.directoriesOf("platform")).To(ConsistOf("gateway", "log"))
}

// Workspaces / Adding an entity keeps what was already there.
func TestAddingAnEntityKeepsWhatWasAlreadyThere(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	workspace.add("hello")

	spec.Expect(workspace.directoriesOf("services")).To(ConsistOf("greet", "hello"))
}

// Workspaces / A system brings everything in it.
func TestASystemBringsEverythingInIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("system:storefront")

	spec.Expect(workspace.directoriesOf("platform")).To(ConsistOf("gateway"))
	spec.Expect(workspace.directoriesOf("services")).To(ConsistOf("greet", "hello"))
}

// Workspaces / A team brings everything it owns.
func TestATeamBringsEverythingItOwns(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("group:team-edge")

	spec.Expect(workspace.directoriesOf("platform")).To(ConsistOf("gateway", "log"))
}

// Workspaces / A system with nothing in it is refused by name.
func TestASystemWithNothingInItIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	spec.Expect(spec.makingAWorkspaceOf("system:warehouse")).To(BeRefusedSaying("nothing is in system:default/warehouse"))
}

// Workspaces / An entity the catalog cannot place is refused, saying why.
func TestAnEntityTheCatalogCannotPlaceIsRefusedSayingWhy(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	spec.Expect(spec.makingAWorkspaceOf("unplaced")).To(BeRefusedSaying("has no backstage.io/source-location"))
}

// Workspaces / An entity the catalog does not know is refused by name.
func TestAnEntityTheCatalogDoesNotKnowIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	spec.Expect(spec.makingAWorkspaceOf("nobody")).To(BeRefusedSaying("component:default/nobody"))
}

// Workspaces / A workspace something else made is built as it stands.
func TestAWorkspaceSomethingElseMadeIsBuiltAsItStands(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceSomethingElseMade("gateway", "platform")

	spec.Expect(workspace.build().covers()).To(Equal([]string{"platform/gateway"}))
}

// Workspaces / What something else brought is what the workspace holds.
func TestWhatSomethingElseBroughtIsWhatTheWorkspaceHolds(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceSomethingElseMade("gateway", "platform")

	spec.Expect(workspace.directoriesOf("platform")).To(ConsistOf("gateway"))
}

// Workspaces / A build covers exactly what the workspace holds.
func TestABuildCoversExactlyWhatTheWorkspaceHolds(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	spec.Expect(workspace.build().covers()).To(Equal([]string{"platform/gateway", "services/greet"}))
}

// Workspaces / A workspace that holds nothing cannot be built.
func TestAWorkspaceThatHoldsNothingCannotBeBuilt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	_, refusal := spec.emptyWorkspace().ask()

	spec.Expect(refusal).To(BeRefusedSaying("holds no repository"))
}
