package build_test

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

// An image apko builds is a configuration and the lock apko wrote of it
// (`apko lock`), side by side. The lock is what finds the image: a
// configuration with no lock is not built. For each architecture the lock
// holds packages of, the image is built as an OCI layout by `apko build`,
// from the lock alone, offline, with the apko the repository pins.
//
// Packages come from the lock alone. It holds no checksum of a whole
// package, only of the sections of one, so each package is a shared fetch
// of its own that runs no tool: the daemon asks the grid's asset service
// for the package by its address, the service fetches it from its
// origin, and the fetch cuts the control and data sections out by the
// lock's ranges, checks each against the lock's checksum, and keeps them
// where apko's cache reads them, with no network. A signing key the lock holds whole is a fetch
// that downloads nothing. apko checks the configuration against the lock,
// and each section again, when it builds.
//
// A package that is not what the lock says is not kept and nothing is
// built with it; a configuration changed since it was locked is not
// built. A change to one package of the lock fetches that package again
// and no other. A configuration that would have apko read what no action
// is given, another file, a base image, a repository or a key on disk, is
// refused by name before anything runs.

// The fixture's images and the fetches of the arm64 one.
const (
	apkoArm64      = "//app:apko.image/aarch64"
	apkoAmd64      = "//app:apko.image/x86-64"
	apkoKey        = "//:apko.fetch/packages.wolfi.dev/os/wolfi-signing.rsa.pub"
	apkoBaselayout = "//:apko.fetch/packages.wolfi.dev/os/aarch64/wolfi-baselayout-20230201-r31.apk"
	apkoBundle     = "//:apko.fetch/packages.wolfi.dev/os/aarch64/ca-certificates-bundle-20261020-r0.apk"
	// The sha256 the lock holds of the data section of the arm64
	// wolfi-baselayout, and one that it is not.
	apkoHeld  = "sha256-5RP/3NK4uU2QTgSOj9J8QxZHFkBTX6Rna6y2uCrw36o="
	apkoWrong = "sha256-6RP/3NK4uU2QTgSOj9J8QxZHFkBTX6Rna6y2uCrw36o="
)

// Images built by apko / An image is a project found by its lock, with a build for each architecture the lock holds packages of.
func TestAnImageIsAProjectFoundByItsLockWithABuildForEachArchitectureTheLockHoldsPackagesOf(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.apkoWorkspace()
	spec.Expect(rootsOf(workspace.projects())).To(ConsistOf("app"))
	built := workspace.build(of(apkoArm64, apkoAmd64))
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.diagnostics())
	spec.Expect(built.outcomeOf(apkoArm64)).To(Equal("passed"))
	spec.Expect(built.outcomeOf(apkoAmd64)).To(Equal("passed"))
}

// Images built by apko / A configuration with no lock beside it is no image.
func TestAConfigurationWithNoLockBesideItIsNoImage(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.apkoWorkspace()
	// The component's own action reads the image, so it goes too.
	workspace.remove("app/catalog-info.yaml").remove("app/apko.lock.json")
	spec.Expect(workspace.projects()).To(BeEmpty())
}

// Images built by apko / Each package the lock holds is fetched by an action of its own, which belongs to no image and reaches nothing.
func TestEachPackageTheLockHoldsIsFetchedByAnActionOfItsOwnWhichBelongsToNoImageAndReachesNothing(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	built := spec.apkoWorkspace().build(of(apkoArm64))
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.diagnostics())
	spec.Expect(built.needsOf(apkoArm64)).To(ConsistOf(apkoKey, apkoBaselayout, apkoBundle))
	for _, fetch := range []string{apkoKey, apkoBaselayout, apkoBundle} {
		spec.Expect(built.outcomeOf(fetch)).To(Equal("passed"), fetch)
		spec.Expect(built.ownerOf(fetch)).To(BeEmpty(), fetch)
		spec.Expect(built.needsOf(fetch)).To(BeEmpty(), fetch)
	}
	// The other architecture's packages are no part of this build.
	spec.Expect(built.actions()).To(ConsistOf(apkoArm64, apkoKey, apkoBaselayout, apkoBundle))
}

