package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// A unit's next version comes from what changed since its last release:
// the last stable tag of its own reachable from where it is planned. A
// prerelease is never a unit's last release.
//
// A commit is a change of each unit whose members' files it touches, and
// of no other. Under the default strategy, conventional commits, a fix is
// a patch, a feature a minor, and a breaking change a major, or a minor
// below 1.0; a synced group moves by the heaviest change of any member. A
// unit never released is released at its initial version.
//
// What a unit depends on orders the plan: it comes after them. It is
// never released against a change of one of them that is not released:
// of a dependency that has been released before and has changed since,
// both are named, or neither. A dependency never released is source the
// dependent is built from, and refuses nothing. Releasing one unit never
// releases another that was not named; bumping a dependent as its
// dependencies setting says is refused by name.
// Versions / What changed / A first release is released at its initial version.
func TestAFirstReleaseIsReleasedAtItsInitialVersion(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A first release is released at its initial version")
	spec := newScenario(t)

	plan := spec.fromMain().releaseWorkspace().plan(spec.releaseOf("greet"))

	spec.Expect(plan.unit("greet").last()).To(BeEmpty())
	spec.Expect(plan.unit("greet").next()).To(Equal("0.1.0"))
	spec.Expect(plan.unit("greet").why()).To(Equal([]string{"first release"}))
}

// Versions / What changed / A first release is at the initial version a unit says.
func TestAFirstReleaseIsAtTheInitialVersionAUnitSays(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A first release is at the initial version a unit says")
	spec := newScenario(t)

	workspace := spec.fromMain().releases("greet", "initial-version: 1.0.0").releaseWorkspace()

	spec.Expect(workspace.unitOf("greet").next()).To(Equal("1.0.0"))
}

// Versions / What changed / A fix since the last release is a patch.
func TestAFixSinceTheLastReleaseIsAPatch(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A fix since the last release is a patch")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.changesGreet("fix: trim the greeting")

	greet := workspace.unitOf("greet")

	spec.Expect(greet.last()).To(Equal("1.2.3"))
	spec.Expect(greet.next()).To(Equal("1.2.4"))
	spec.Expect(greet.why()).To(Equal([]string{"own change: fix: trim the greeting"}))
	spec.Expect(greet.tag()).To(Equal("greet/v1.2.4"))
}

// Versions / What changed / A feature is a minor.
func TestAFeatureIsAMinor(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A feature is a minor")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.changesGreet("feat: greet by name")

	spec.Expect(workspace.unitOf("greet").next()).To(Equal("1.3.0"))
}

// Versions / What changed / A breaking change is a major.
func TestABreakingChangeIsAMajor(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A breaking change is a major")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.changesGreet("feat!: greet only by name")

	spec.Expect(workspace.unitOf("greet").next()).To(Equal("2.0.0"))
}

// Versions / What changed / A breaking change said in a footer is a major.
func TestABreakingChangeSaidInAFooterIsAMajor(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A breaking change said in a footer is a major")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.changesGreet("feat: greet by name\n\nBREAKING CHANGE: Hello takes a name")

	spec.Expect(workspace.unitOf("greet").next()).To(Equal("2.0.0"))
}

// Versions / What changed / Below 1.0, a breaking change is a minor.
func TestBelow10ABreakingChangeIsAMinor(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / Below 1.0, a breaking change is a minor")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v0.3.0")
	workspace.changesGreet("feat!: greet only by name")

	spec.Expect(workspace.unitOf("greet").next()).To(Equal("0.4.0"))
}

// Versions / What changed / A commit that says no kind of change is a patch.
func TestACommitThatSaysNoKindOfChangeIsAPatch(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A commit that says no kind of change is a patch")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.changesGreet("tidy the greeting")

	spec.Expect(workspace.unitOf("greet").next()).To(Equal("1.2.4"))
}

// Versions / What changed / The heaviest change since the last release is what a unit moves by.
func TestTheHeaviestChangeSinceTheLastReleaseIsWhatAUnitMovesBy(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / The heaviest change since the last release is what a unit moves by")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.changesGreet("fix: trim the greeting")
	workspace.changesGreet("feat: greet by name")
	workspace.changesGreet("fix: spell the name")

	greet := workspace.unitOf("greet")

	spec.Expect(greet.next()).To(Equal("1.3.0"))
	spec.Expect(greet.why()[0]).To(Equal("own change: feat: greet by name"))
}

