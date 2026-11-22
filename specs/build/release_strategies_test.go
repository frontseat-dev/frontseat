package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// Frontseat builds the release graph: its units, what each depends on,
// each one's last release and the commits since. It does not decide how a
// version moves, or what a branch releases: plugins do, and a repository
// can list a plugin of its own that does either its own way.
//
// A branching strategy says what a branch releases of a unit: whether it
// releases at all, stable or prerelease, and held to which line. Three
// come with frontseat. Under Release Flow, the default, a release branch,
// release/1.26, releases, held to the line its name gives, and every
// other branch releases a prerelease of the version it is heading for:
// the default branch of the next minor at least. Under GitHub Flow the
// default branch releases, and any other releases prereleases. A
// prerelease is spelled by frontseat, as the unit's settings say, from
// the branch and from when the commit it is planned at was made.
// GitLab Flow's release branches are as Release Flow's, named
// 1-26-stable.
//
// A versioning strategy says what a unit's next version is and why, told
// its commits. Conventional commits are the default.
//
// A unit names its strategies in spec.frontseat.release, as
// "<plugin>:<name>", inherited like every release setting. Frontseat
// checks what a strategy answers: a version above the last release, on
// the branch's line, and new. The plan says which strategy answered for
// each unit.
// Release strategies / The plan says which strategies answered for a unit.
func TestThePlanSaysWhichStrategiesAnsweredForAUnit(t *testing.T) {
	t.Skip("Pending requirement: Release strategies / The plan says which strategies answered for a unit")
	spec := newScenario(t)

	plan := spec.releasable().releaseWorkspace().plan(spec.releaseOf("greet"))

	spec.Expect(plan.unit("greet").strategies()).To(Equal([]string{"release:conventional-commits", "release:release-flow"}))
}

// Release strategies / A unit that says nothing of how it branches branches by Release Flow.
func TestAUnitThatSaysNothingOfHowItBranchesBranchesByReleaseFlow(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.releasable().releaseWorkspace()

	spec.Expect(workspace.releaseSetting("greet", "branching")).To(Equal(Setting{"release:release-flow", "the default"}))
}

// Release strategies / A strategy no listed plugin serves is refused by name.
func TestAStrategyNoListedPluginServesIsRefusedByName(t *testing.T) {
	t.Skip("Pending requirement: Release strategies / A strategy no listed plugin serves is refused by name")
	spec := newScenario(t)

	workspace := spec.releasable().releases("greet", "versioning: release:guesswork").releaseWorkspace()

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("no plugin the repository lists serves the versioning strategy release:guesswork"))
}

// Release strategies / A strategy is named with its plugin.
func TestAStrategyIsNamedWithItsPlugin(t *testing.T) {
	t.Skip("Pending requirement: Release strategies / A strategy is named with its plugin")
	spec := newScenario(t)

	workspace := spec.releasable().releases("greet", "versioning: conventional-commits").releaseWorkspace()

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("spec.frontseat.release.versioning must be <plugin>:<name>"))
}

// Release strategies / GitHub Flow / The default branch releases what it releases as stable.
func TestTheDefaultBranchReleasesWhatItReleasesAsStable(t *testing.T) {
	t.Skip("Pending requirement: Release strategies / GitHub Flow / The default branch releases what it releases as stable")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.changesGreet("feat: greet by name")

	greet := workspace.unitOf("greet")

	spec.Expect(greet.next()).To(Equal("1.3.0"))
	spec.Expect(greet.channel()).To(Equal("latest"))
}

// Release strategies / GitHub Flow / Another branch releases a prerelease, named by the branch.
func TestAnotherBranchReleasesAPrereleaseNamedByTheBranch(t *testing.T) {
	t.Skip("Pending requirement: Release strategies / GitHub Flow / Another branch releases a prerelease, named by the branch")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.onBranch("services", "feat/greet-by-name").
		changesGreet("feat: greet by name")

	greet := workspace.unitOf("greet")

	spec.Expect(greet.next()).To(MatchRegexp(`^1\.3\.0-feat-greet-by-name-\d{14}$`))
	spec.Expect(greet.channel()).To(Equal("feat-greet-by-name"))
}

