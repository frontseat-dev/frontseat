package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// A build runs every action of the workspace: for a Go module that is
// linting, compiling and testing. The actions run on the grid, never on
// the machine you asked from, and the grid keeps what each one answered.
//
// A build belongs to the daemon, not to whoever asked for it. It starts
// at once, carries on if you go away, and can be followed, picked up
// again or cancelled by anyone who knows its name.
//
// The specs use the company's source, and of it the repository lab, whose
// entities misbehave on purpose: broken, whose code the linter refuses
// and nothing else does; failing, which has a test that fails; slow,
// whose test takes two minutes. The entity unpinned is in a repository
// that pins no tools.
// Builds / A build of healthy source succeeds.
func TestABuildOfHealthySourceSucceeds(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	spec.workspaceOf("gateway").build().succeeded()
}

// Builds / Every action of every module is run.
func TestEveryActionOfEveryModuleIsRun(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build()

	// The lint, the format and the compile of each of its two modules.
	spec.Expect(build.passed()).To(Equal(6))
}

// Builds / A second build is answered from what the grid kept.
func TestASecondBuildIsAnsweredFromWhatTheGridKept(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")
	workspace.build()

	second := workspace.build()

	spec.Expect(second.ranOnAWorker()).To(BeZero())
}

// Builds / What the grid kept still counts as passed.
func TestWhatTheGridKeptStillCountsAsPassed(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")
	workspace.build()

	second := workspace.build()

	spec.Expect(second.passed()).To(Equal(6))
}

// Builds / Source that does not pass the linter fails the build.
func TestSourceThatDoesNotPassTheLinterFailsTheBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("broken").build()

	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}

// Builds / The action that failed is named.
func TestTheActionThatFailedIsNamed(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("broken").build()

	spec.Expect(build.failed()).To(Equal([]string{"//lab/broken:go.lint"}), build.says())
}

// Builds / One failure does not stop the other actions.
func TestOneFailureDoesNotStopTheOtherActions(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("broken").build()

	spec.Expect(build.passed()).To(Equal(2))
}

// Builds / A test that fails ends the build as tests failed.
func TestATestThatFailsEndsTheBuildAsTestsFailed(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("failing").build()

	spec.Expect(build.outcome()).To(Equal("TESTS_FAILED"))
}

// Builds / A build carries on without whoever asked for it.
func TestABuildCarriesOnWithoutWhoeverAskedForIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("slow").start()

	spec.Expect(build.state()).To(Equal("running"))
}

// Builds / A build can be cancelled.
func TestABuildCanBeCancelled(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("slow").start()

	build.cancel()

	spec.Expect(build.outcome()).To(Equal("INTERRUPTED"))
}

// Builds / A build tells its story in events, numbered from one without a gap.
func TestABuildTellsItsStoryInEventsNumberedFromOneWithoutAGap(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build()

	numbers := build.eventNumbers()

	spec.Expect(numbers).NotTo(BeEmpty())
	spec.Expect(numbers).To(Equal(counting(len(numbers))))
}

// Builds / The last event is the end of the build.
func TestTheLastEventIsTheEndOfTheBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build()

	spec.Expect(build.lastEventIsItsEnd()).To(BeTrue())
}

// Builds / Whoever lost the connection picks up where they were.
func TestWhoeverLostTheConnectionPicksUpWhereTheyWere(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build()

	spec.Expect(build.followedAgainAfter(3)).To(Equal(4))
}

// Builds / A build nobody started is not found.
func TestABuildNobodyStartedIsNotFound(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	spec.Expect(spec.emptyWorkspace().askingAfter("build_nobody")).To(BeRefusedSaying("no build has the id"))
}

// Builds / A repository that does not pin its tools cannot be built.
func TestARepositoryThatDoesNotPinItsToolsCannotBeBuilt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("unpinned").build()

	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}