// Images built by apko / The image is left as an OCI layout, for whatever reads the build's action.
func TestTheImageIsLeftAsAnOCILayoutForWhateverReadsTheBuildSAction(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	built := spec.apkoWorkspace().build(of("//app:custom.layout"))
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.diagnostics())
	printed := built.printed("//app:custom.layout")
	spec.Expect(printed).To(ContainSubstring(`"imageLayoutVersion"`))
	spec.Expect(printed).To(ContainSubstring("application/vnd.oci.image.index.v1+json"))
	spec.Expect(printed).To(ContainSubstring(`"architecture":"arm64"`))
}

// Images built by apko / With nothing changed, a build is of the same actions and none runs again.
func TestWithNothingChangedABuildIsOfTheSameActionsAndNoneRunsAgain(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.apkoWorkspace()
	before := workspace.build(of(apkoArm64)).succeeded()
	again := workspace.build(of(apkoArm64))
	for _, label := range []string{apkoArm64, apkoKey, apkoBaselayout, apkoBundle} {
		spec.Expect(again.action(label)).To(Equal(before.action(label)), label)
		spec.Expect(again.cameFrom(label)).To(Equal("grid_cache"), label)
	}
}

// Images built by apko / A package that is not what the lock says is not kept, and no image is built with it; the other packages are as they were.
func TestAPackageThatIsNotWhatTheLockSaysIsNotKeptAndNoImageIsBuiltWithItTheOtherPackagesAreAsTheyWere(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.apkoWorkspace()
	before := workspace.build(of(apkoArm64)).succeeded()
	spec.Expect(workspace.file("app/apko.lock.json")).To(ContainSubstring(apkoHeld))
	workspace.write("app/apko.lock.json", strings.Replace(workspace.file("app/apko.lock.json"), apkoHeld, apkoWrong, 1))
	after := workspace.build(of(apkoArm64))
	spec.Expect(after.outcome()).NotTo(Equal("SUCCESS"))
	spec.Expect(after.actions()).To(ContainElement(apkoBaselayout), after.says())
	spec.Expect(after.outcomeOf(apkoBaselayout)).To(Equal("failed"))
	spec.Expect(after.printed(apkoBaselayout)).To(SatisfyAll(ContainSubstring("wolfi-baselayout-20230201-r31.apk"), ContainSubstring("the lock holds")))
	spec.Expect(after.outcomeOf(apkoArm64)).To(Equal("skipped"))
	// The other package, which did not change, is as it was.
	spec.Expect(after.action(apkoBundle)).To(Equal(before.action(apkoBundle)))
	spec.Expect(after.outcomeOf(apkoBundle)).To(Equal("passed"))
	// And the lock as it was builds again: nothing of the refusal
	// was kept.
	workspace.write("app/apko.lock.json", strings.Replace(workspace.file("app/apko.lock.json"), apkoWrong, apkoHeld, 1))
	restored := workspace.build(of(apkoArm64))
	spec.Expect(restored.outcome()).To(Equal("SUCCESS"), restored.diagnostics())
}

// Images built by apko / A configuration changed since it was locked is not built.
func TestAConfigurationChangedSinceItWasLockedIsNotBuilt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.apkoWorkspace()
	spec.Expect(workspace.build(of(apkoArm64)).outcome()).To(Equal("SUCCESS"))
	workspace.write("app/apko.yaml", workspace.file("app/apko.yaml")+"\nentrypoint:\n  command: /bin/sh\n")
	built := workspace.build(of(apkoArm64))
	spec.Expect(built.outcome()).NotTo(Equal("SUCCESS"))
	spec.Expect(built.actions()).To(ContainElement(apkoArm64), built.says())
	spec.Expect(built.outcomeOf(apkoArm64)).To(Equal("failed"))
	spec.Expect(built.printed(apkoArm64)).To(ContainSubstring("lock file"))
}

// Images built by apko / A configuration that reads what no action is given is refused by name, before anything runs.
func TestAConfigurationThatReadsWhatNoActionIsGivenIsRefusedByNameBeforeAnythingRuns(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.apkoWorkspace()
	spec.Expect(rootsOf(workspace.projects())).To(ConsistOf("app"))
	workspace.write("app/apko.yaml", strings.Replace(workspace.file("app/apko.yaml"), "- https://packages.wolfi.dev/os\n", "- https://packages.wolfi.dev/os\n    - ./packages\n", 1))
	failure := workspace.projectFailure()
	spec.Expect(failure).NotTo(BeNil())
	spec.Expect(failure.GetMessage()).To(SatisfyAll(ContainSubstring("./packages"), ContainSubstring("not served yet")))
}