// Release strategies / GitHub Flow / A later commit's prerelease comes after an earlier one's.
func TestALaterCommitSPrereleaseComesAfterAnEarlierOneS(t *testing.T) {
	t.Skip("Pending requirement: Release strategies / GitHub Flow / A later commit's prerelease comes after an earlier one's")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.onBranch("services", "next").
		changesGreet("feat: greet by name")
	earlier := workspace.unitOf("greet").next()
	workspace.changesGreet("fix: spell the name")

	spec.Expect(workspace.unitOf("greet").next() > earlier).To(BeTrue())
}

// Release strategies / GitHub Flow / A prerelease is no last release of what comes after it.
func TestAPrereleaseIsNoLastReleaseOfWhatComesAfterIt(t *testing.T) {
	t.Skip("Pending requirement: Release strategies / GitHub Flow / A prerelease is no last release of what comes after it")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.onBranch("services", "next").
		changesGreet("feat: greet by name").
		tagged("services", "greet/v1.3.0-next-20261120101500").
		backFromBranch("services")
	workspace.changesGreet("fix: trim the greeting")

	spec.Expect(workspace.unitOf("greet").last()).To(Equal("1.2.3"))
}

// Release strategies / Release Flow / The default branch releases a prerelease of the next minor.
func TestTheDefaultBranchReleasesAPrereleaseOfTheNextMinor(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.releasable().releaseWorkspace().tagged("services", "greet/v1.26.0")
	workspace.changesGreet("fix: trim the greeting")

	greet := workspace.unitOf("greet")

	spec.Expect(greet.next()).To(MatchRegexp(`^1\.27\.0-main-\d{14}$`))
	spec.Expect(greet.last()).To(Equal("1.26.0"))
	spec.Expect(greet.channel()).To(Equal("main"))
}

// Release strategies / Release Flow / Another branch releases a prerelease of what its commits call for.
func TestAnotherBranchReleasesAPrereleaseOfWhatItsCommitsCallFor(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.releasable().releaseWorkspace().tagged("services", "greet/v1.26.0")
	workspace.onBranch("services", "fix/trim").
		changesGreet("fix: trim the greeting")

	greet := workspace.unitOf("greet")

	spec.Expect(greet.next()).To(MatchRegexp(`^1\.26\.1-fix-trim-\d{14}$`))
	spec.Expect(greet.channel()).To(Equal("fix-trim"))
}

// Release strategies / Release Flow / A release branch releases, held to the line its name gives.
func TestAReleaseBranchReleasesHeldToTheLineItsNameGives(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.26").tagged("services", "greet/v1.26.0").
		changesGreet("fix: trim the greeting")

	spec.Expect(workspace.unitOf("greet").next()).To(Equal("1.26.1"))
}

// Release strategies / Release Flow / A branch cut for a line not yet released releases the line's first version.
func TestABranchCutForALineNotYetReleasedReleasesTheLineSFirstVersion(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.releasable().releaseWorkspace().tagged("services", "greet/v1.26.3")
	workspace.onBranch("services", "release/1.27").
		changesGreet("fix: trim the greeting")

	spec.Expect(workspace.unitOf("greet").next()).To(Equal("1.27.0"))
}

// Release strategies / Release Flow / A unit never released is released, on a release branch, at the line's first version.
func TestAUnitNeverReleasedIsReleasedOnAReleaseBranchAtTheLineSFirstVersion(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("0.77").
		changesGreet("fix: trim the greeting")

	greet := workspace.unitOf("greet")

	spec.Expect(greet.next()).To(Equal("0.77.0"))
	spec.Expect(greet.why()).To(ContainElement("first release"))
}

// Release strategies / Release Flow / An initial version that is not on a release branch's line is refused, naming both.
func TestAnInitialVersionThatIsNotOnAReleaseBranchSLineIsRefusedNamingBoth(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	sound := spec.releasable().releases("greet", "initial: 0.77.0").releaseWorkspace()
	sound.onBranch("services", "release/0.77")
	workspace := spec.releasable().releases("greet", "initial: 0.1.0").releaseWorkspace()
	workspace.onBranch("services", "release/0.77")

	allowed := sound.unitOf("greet")
	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(allowed.next()).To(Equal("0.77.0"))
	spec.Expect(refusal).To(BeRefusedSaying("greet: spec.frontseat.release.initial: 0.1.0 is not on the line 0.77 of release/0.77"))
}

