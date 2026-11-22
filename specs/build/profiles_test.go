package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// Source is built in one of two ways.
//
// Development is for whoever is working on it: quick to build, and easy
// to debug. Production is for what is shipped: compiled for speed and for
// safety.
//
// You do not describe a profile. Each plugin knows what the two mean for
// its own tools, which compiler flags and which of the tool's own
// switches, so "production" means the same thing in every repository.
//
// A profile has a name, and these two are the ones frontseat comes with.
// A repository will be able to name profiles of its own, each built on
// one of the two. None is needed yet, so today there are these two, and
// asking for any other is refused.
//
// A build is a development build, and a release builds for production
// (releases_test.go). You can also ask for a production build without
// releasing anything, to see that what you will release builds.
//
// The two never share an answer. What was compiled for development is not
// handed to a production build; what a profile does not change, such as
// linting, is done once and used by both. Two builds share an action when
// the grid knows it by one name in both: it is then the same action, run
// once and answered to both.
// Profiles / A build is a development build.
func TestABuildIsADevelopmentBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build()

	spec.Expect(build.profile()).To(Equal("development"))
}

// Profiles / A production build can be asked for without releasing.
func TestAProductionBuildCanBeAskedForWithoutReleasing(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(production())

	spec.Expect(build.profile()).To(Equal("production"))
}

// Profiles / Source that builds for development builds for production.
func TestSourceThatBuildsForDevelopmentBuildsForProduction(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(production())

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
}

// Profiles / A profile nobody defined is refused by name.
func TestAProfileNobodyDefinedIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	_, refusal := spec.workspaceOf("gateway").ask(withProfile("staging"))

	spec.Expect(refusal).To(BeRefusedSaying("there is no profile of the name asked for"))
}

// Profiles / Each profile compiles in its own way.
func TestEachProfileCompilesInItsOwnWay(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	forDevelopment, forProduction := spec.gatewayForEachProfile()

	spec.Expect(forProduction.action("//platform/gateway:go.compile")).NotTo(Equal(forDevelopment.action("//platform/gateway:go.compile")))
}

// Profiles / What a profile does not change is done once.
func TestWhatAProfileDoesNotChangeIsDoneOnce(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	forDevelopment, forProduction := spec.gatewayForEachProfile()

	spec.Expect(forProduction.action("//platform/gateway:go.lint")).To(Equal(forDevelopment.action("//platform/gateway:go.lint")))
}
