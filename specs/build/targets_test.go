package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// A build covers the whole workspace unless you say otherwise.
//
// You say otherwise by pointing at things, in the words you already have
// for them:
//
//   - a name is an entity: gateway, or in full component:default/gateway;
//   - a path is everything at or under it: platform/, platform/gateway,
//     or a file, which means the module it belongs to;
//   - an action, exactly as frontseat prints it:
//     //platform/gateway:go.test.
//
// You can point at several things, and get all of them.
//
// Separately, you can keep only one kind of action: only test, only lint.
// An action has a name, and plugins use the same names for the same
// things, so test means every plugin's tests.
//
// There are no phases and no order to learn. An action runs when what it
// needs is done.
//
// When what you point at is not there, frontseat says so, and says what
// is nearest.
//
// The specs use a workspace made from gateway, which holds gateway of the
// repository platform and greet of the repository services; some add log
// of platform to it.
// Saying what to build / With nothing said, everything in the workspace is built.
func TestWithNothingSaidEverythingInTheWorkspaceIsBuilt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build()

	spec.Expect(build.covers()).To(Equal([]string{"platform/gateway", "services/greet"}))
}

// Saying what to build / A name is an entity.
func TestANameIsAnEntity(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(of("greet"))

	spec.Expect(build.covers()).To(Equal([]string{"services/greet"}))
}

// Saying what to build / An entity can be named in full.
func TestAnEntityCanBeNamedInFull(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(of("component:default/greet"))

	spec.Expect(build.covers()).To(Equal([]string{"services/greet"}))
}

// Saying what to build / A path is everything at or under it.
func TestAPathIsEverythingAtOrUnderIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway").add("log")

	build := workspace.build(of("platform/"))

	spec.Expect(build.covers()).To(Equal([]string{"platform/gateway", "platform/log"}))
}

// Saying what to build / A file is the module it belongs to.
func TestAFileIsTheModuleItBelongsTo(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(of("platform/gateway/main.go"))

	spec.Expect(build.covers()).To(Equal([]string{"platform/gateway"}))
}

// Saying what to build / An action is named exactly as frontseat prints it.
func TestAnActionIsNamedExactlyAsFrontseatPrintsIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(of("//platform/gateway:go.lint"))

	spec.Expect(build.actions()).To(Equal([]string{"//platform/gateway:go.lint"}))
}

// Saying what to build / Several things can be pointed at.
func TestSeveralThingsCanBePointedAt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway").add("log")

	build := workspace.build(of("greet", "platform/log"))

	spec.Expect(build.covers()).To(Equal([]string{"platform/log", "services/greet"}))
}

// Saying what to build / Only one kind of action is kept.
func TestOnlyOneKindOfActionIsKept(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(only("lint"))

	spec.Expect(build.actions()).To(Equal([]string{"//platform/gateway:go.lint", "//services/greet:go.lint"}))
}

// Saying what to build / One kind of action, of one thing.
func TestOneKindOfActionOfOneThing(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(of("gateway"), only("lint"))

	spec.Expect(build.actions()).To(Equal([]string{"//platform/gateway:go.lint"}))
}

// Saying what to build / A name nothing has is refused, with the nearest.
func TestANameNothingHasIsRefusedWithTheNearest(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(of("gatewya"))

	spec.Expect(build.says()).To(ContainSubstring("there is no entity gatewya in the workspace; did you mean gateway?"))
}

// Saying what to build / A path where nothing is built is refused.
func TestAPathWhereNothingIsBuiltIsRefused(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(of("platform/notes/"))

	spec.Expect(build.says()).To(ContainSubstring("nothing is built at platform/notes"))
}

// Saying what to build / A kind of action nothing has is refused, with the nearest.
func TestAKindOfActionNothingHasIsRefusedWithTheNearest(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(only("lnit"))

	spec.Expect(build.says()).To(ContainSubstring("no action is named lnit; did you mean lint?"))
}

// Saying what to build / Pointing at nothing that is there fails the build.
func TestPointingAtNothingThatIsThereFailsTheBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(of("gatewya"))

	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}