// Versions / What changed / A commit that touches none of a unit's files does not move it.
func TestACommitThatTouchesNoneOfAUnitSFilesDoesNotMoveIt(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A commit that touches none of a unit's files does not move it")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.2.3")
	workspace.commits("feat: say more", "services/banner/banner.go")

	spec.expectGreetUnchanged(workspace)
}

// Versions / What changed / A commit is a change of each unit whose files it touches.
func TestACommitIsAChangeOfEachUnitWhoseFilesItTouches(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A commit is a change of each unit whose files it touches")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().
		tagged("services", "greet/v1.0.0").
		tagged("services", "kiosk/v1.0.0")
	workspace.commits("feat: greet by name", "services/greet/greet.go", "services/banner/banner.go")

	plan := workspace.plan(spec.releaseOf("greet", "system:kiosk"))

	spec.Expect(plan.unit("greet").why()).To(Equal([]string{"own change: feat: greet by name"}))
	spec.Expect(plan.unit("kiosk").why()).To(Equal([]string{"own change: feat: greet by name"}))
}

// Versions / What changed / A synced group moves by the heaviest change of any member.
func TestASyncedGroupMovesByTheHeaviestChangeOfAnyMember(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A synced group moves by the heaviest change of any member")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "kiosk/v1.0.0")
	workspace.commits("fix: say less", "services/banner/banner.go")
	workspace.commits("feat: tick by name", "services/ticker/ticker.go")

	kiosk := workspace.plan(spec.releaseOf("system:kiosk")).unit("kiosk")

	spec.Expect(kiosk.next()).To(Equal("1.1.0"))
	spec.Expect(kiosk.why()).To(Equal([]string{"own change: feat: tick by name", "own change: fix: say less"}))
}

// Versions / What changed / Another unit's tag is no release of this one.
func TestAnotherUnitSTagIsNoReleaseOfThisOne(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / Another unit's tag is no release of this one")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "banner/v9.0.0").tagged("services", "kiosk/v9.0.0")

	spec.Expect(workspace.unitOf("greet").last()).To(BeEmpty())
}

// Versions / What changed / A tag that is not reachable from where a unit is planned is no release of it.
func TestATagThatIsNotReachableFromWhereAUnitIsPlannedIsNoReleaseOfIt(t *testing.T) {
	t.Skip("Pending requirement: Versions / What changed / A tag that is not reachable from where a unit is planned is no release of it")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace()
	workspace.onBranch("services", "elsewhere").
		changesGreet("fix: somewhere else").
		tagged("services", "greet/v3.0.0").
		backFromBranch("services")
	workspace.tagged("services", "greet/v1.0.0")
	workspace.changesGreet("fix: trim the greeting")

	spec.Expect(workspace.unitOf("greet").last()).To(Equal("1.0.0"))
}

// Versions / What a unit depends on / A unit comes after what it depends on.
func TestAUnitComesAfterWhatItDependsOn(t *testing.T) {
	t.Skip("Pending requirement: Versions / What a unit depends on / A unit comes after what it depends on")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace()

	spec.expectGreetBeforeKiosk(workspace)
}

// Versions / What a unit depends on / What depends on a release is left as it is.
func TestWhatDependsOnAReleaseIsLeftAsItIs(t *testing.T) {
	t.Skip("Pending requirement: Versions / What a unit depends on / What depends on a release is left as it is")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.0.0").tagged("services", "kiosk/v1.0.0")
	workspace.changesGreet("feat: greet by name")

	spec.Expect(workspace.plan(spec.releaseOf("greet")).units()).To(Equal([]string{"greet"}))
}

// Versions / What a unit depends on / A unit is not released against an unreleased change of what it depends on.
func TestAUnitIsNotReleasedAgainstAnUnreleasedChangeOfWhatItDependsOn(t *testing.T) {
	t.Skip("Pending requirement: Versions / What a unit depends on / A unit is not released against an unreleased change of what it depends on")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.0.0").tagged("services", "kiosk/v1.0.0")
	workspace.changesGreet("feat: greet by name")
	workspace.commits("fix: say less", "services/banner/banner.go")

	_, refusal := workspace.planning(spec.releaseOf("system:kiosk"))

	spec.Expect(refusal).To(BeRefusedSaying("kiosk depends on greet, which has changes not released; release both"))
}