// Release strategies / Release Flow / A line cut after another comes after it, though that one's tags are on its own branch.
func TestALineCutAfterAnotherComesAfterItThoughThatOneSTagsAreOnItsOwnBranch(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.26").
		changesGreet("fix: steady the greeting").tagged("services", "greet/v1.26.0").
		changesGreet("fix: steady it again").tagged("services", "greet/v1.26.1")
	workspace.switchTo("services", "main").
		changesGreet("feat: greet by name").
		onBranch("services", "release/1.27")

	greet := workspace.unitOf("greet")

	spec.Expect(greet.last()).To(Equal("1.26.1"))
	spec.Expect(greet.next()).To(Equal("1.27.0"))
	spec.Expect(greet.why()).To(Equal([]string{"own change: feat: greet by name"}))
}

// Release strategies / Release Flow / An earlier line is still released on its own branch, after a later line is.
func TestAnEarlierLineIsStillReleasedOnItsOwnBranchAfterALaterLineIs(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.26").
		changesGreet("fix: steady the greeting").tagged("services", "greet/v1.26.0")
	workspace.switchTo("services", "main").
		changesGreet("feat: greet by name").
		onBranch("services", "release/1.27").tagged("services", "greet/v1.27.0")
	workspace.switchTo("services", "release/1.26").
		changesGreet("fix: trim the greeting")

	greet := workspace.unitOf("greet")

	spec.Expect(greet.last()).To(Equal("1.26.0"))
	spec.Expect(greet.next()).To(Equal("1.26.1"))
}

// Release strategies / Release Flow / What leaves a release branch's line is refused.
func TestWhatLeavesAReleaseBranchSLineIsRefused(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.26").tagged("services", "greet/v1.26.0").
		changesGreet("feat: greet by name")

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("greet 1.27.0 leaves the line 1.26 of release/1.26"))
}

// Release strategies / GitLab Flow / The default branch releases nothing.
func TestReleaseStrategiesGitLabFlowTheDefaultBranchReleasesNothing(t *testing.T) {
	t.Skip("Pending requirement: Release strategies / GitLab Flow / The default branch releases nothing")
	spec := newScenario(t)

	greet := spec.stable("fix: trim the greeting").unitOf("greet")

	spec.Expect(greet.next()).To(BeEmpty())
	spec.Expect(greet.why()).To(ContainElement(HavePrefix("branch: ")))
}

// Release strategies / GitLab Flow / What leaves a stable branch's line is refused.
func TestWhatLeavesAStableBranchSLineIsRefused(t *testing.T) {
	t.Skip("Pending requirement: Release strategies / GitLab Flow / What leaves a stable branch's line is refused")
	spec := newScenario(t)

	workspace := spec.stable("feat: greet by name").switchTo("services", "2-3-stable").cherryPicks("services", "main")

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("greet 2.4.0 leaves the line 2.3 of 2-3-stable"))
}

// Release strategies / GitLab Flow / A branch that is neither the default nor stable releases nothing.
func TestABranchThatIsNeitherTheDefaultNorStableReleasesNothing(t *testing.T) {
	t.Skip("Pending requirement: Release strategies / GitLab Flow / A branch that is neither the default nor stable releases nothing")
	spec := newScenario(t)

	workspace := spec.stable("fix: trim the greeting").onBranch("services", "feature/names")
	workspace.changesGreet("fix: spell the name")

	spec.Expect(workspace.unitOf("greet").next()).To(BeEmpty())
}

func (spec *scenario) stable(message string) *Workspace {
	spec.Helper()
	workspace := spec.releasable().releases("greet", "branching: release:gitlab-flow").releaseWorkspace().
		tagged("services", "greet/v2.3.0").
		onBranch("services", "2-3-stable").
		backFromBranch("services")
	return workspace.commits(message, "services/greet/greet.go")
}
