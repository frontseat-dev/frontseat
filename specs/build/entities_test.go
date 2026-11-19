package build_test

import (
	"testing"

	. "github.com/onsi/gomega"
)

// An entity is what people name: a component, in Backstage's words, by
// its kind, its namespace and its name. What is built belongs to
// entities: a project is owned by the entity whose directory it is in,
// and so is every action of it.
//
// Nobody writes what an entity depends on. An entity depends on another
// because something of the one needs something of the other: a module
// requires a module, an action reads what an action left. That is read from the
// source, and it is what a build goes by.
//
// A catalog still holds the relation, as dependsOn, because a workspace
// is made from the catalog before any source is there. It holds it as
// frontseat wrote it. An entity's catalog file that does not say what
// the source says is out of step: a build fails and says what the file
// should say, and a build asked to fix writes it. A file is never a
// second opinion.
//
// Only what the source accounts for is frontseat's to write: the
// components an entity depends on. What else an entity says it depends
// on, a resource such as a database, is left as it is.
//
// The specs use the company's source: gateway and hello call greet, and
// nothing calls log. Where a spec says so, an entity has a catalog file
// of its own in its directory.
// What an entity depends on / A project is owned by the entity whose directory it is in.
func TestAProjectIsOwnedByTheEntityWhoseDirectoryItIsIn(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	spec.Expect(workspace.ownerOf("platform/gateway")).To(Equal("component:default/gateway"))
}

// What an entity depends on / An action is owned by the entity its project is owned by.
func TestAnActionIsOwnedByTheEntityItsProjectIsOwnedBy(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build()

	spec.Expect(build.ownerOf("//platform/gateway:go.compile")).To(Equal("component:default/gateway"))
}

// What an entity depends on / An entity depends on what its modules require.
func TestAnEntityDependsOnWhatItsModulesRequire(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	spec.Expect(workspace.dependsOn("gateway")).To(Equal([]string{"component:default/greet"}))
}

// What an entity depends on / An entity whose modules require nothing depends on nothing.
func TestAnEntityWhoseModulesRequireNothingDependsOnNothing(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	spec.Expect(workspace.dependsOn("greet")).To(BeEmpty())
}

// What an entity depends on / An entity depends on what its own actions read of another.
func TestAnEntityDependsOnWhatItsOwnActionsReadOfAnother(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("borrower")

	spec.Expect(workspace.dependsOn("borrower")).To(Equal([]string{"component:default/chain"}))
}

// What an entity depends on / A catalog file that says what the source says is in step.
func TestACatalogFileThatSaysWhatTheSourceSaysIsInStep(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().entitySays("gateway", "dependsOn: [component:greet]")

	build := source.workspaceOf("gateway").build()

	spec.Expect(build.outcomeOf("//platform/gateway:catalog.depends-on")).To(Equal("passed"))
}

// What an entity depends on / What frontseat checks itself is no worker's work.
func TestWhatFrontseatChecksItselfIsNoWorkerSWork(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().entitySays("gateway", "dependsOn: [component:greet]")
	workspace := source.workspaceOf("gateway")
	workspace.build()

	again := workspace.build()

	spec.Expect(again.cameFrom("//platform/gateway:catalog.depends-on")).To(Equal("daemon"))
	spec.Expect(again.ranOnAWorker()).To(BeZero())
}

// What an entity depends on / A catalog file that does not say what the source says is out of step.
func TestACatalogFileThatDoesNotSayWhatTheSourceSaysIsOutOfStep(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.outOfStepGateway().build()

	spec.Expect(build.outcomeOf("//platform/gateway:catalog.depends-on")).To(Equal("rewrite"))
}

// What an entity depends on / A catalog file that is out of step fails the build.
func TestACatalogFileThatIsOutOfStepFailsTheBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.outOfStepGateway().build()

	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}

// What an entity depends on / The build says what the catalog file should say.
func TestTheBuildSaysWhatTheCatalogFileShouldSay(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.outOfStepGateway().build()

	spec.Expect(build.saysOf("//platform/gateway:catalog.depends-on")).To(ContainSubstring("it should say it depends on component:default/greet"))
}

// What an entity depends on / A build asked to fix writes what the source says into the catalog file.
func TestABuildAskedToFixWritesWhatTheSourceSaysIntoTheCatalogFile(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.outOfStepGateway()

	workspace.build(fixing())

	spec.Expect(workspace.file("platform/gateway/catalog-info.yaml")).To(ContainSubstring("- component:greet"))
}

// What an entity depends on / A catalog file that was fixed is in step.
func TestACatalogFileThatWasFixedIsInStep(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.outOfStepGateway()
	workspace.build(fixing())

	build := workspace.build()

	spec.Expect(build.outcomeOf("//platform/gateway:catalog.depends-on")).To(Equal("passed"))
}

// What an entity depends on / A component the source does not account for is taken out.
func TestAComponentTheSourceDoesNotAccountForIsTakenOut(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().entitySays("gateway", "dependsOn: [component:greet, component:log]")
	workspace := source.workspaceOf("gateway").add("log")

	workspace.build(fixing())

	spec.Expect(workspace.file("platform/gateway/catalog-info.yaml")).NotTo(ContainSubstring("component:log"))
}

// What an entity depends on / What else an entity says it depends on is left as it is.
func TestWhatElseAnEntitySaysItDependsOnIsLeftAsItIs(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().entitySays("gateway", "dependsOn: [resource:default/orders-db]")
	workspace := source.workspaceOf("gateway")

	workspace.build(fixing())

	spec.Expect(workspace.file("platform/gateway/catalog-info.yaml")).To(ContainSubstring("resource:default/orders-db"))
}
