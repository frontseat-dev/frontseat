package build_test

import (
	"testing"

	. "github.com/onsi/gomega"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
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
// These two are the profiles there are, an enum of the contracts' shared
// types that a build, a run and a plugin all say; a number that is
// neither is refused.
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

	spec.Expect(build.profile()).To(Equal(typev1.Profile_PROFILE_DEVELOPMENT))
}

// Profiles / A production build can be asked for without releasing.
func TestAProductionBuildCanBeAskedForWithoutReleasing(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(production())

	spec.Expect(build.profile()).To(Equal(typev1.Profile_PROFILE_PRODUCTION))
}

// Profiles / Source that builds for development builds for production.
func TestSourceThatBuildsForDevelopmentBuildsForProduction(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build(production())

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
}

// Profiles / A number that is no profile is refused by name.
func TestANumberThatIsNoProfileIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	_, refusal := spec.workspaceOf("gateway").ask(withProfile(typev1.Profile(7)))

	spec.Expect(refusal).To(BeRefusedSaying("the number asked for is no profile"))
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