// Versions / What a unit depends on / Named together, both are released, and the dependent moves only by its own changes.
func TestNamedTogetherBothAreReleasedAndTheDependentMovesOnlyByItsOwnChanges(t *testing.T) {
	t.Skip("Pending requirement: Versions / What a unit depends on / Named together, both are released, and the dependent moves only by its own changes")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.0.0").tagged("services", "kiosk/v1.0.0")
	workspace.changesGreet("feat: greet by name")
	workspace.commits("fix: say less", "services/banner/banner.go")

	plan := workspace.plan(spec.releaseOf("greet", "system:kiosk"))

	spec.Expect(plan.unit("greet").next()).To(Equal("1.1.0"))
	spec.Expect(plan.unit("kiosk").next()).To(Equal("1.0.1"))
}

// Versions / What a unit depends on / A unit whose dependencies are released as they are is released alone.
func TestAUnitWhoseDependenciesAreReleasedAsTheyAreIsReleasedAlone(t *testing.T) {
	t.Skip("Pending requirement: Versions / What a unit depends on / A unit whose dependencies are released as they are is released alone")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.0.0").tagged("services", "kiosk/v1.0.0")
	workspace.commits("fix: say less", "services/banner/banner.go")

	plan := workspace.plan(spec.releaseOf("system:kiosk"))

	spec.Expect(plan.units()).To(Equal([]string{"kiosk"}))
	spec.Expect(plan.unit("kiosk").next()).To(Equal("1.0.1"))
}

// Versions / What a unit depends on / A unit may depend on one in another repository.
func TestAUnitMayDependOnOneInAnotherRepository(t *testing.T) {
	t.Skip("Pending requirement: Versions / What a unit depends on / A unit may depend on one in another repository")
	spec := newScenario(t)

	source := spec.fromMain().requires("ticker", "log")
	workspace := source.releaseWorkspace()

	plan := workspace.plan(spec.releaseOf("log", "system:kiosk"))

	spec.Expect(plan.units()).To(Equal([]string{"log", "kiosk"}))
	spec.Expect(plan.unit("kiosk").dependsOn()).To(Equal([]string{"log"}))
}

// Versions / What a unit depends on / A dependency never released is source the dependent is built from, and refuses nothing.
func TestADependencyNeverReleasedIsSourceTheDependentIsBuiltFromAndRefusesNothing(t *testing.T) {
	t.Skip("Pending requirement: Versions / What a unit depends on / A dependency never released is source the dependent is built from, and refuses nothing")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "kiosk/v1.0.0")
	workspace.changesGreet("feat: greet by name")
	workspace.commits("fix: say less", "services/banner/banner.go")

	plan := workspace.plan(spec.releaseOf("system:kiosk"))

	spec.Expect(plan.units()).To(Equal([]string{"kiosk"}))
}

// Versions / What a unit depends on / Members of one synced group are no dependency of each other.
func TestMembersOfOneSyncedGroupAreNoDependencyOfEachOther(t *testing.T) {
	t.Skip("Pending requirement: Versions / What a unit depends on / Members of one synced group are no dependency of each other")
	spec := newScenario(t)

	source := spec.fromMain().requires("banner", "ticker")
	workspace := source.releaseWorkspace().tagged("services", "kiosk/v1.0.0")
	workspace.commits("feat: tick by name", "services/ticker/ticker.go")

	kiosk := workspace.plan(spec.releaseOf("system:kiosk")).unit("kiosk")

	spec.Expect(kiosk.dependsOn()).To(BeEmpty())
	spec.Expect(kiosk.why()).To(Equal([]string{"own change: feat: tick by name"}))
}

// Versions / What a unit depends on / Units that depend on each other in a cycle are refused, naming the cycle.
func TestUnitsThatDependOnEachOtherInACycleAreRefusedNamingTheCycle(t *testing.T) {
	t.Skip("Pending requirement: Versions / What a unit depends on / Units that depend on each other in a cycle are refused, naming the cycle")
	spec := newScenario(t)

	source := spec.fromMain().requires("greet", "log").requires("log", "greet")

	_, refusal := source.releaseWorkspace().planning(spec.releaseOf("greet", "log"))

	spec.Expect(refusal).To(BeRefusedSaying("greet and log depend on each other: greet → log → greet"))
}
