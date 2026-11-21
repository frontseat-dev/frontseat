package build_test

import (
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"testing"

	. "github.com/onsi/gomega"
)

// You release entities by pointing at them: a component, alone, or a
// system or a domain, as a group. What is released is a unit: what gets
// one version and one tag. A unit's members are in one repository.
//
// Nobody writes a group. A system is one: its members are the entities
// that are part of it, as the catalog says. A group is synced unless it
// says otherwise: its members move together, on one version and one tag,
// and pointing at any member releases them all. A group whose grouping is
// independent gives each member a version and a tag of its own, and a
// member that says independent leaves its synced group. A domain groups
// its systems only where it says synced; otherwise each of its systems is
// a group of its own.
//
// How an entity is released is said in its catalog file, under
// spec.frontseat.release, and is inherited: a domain's setting reaches its
// systems, a system's its members, and the nearest level that sets a key
// wins, key by key. A synced group moves as one, so it is released one
// way: of its settings, a member sets only its grouping, to leave it. The
// graph API shows, for each entity, what it is released as and where each
// of its settings came from. A repository releases only where it lists the
// release plugin, whose strategies the defaults name.
//
// Releasing has no phases. You plan first, which reads and changes
// nothing, and then release the plan you saw, named by its etag: a plan
// whose source has moved since is refused. A release builds its units for
// production, exactly as a production build does, and only then tags each
// unit that moves, at the commit it was planned from. A commit that
// carries a unit's tag is that release: releasing it again completes it
// and makes no new version.
// Releases / What is released / A component in no system is released on its own.
func TestAComponentInNoSystemIsReleasedOnItsOwn(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / A component in no system is released on its own")
	spec := newScenario(t)

	plan := spec.releasable().releaseWorkspace().plan(spec.releaseOf("greet"))

	spec.Expect(plan.units()).To(Equal([]string{"greet"}))
	spec.Expect(plan.unit("greet").members()).To(Equal([]string{"greet"}))
	spec.Expect(plan.unit("greet").group()).To(Equal("greet"))
}

// Releases / What is released / A system is released as one, its members on one version.
func TestASystemIsReleasedAsOneItsMembersOnOneVersion(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / A system is released as one, its members on one version")
	spec := newScenario(t)

	plan := spec.releasable().releaseWorkspace().plan(spec.releaseOf("system:kiosk"))

	spec.Expect(plan.units()).To(Equal([]string{"kiosk"}))
	spec.Expect(plan.unit("kiosk").members()).To(ConsistOf("banner", "ticker"))
	spec.Expect(plan.unit("kiosk").grouping()).To(Equal("synced"))
}

// Releases / What is released / A member of a synced system is released with its system.
func TestAMemberOfASyncedSystemIsReleasedWithItsSystem(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / A member of a synced system is released with its system")
	spec := newScenario(t)

	plan := spec.releasable().releaseWorkspace().plan(spec.releaseOf("banner"))

	spec.Expect(plan.units()).To(Equal([]string{"kiosk"}))
	spec.Expect(plan.unit("kiosk").members()).To(ConsistOf("banner", "ticker"))
}

// Releases / What is released / A system whose grouping is independent releases each member on its own.
func TestASystemWhoseGroupingIsIndependentReleasesEachMemberOnItsOwn(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / A system whose grouping is independent releases each member on its own")
	spec := newScenario(t)

	workspace := spec.releasable().releases("system:kiosk", "grouping: independent").releaseWorkspace()

	plan := workspace.plan(spec.releaseOf("system:kiosk"))

	spec.Expect(plan.units()).To(ConsistOf("banner", "ticker"))
	spec.Expect(plan.unit("banner").group()).To(Equal("kiosk"))
	spec.Expect(plan.unit("banner").grouping()).To(Equal("independent"))
}

// Releases / What is released / A member that says independent leaves its synced system.
func TestAMemberThatSaysIndependentLeavesItsSyncedSystem(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / A member that says independent leaves its synced system")
	spec := newScenario(t)

	workspace := spec.releasable().releases("banner", "grouping: independent").releaseWorkspace()

	plan := workspace.plan(spec.releaseOf("system:kiosk"))

	spec.Expect(plan.units()).To(ConsistOf("kiosk", "banner"))
	spec.Expect(plan.unit("kiosk").members()).To(Equal([]string{"ticker"}))
	spec.Expect(plan.unit("banner").members()).To(Equal([]string{"banner"}))
}

// Releases / What is released / A domain that says nothing leaves each of its systems a group of its own.
func TestADomainThatSaysNothingLeavesEachOfItsSystemsAGroupOfItsOwn(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	plan := spec.releasable().domainHolds("retail", "lobby", "greet").releaseWorkspace().plan(spec.releaseOf("domain:retail"))

	spec.Expect(plan.units()).To(ConsistOf("kiosk", "lobby"))
	spec.Expect(plan.unit("kiosk").members()).To(ConsistOf("banner", "ticker"))
	spec.Expect(plan.unit("lobby").members()).To(Equal([]string{"greet"}))
}

// Releases / What is released / A domain that says synced is one group across its systems.
func TestADomainThatSaysSyncedIsOneGroupAcrossItsSystems(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.syncedRetail().releaseWorkspace()

	plan := workspace.plan(spec.releaseOf("domain:retail"))

	spec.Expect(plan.units()).To(Equal([]string{"retail"}))
	spec.Expect(plan.unit("retail").members()).To(ConsistOf("banner", "ticker", "greet"))
}

// Releases / What is released / A name a Domain and a Component share is the Component's, and the Domain is named with its kind.
func TestANameADomainAndAComponentShareIsTheComponentsAndTheDomainIsNamedWithItsKind(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	// A component retail, in platform, beside the synced domain retail
	// of services: one repository may not hold both, as their tags would
	// be spelled alike.
	source := spec.syncedRetail()
	source.says("platform", "retail/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: retail\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n")
	source.says("platform", "retail/go.mod", "module example.com/retail\n\ngo 1.27\n")
	source.says("platform", "retail/retail.go", "package retail\n")
	workspace := source.releaseWorkspace()

	// No kind and no namespace, as `frontseat ship plan retail` asks: the
	// component, released alone.
	component := workspace.plan(func(r *buildv1.PlanReleaseRequest) { r.Entities = []*typev1.EntityRef{{Name: "retail"}} })
	spec.Expect(component.units()).To(Equal([]string{"retail"}))
	spec.Expect(component.unit("retail").members()).To(Equal([]string{"retail"}))
	// The control: named with its kind, it is the domain, and its members.
	domain := workspace.plan(func(r *buildv1.PlanReleaseRequest) {
		r.Entities = []*typev1.EntityRef{{Kind: "domain", Name: "retail"}}
	})
	spec.Expect(domain.units()).To(Equal([]string{"retail"}))
	spec.Expect(domain.unit("retail").members()).To(ConsistOf("banner", "ticker", "greet"))
	// And a name the workspace holds none of is refused as one.
	_, refusal := workspace.planning(func(r *buildv1.PlanReleaseRequest) { r.Entities = []*typev1.EntityRef{{Name: "kiosks"}} })
	spec.Expect(refusal).To(BeRefusedSaying("the workspace holds no entity component:kiosks"))
}

// Releases / What is released / A unit whose members are in two repositories is refused by name.
func TestAUnitWhoseMembersAreInTwoRepositoriesIsRefusedByName(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / A unit whose members are in two repositories is refused by name")
	spec := newScenario(t)

	_, refusal := spec.releasable().releaseWorkspace().planning(spec.releaseOf("system:storefront"))

	spec.Expect(refusal).To(BeRefusedSaying("storefront's members are in services and platform; a unit is released from one repository"))
}

// Releases / What is released / Members of an independent system may be in two repositories.
func TestMembersOfAnIndependentSystemMayBeInTwoRepositories(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / Members of an independent system may be in two repositories")
	spec := newScenario(t)

	workspace := spec.releasable().releases("system:storefront", "grouping: independent").releaseWorkspace()

	plan := workspace.plan(spec.releaseOf("system:storefront"))

	spec.Expect(plan.units()).To(ConsistOf("hello", "gateway"))
}

// Releases / What is released / A system with no members is refused by name.
func TestASystemWithNoMembersIsRefusedByName(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / A system with no members is refused by name")
	spec := newScenario(t)

	workspace := spec.releasable().releases("system:empty").releaseWorkspace()

	_, refusal := workspace.planning(spec.releaseOf("system:empty"))

	spec.Expect(refusal).To(BeRefusedSaying("system:default/empty has no members to release"))
}

// Releases / What is released / Asking to release nothing is refused.
func TestAskingToReleaseNothingIsRefused(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / Asking to release nothing is refused")
	spec := newScenario(t)

	_, refusal := spec.releasable().releaseWorkspace().planning()

	spec.Expect(refusal).To(BeRefusedSaying("name what to release"))
}

// Releases / What is released / An entity named twice is refused, and a unit named by two of its members is one unit.
func TestAnEntityNamedTwiceIsRefusedAndAUnitNamedByTwoOfItsMembersIsOneUnit(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / An entity named twice is refused, and a unit named by two of its members is one unit")
	spec := newScenario(t)

	workspace := spec.releasable().releaseWorkspace()

	_, refusal := workspace.planning(spec.releaseOf("greet", "greet"))
	plan := workspace.plan(spec.releaseOf("banner", "ticker"))

	spec.Expect(refusal).To(BeRefusedSaying("component:default/greet is named twice"))
	spec.Expect(plan.units()).To(Equal([]string{"kiosk"}))
}

// Releases / What is released / What each entity is released as is in the graph.
func TestWhatEachEntityIsReleasedAsIsInTheGraph(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / What each entity is released as is in the graph")
	spec := newScenario(t)

	workspace := spec.releasable().releaseWorkspace()

	spec.Expect(workspace.releasedAs("banner")).To(Equal("kiosk"))
	spec.Expect(workspace.releasedAs("ticker")).To(Equal("kiosk"))
	spec.Expect(workspace.releasedAs("greet")).To(Equal("greet"))
}

// Releases / What is released / A repository that lists no release plugin releases nothing, and says so.
func TestARepositoryThatListsNoReleasePluginReleasesNothingAndSaysSo(t *testing.T) {
	t.Skip("Pending requirement: Releases / What is released / A repository that lists no release plugin releases nothing, and says so")
	spec := newScenario(t)

	source := spec.releasable()
	source.plugins["services"] = map[string][]string{"go": nil, "backstage": nil}
	source.listing("services")

	_, refusal := source.releaseWorkspace().planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("services lists no release plugin; list it to release greet"))
}

// Releases / How settings resolve / A setting nobody wrote is the default, and is shown as one.
func TestASettingNobodyWroteIsTheDefaultAndIsShownAsOne(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A setting nobody wrote is the default, and is shown as one")
	spec := newScenario(t)

	workspace := spec.releasable().releaseWorkspace()

	spec.Expect(workspace.releaseSetting("greet", "grouping")).To(Equal(Setting{"synced", "the default"}))
	spec.Expect(workspace.releaseSetting("greet", "tag")).To(Equal(Setting{"{unit}/v{version}", "the default"}))
	spec.Expect(workspace.releaseSetting("greet", "initial")).To(Equal(Setting{"0.1.0", "the default"}))
	spec.Expect(workspace.releaseSetting("greet", "versioning")).To(Equal(Setting{"release:conventional-commits", "the default"}))
	spec.Expect(workspace.releaseSetting("greet", "branching")).To(Equal(Setting{"release:release-flow", "the default"}))
}

// Releases / How settings resolve / A system's settings are in the graph, as its members inherit them.
func TestASystemSSettingsAreInTheGraphAsItsMembersInheritThem(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A system's settings are in the graph, as its members inherit them")
	spec := newScenario(t)

	workspace := spec.releasable().releases("system:kiosk", "initial: 1.0.0").releaseWorkspace()

	spec.Expect(workspace.releaseSetting("kiosk", "initial")).To(Equal(Setting{"1.0.0", "system:default/kiosk"}))
}

// Releases / How settings resolve / A member inherits its system's settings.
func TestAMemberInheritsItsSystemSSettings(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A member inherits its system's settings")
	spec := newScenario(t)

	workspace := spec.releasable().releases("system:kiosk", "initial: 1.0.0").releaseWorkspace()

	spec.Expect(workspace.releaseSetting("banner", "initial")).To(Equal(Setting{"1.0.0", "system:default/kiosk"}))
}

// Releases / How settings resolve / A domain's settings reach the members of its systems.
func TestADomainSSettingsReachTheMembersOfItsSystems(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A domain's settings reach the members of its systems")
	spec := newScenario(t)

	workspace := spec.releasable().releases("domain:retail", "initial: 2.0.0").releaseWorkspace()

	spec.Expect(workspace.releaseSetting("ticker", "initial")).To(Equal(Setting{"2.0.0", "domain:default/retail"}))
}

// Releases / How settings resolve / The nearest level wins, key by key.
func TestTheNearestLevelWinsKeyByKey(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / The nearest level wins, key by key")
	spec := newScenario(t)

	workspace := spec.releasable().
		releases("domain:retail", "initial: 2.0.0", "tag: retail-{unit}-{version}").
		releases("system:kiosk", "initial: 1.0.0").
		releaseWorkspace()

	spec.Expect(workspace.releaseSetting("banner", "initial")).To(Equal(Setting{"1.0.0", "system:default/kiosk"}))
	spec.Expect(workspace.releaseSetting("banner", "tag")).To(Equal(Setting{"retail-{unit}-{version}", "domain:default/retail"}))
}

// Releases / How settings resolve / A member of an independent group sets its own settings.
func TestAMemberOfAnIndependentGroupSetsItsOwnSettings(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A member of an independent group sets its own settings")
	spec := newScenario(t)

	workspace := spec.releasable().
		releases("system:kiosk", "grouping: independent", "initial: 1.0.0").
		releases("banner", "initial: 3.0.0").
		releaseWorkspace()

	spec.Expect(workspace.releaseSetting("banner", "initial")).To(Equal(Setting{"3.0.0", "component:default/banner"}))
	spec.Expect(workspace.releaseSetting("ticker", "initial")).To(Equal(Setting{"1.0.0", "system:default/kiosk"}))
}

// Releases / How settings resolve / A member of a synced group sets none of its group's settings / its tag.
func TestItsTag(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A member of a synced group sets none of its group's settings / its tag")
	spec := newScenario(t)

	spec.expectGroupSettingRefused("tag: b-{version}", "tag")
}

// Releases / How settings resolve / A member of a synced group sets none of its group's settings / its first version.
func TestItsFirstVersion(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A member of a synced group sets none of its group's settings / its first version")
	spec := newScenario(t)

	spec.expectGroupSettingRefused("initial: 1.0.0", "initial")
}

// Releases / How settings resolve / A member of a synced group sets none of its group's settings / its versioning.
func TestItsVersioning(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A member of a synced group sets none of its group's settings / its versioning")
	spec := newScenario(t)

	spec.expectGroupSettingRefused("versioning: release:manual", "versioning")
}

// Releases / How settings resolve / A member of a synced group sets none of its group's settings / its branching.
func TestItsBranching(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A member of a synced group sets none of its group's settings / its branching")
	spec := newScenario(t)

	spec.expectGroupSettingRefused("branching: release:github-flow", "branching")
}

// Releases / How settings resolve / A setting frontseat does not know is refused, naming the file and the key.
func TestASettingFrontseatDoesNotKnowIsRefusedNamingTheFileAndTheKey(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A setting frontseat does not know is refused, naming the file and the key")
	spec := newScenario(t)

	workspace := spec.releasable().releases("greet", "channels: beta").releaseWorkspace()

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("greet/catalog-info.yaml: spec.frontseat.release.channels"))
}

// Releases / How settings resolve / A grouping is synced or independent.
func TestAGroupingIsSyncedOrIndependent(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A grouping is synced or independent")
	spec := newScenario(t)

	workspace := spec.releasable().releases("greet", "grouping: locked").releaseWorkspace()

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("spec.frontseat.release.grouping must be synced or independent"))
}

// Releases / How settings resolve / A tag holds the version exactly once.
func TestATagHoldsTheVersionExactlyOnce(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A tag holds the version exactly once")
	spec := newScenario(t)

	workspace := spec.releasable().releases("greet", "tag: '{unit}'").releaseWorkspace()

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("spec.frontseat.release.tag {unit} must hold {version} once"))
}

// Releases / How settings resolve / Two units of one repository whose tags are spelled alike are refused by name.
func TestTwoUnitsOfOneRepositoryWhoseTagsAreSpelledAlikeAreRefusedByName(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / Two units of one repository whose tags are spelled alike are refused by name")
	spec := newScenario(t)

	workspace := spec.releasable().releases("greet", "tag: v{version}").releases("system:kiosk", "tag: v{version}").releaseWorkspace()

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("greet and kiosk spell their tags alike in services: v{version}"))
}

// Releases / How settings resolve / A first version is a semantic version.
func TestAFirstVersionIsASemanticVersion(t *testing.T) {
	t.Skip("Pending requirement: Releases / How settings resolve / A first version is a semantic version")
	spec := newScenario(t)

	workspace := spec.releasable().releases("greet", "initial: '1.0'").releaseWorkspace()

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("spec.frontseat.release.initial: 1.0 is no semantic version"))
}

// Releases / Releasing / A release tags each unit that moves, at the commit it was planned from.
func TestAReleaseTagsEachUnitThatMovesAtTheCommitItWasPlannedFrom(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / A release tags each unit that moves, at the commit it was planned from")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace()

	shipped := workspace.release(spec.releaseOf("greet"))

	spec.Expect(shipped.state()).To(Equal("succeeded"))
	spec.Expect(shipped.tagsOf("greet")).To(Equal([]string{"services:greet/v0.1.0"}))
	spec.Expect(workspace.tagsAtHead("services")).To(ContainElement("greet/v0.1.0"))
}

// Releases / Releasing / A synced group is tagged once, with its unit's name.
func TestASyncedGroupIsTaggedOnceWithItsUnitSName(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / A synced group is tagged once, with its unit's name")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace()

	shipped := workspace.release(spec.releaseOf("system:kiosk"))

	spec.Expect(shipped.tagsOf("kiosk")).To(Equal([]string{"services:kiosk/v0.1.0"}))
}

// Releases / Releasing / An independent group's members are each tagged.
func TestAnIndependentGroupSMembersAreEachTagged(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / An independent group's members are each tagged")
	spec := newScenario(t)

	workspace := spec.fromMain().releases("system:kiosk", "grouping: independent").releaseWorkspace()

	shipped := workspace.release(spec.releaseOf("system:kiosk"))

	spec.Expect(shipped.tagsOf("banner")).To(Equal([]string{"services:banner/v0.1.0"}))
	spec.Expect(shipped.tagsOf("ticker")).To(Equal([]string{"services:ticker/v0.1.0"}))
}

// Releases / Releasing / What a release builds is what a production build builds, and not a development one.
func TestWhatAReleaseBuildsIsWhatAProductionBuildBuildsAndNotADevelopmentOne(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / What a release builds is what a production build builds, and not a development one")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace()

	shipped := workspace.release(spec.releaseOf("system:kiosk"))
	production := workspace.build(production(), of("ticker"))
	development := workspace.build(of("ticker"))

	spec.Expect(shipped.built().action("//services/ticker:go.compile")).To(Equal(production.action("//services/ticker:go.compile")))
	spec.Expect(shipped.built().action("//services/ticker:go.compile")).NotTo(Equal(development.action("//services/ticker:go.compile")))
}

// Releases / Releasing / A commit that carries a unit's tag is released: releasing it again completes it.
func TestACommitThatCarriesAUnitSTagIsReleasedReleasingItAgainCompletesIt(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / A commit that carries a unit's tag is released: releasing it again completes it")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace()
	workspace.release(spec.releaseOf("greet"))

	plan := workspace.plan(spec.releaseOf("greet"))
	again := plan.release()

	spec.Expect(plan.unit("greet").why()).To(Equal([]string{"released"}))
	spec.Expect(plan.unit("greet").next()).To(Equal("0.1.0"))
	spec.Expect(again.already("greet")).To(BeTrue())
	spec.Expect(workspace.tagsAtHead("services")).To(Equal([]string{"greet/v0.1.0"}))
}

// Releases / Releasing / A unit whose tag is on an earlier commit, and that has not changed since, holds.
func TestAUnitWhoseTagIsOnAnEarlierCommitAndThatHasNotChangedSinceHolds(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / A unit whose tag is on an earlier commit, and that has not changed since, holds")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("services", "greet/v1.0.0")
	workspace.commits("fix: say more", "services/banner/banner.go")

	spec.expectGreetUnchanged(workspace)
}

// Releases / Releasing / A unit that holds is neither built nor tagged, and one that moves beside it is both.
func TestAUnitThatHoldsIsNeitherBuiltNorTaggedAndOneThatMovesBesideItIsBoth(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / A unit that holds is neither built nor tagged, and one that moves beside it is both")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace().tagged("platform", "log/v1.0.0")
	workspace.commits("chore: note it", "platform/gateway/main.go")

	shipped := workspace.release(spec.releaseOf("greet", "log"))

	spec.Expect(shipped.built().actions()).NotTo(ContainElement(HavePrefix("//platform/log:")))
	spec.Expect(shipped.built().actions()).To(ContainElement(HavePrefix("//services/greet:")))
	spec.Expect(workspace.tagsAtHead("platform")).To(BeEmpty())
	spec.Expect(workspace.tagsAtHead("services")).To(ContainElement("greet/v0.1.0"))
}

// Releases / Releasing / A plan whose source moved since is refused, and planned again it is released.
func TestAPlanWhoseSourceMovedSinceIsRefusedAndPlannedAgainItIsReleased(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / A plan whose source moved since is refused, and planned again it is released")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace()
	fresh, refusal := workspace.releasingAPlanOfGreetThatMoved()

	spec.Expect(refusal).To(BeRefusedSaying("the plan is no longer what releasing would do; plan again"))
	spec.Expect(fresh.state()).To(Equal("succeeded"))
}

// Releases / Releasing / Two daemons that plan one request at one commit come to one etag.
func TestTwoDaemonsThatPlanOneRequestAtOneCommitComeToOneEtag(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / Two daemons that plan one request at one commit come to one etag")
	spec := newScenario(t)

	source := spec.fromMain()

	one := source.releaseWorkspace().plan(spec.releaseOf("greet"))
	other := source.releaseWorkspace().plan(spec.releaseOf("greet"))

	spec.Expect(other.etag()).To(Equal(one.etag()))
}

// Releases / Releasing / A plan of another commit, or of another request, has another etag.
func TestAPlanOfAnotherCommitOrOfAnotherRequestHasAnotherEtag(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / A plan of another commit, or of another request, has another etag")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace()
	one := workspace.plan(spec.releaseOf("greet"))
	asked := workspace.plan(spec.releaseOf("log"))

	later := workspace.planAfterGreetChanges()

	spec.Expect(asked.etag()).NotTo(Equal(one.etag()))
	spec.Expect(later.etag()).NotTo(Equal(one.etag()))
}

// Releases / Releasing / A version already tagged on another commit is refused when planned.
func TestAVersionAlreadyTaggedOnAnotherCommitIsRefusedWhenPlanned(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / A version already tagged on another commit is refused when planned")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace()
	workspace.onBranch("services", "elsewhere").
		changesGreet("fix: somewhere else").
		tagged("services", "greet/v0.1.0").
		backFromBranch("services")

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("greet 0.1.0 is not new: greet/v0.1.0 names another commit"))
}

// Releases / Releasing / A unit whose build fails is not tagged, and one beside it that built is.
func TestAUnitWhoseBuildFailsIsNotTaggedAndOneBesideItThatBuiltIs(t *testing.T) {
	t.Skip("Pending requirement: Releases / Releasing / A unit whose build fails is not tagged, and one beside it that built is")
	spec := newScenario(t)

	workspace := spec.fromMain().releaseWorkspace()
	workspace.write("services/greet/greet.go", "package greet\n\nfunc broken( {")
	workspace.changesGreet("fix: break the greeting")

	shipped := workspace.release(spec.releaseOf("greet", "log"))

	spec.Expect(shipped.state()).To(Equal("failed"))
	spec.Expect(workspace.tagsAtHead("services")).To(BeEmpty())
	spec.Expect(workspace.tagsAtHead("platform")).To(ContainElement("log/v0.1.0"))
}

// What is served of releasing so far is planning, under Release Flow:
// these are its use cases through the API, each from a release branch.
// The specs above are the whole design, and wait for the rest.
// Planning a release / A system is planned as one unit, of all its members, on one version and one tag.
func TestASystemIsPlannedAsOneUnitOfAllItsMembersOnOneVersionAndOneTag(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")

	kiosk := workspace.plan(spec.releaseOf("banner")).unit("kiosk")

	spec.Expect(kiosk.members()).To(ConsistOf("banner", "ticker"))
	spec.Expect(kiosk.grouping()).To(Equal("synced"))
	spec.Expect(kiosk.next()).To(Equal("1.0.0"))
	spec.Expect(kiosk.tag()).To(Equal("kiosk/v1.0.0"))
}

// Planning a release / A unit moves only for changes to its own members' files.
func TestAUnitMovesOnlyForChangesToItsOwnMembersFiles(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0").tagged("services", "kiosk/v1.0.0").tagged("services", "greet/v1.0.0")
	workspace.commits("fix: scroll the ticker", "services/ticker/ticker.go")

	plan := workspace.plan(spec.releaseOf("system:kiosk", "greet"))

	spec.Expect(plan.unit("kiosk").next()).To(Equal("1.0.1"))
	spec.Expect(plan.unit("kiosk").why()).To(Equal([]string{"own change: fix: scroll the ticker"}))
	spec.Expect(plan.unit("greet").next()).To(BeEmpty())
	spec.Expect(plan.unit("greet").why()).To(Equal([]string{"unchanged"}))
}

// Planning a release / A plan lists what a unit depends on before it.
func TestAPlanListsWhatAUnitDependsOnBeforeIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	spec.expectGreetBeforeKiosk(spec.onRelease("1.0"))
}

// Planning a release / A commit that carries a unit's tag is released, and plans no new version.
func TestACommitThatCarriesAUnitSTagIsReleasedAndPlansNoNewVersion(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	workspace.changesGreet("fix: trim the greeting").tagged("services", "greet/v1.0.0")

	greet := workspace.unitOf("greet")

	spec.Expect(greet.why()).To(Equal([]string{"released"}))
	spec.Expect(greet.last()).To(Equal("1.0.0"))
	spec.Expect(greet.next()).To(BeEmpty())
}

// Planning a release / A version already tagged on another commit is refused when planned.
func TestPlanningAReleaseAVersionAlreadyTaggedOnAnotherCommitIsRefusedWhenPlanned(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0").tagged("services", "greet/v1.0.0")
	workspace.onBranch("services", "elsewhere").
		changesGreet("fix: somewhere else").
		tagged("services", "greet/v1.0.1")
	workspace.switchTo("services", "release/1.0").
		changesGreet("fix: trim the greeting")

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("greet 1.0.1 is not new: greet/v1.0.1 names another commit"))
	spec.Expect(spec.refusedAs(refusal)).To(Equal("failed_precondition"))
}

// Planning a release / A tag git would not take is refused when planned, naming the tag.
func TestATagGitWouldNotTakeIsRefusedWhenPlannedNamingTheTag(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	sound := spec.releasable().releases("greet", "tag: '{unit}-{version}'").releaseWorkspace().onBranch("services", "release/1.0")
	workspace := spec.releasable().releases("greet", "tag: '{unit}.lock/{version}'").releaseWorkspace().onBranch("services", "release/1.0")

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(sound.unitOf("greet").tag()).To(Equal("greet-1.0.0"))
	spec.Expect(refusal).To(BeRefusedSaying(`greet would be tagged "greet.lock/1.0.0"`))
}

// Planning a release / Two plans of one request at one commit have one etag, and another commit or request another.
func TestTwoPlansOfOneRequestAtOneCommitHaveOneEtagAndAnotherCommitOrRequestAnother(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	one := workspace.plan(spec.releaseOf("greet"))
	again := workspace.plan(spec.releaseOf("greet"))
	asked := workspace.plan(spec.releaseOf("greet", "system:kiosk"))

	later := workspace.planAfterGreetChanges()

	spec.Expect(again.etag()).To(Equal(one.etag()))
	spec.Expect(asked.etag()).NotTo(Equal(one.etag()))
	spec.Expect(later.etag()).NotTo(Equal(one.etag()))
}

// Planning a release / A request is refused for what it asks, each in its own way.
func TestARequestIsRefusedForWhatItAsksEachInItsOwnWay(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	spec.Expect(workspace.plan(spec.releaseOf("greet")).units()).To(Equal([]string{"greet"}))

	_, none := workspace.planning()
	_, twice := workspace.planning(spec.releaseOf("greet", "greet"))
	_, ghost := workspace.planning(spec.releaseOf("ghost"))
	_, across := workspace.planning(spec.releaseOf("system:storefront"))

	spec.Expect(none).To(BeRefusedSaying("name what to release"))
	spec.Expect(spec.refusedAs(none)).To(Equal("invalid_argument"))
	spec.Expect(twice).To(BeRefusedSaying("component:default/greet is named twice"))
	spec.Expect(spec.refusedAs(twice)).To(Equal("invalid_argument"))
	spec.Expect(spec.refusedAs(ghost)).To(Equal("not_found"))
	spec.Expect(across).To(BeRefusedSaying("storefront's members are in services and platform; a unit is released from one repository"))
	spec.Expect(spec.refusedAs(across)).To(Equal("failed_precondition"))
}

// Planning a release / A repository that lists no release plugin is refused, saying to list it.
func TestARepositoryThatListsNoReleasePluginIsRefusedSayingToListIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.releasable()
	source.plugins["services"] = map[string][]string{"go": nil, "backstage": nil}
	source.listing("services")
	workspace := source.releaseWorkspace().onBranch("services", "release/1.0")

	_, refusal := workspace.planning(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("services lists no release plugin; list it to release greet"))
}

// Planning a release / A member of a synced group that says how it is released is refused, naming the setting.
func TestAMemberOfASyncedGroupThatSaysHowItIsReleasedIsRefusedNamingTheSetting(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.releasable().releases("banner", "initial: 3.0.0").releaseWorkspace().onBranch("services", "release/1.0")

	_, refusal := workspace.planning(spec.releaseOf("banner"))

	spec.Expect(refusal).To(BeRefusedSaying("banner versions with kiosk, which says how it is released: spec.frontseat.release.initial"))
}

// Planning a release / A domain that says synced is planned as one unit, of every component of its systems, on one version and one tag.
func TestADomainThatSaysSyncedIsPlannedAsOneUnitOfEveryComponentOfItsSystemsOnOneVersionAndOneTag(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.syncedRetail().releaseWorkspace().onBranch("services", "release/1.0")

	plan := workspace.plan(spec.releaseOf("domain:retail"))

	spec.Expect(plan.units()).To(Equal([]string{"retail"}))
	retail := plan.unit("retail")
	spec.Expect(retail.members()).To(ConsistOf("banner", "ticker", "greet"))
	spec.Expect(retail.group()).To(Equal("retail"))
	spec.Expect(retail.grouping()).To(Equal("synced"))
	spec.Expect(retail.next()).To(Equal("1.0.0"))
	spec.Expect(retail.tag()).To(Equal("retail/v1.0.0"))
}

// Planning a release / A change to a component of one system of a synced domain moves the whole domain.
func TestAChangeToAComponentOfOneSystemOfASyncedDomainMovesTheWholeDomain(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.syncedRetail().releaseWorkspace().onBranch("services", "release/1.0").tagged("services", "retail/v1.0.0")
	workspace.commits("fix: greet the shop", "services/hello/go.mod")
	elsewhere := workspace.plan(spec.releaseOf("domain:retail")).unit("retail")
	workspace.commits("fix: scroll the ticker", "services/ticker/ticker.go")

	retail := workspace.plan(spec.releaseOf("domain:retail")).unit("retail")

	spec.Expect(elsewhere.why()).To(Equal([]string{"unchanged"}))
	spec.Expect(elsewhere.next()).To(BeEmpty())
	spec.Expect(retail.last()).To(Equal("1.0.0"))
	spec.Expect(retail.next()).To(Equal("1.0.1"))
	spec.Expect(retail.why()).To(Equal([]string{"own change: fix: scroll the ticker"}))
	spec.Expect(retail.tag()).To(Equal("retail/v1.0.1"))
	spec.Expect(retail.members()).To(ConsistOf("banner", "ticker", "greet"))
}

// Planning a release / A component or a system of a synced domain is released with its domain, unless it says independent.
func TestAComponentOrASystemOfASyncedDomainIsReleasedWithItsDomainUnlessItSaysIndependent(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.syncedRetail().releaseWorkspace().onBranch("services", "release/1.0")
	left := spec.syncedRetail().releases("system:lobby", "grouping: independent").releaseWorkspace().onBranch("services", "release/1.0")

	spec.Expect(workspace.plan(spec.releaseOf("banner")).units()).To(Equal([]string{"retail"}))
	spec.Expect(workspace.plan(spec.releaseOf("system:lobby")).units()).To(Equal([]string{"retail"}))
	spec.Expect(workspace.releasedAs("greet")).To(Equal("retail"))
	spec.Expect(workspace.plan(spec.releaseOf("log")).units()).To(Equal([]string{"log"}))
	spec.Expect(left.plan(spec.releaseOf("domain:retail")).unit("retail").members()).To(ConsistOf("banner", "ticker"))
	spec.Expect(left.plan(spec.releaseOf("greet")).units()).To(Equal([]string{"greet"}))
	spec.Expect(left.plan(spec.releaseOf("greet")).unit("greet").group()).To(Equal("lobby"))
}

// Planning a release / A system of a synced domain that says how it is released is refused, naming the setting.
func TestASystemOfASyncedDomainThatSaysHowItIsReleasedIsRefusedNamingTheSetting(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.syncedRetail().releases("system:kiosk", "initial: 3.0.0").releaseWorkspace().onBranch("services", "release/1.0")

	_, refusal := workspace.planning(spec.releaseOf("domain:retail"))

	spec.Expect(refusal).To(BeRefusedSaying("kiosk versions with retail, which says how it is released: spec.frontseat.release.initial"))
	spec.Expect(spec.refusedAs(refusal)).To(Equal("failed_precondition"))
}

// Planning a release / A domain with nothing to release is refused by name.
func TestADomainWithNothingToReleaseIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.releasable().releases("domain:empty", "grouping: synced").releases("domain:vacant").releaseWorkspace().onBranch("services", "release/1.0")

	_, synced := workspace.planning(spec.releaseOf("domain:empty"))
	_, vacant := workspace.planning(spec.releaseOf("domain:vacant"))

	spec.Expect(workspace.plan(spec.releaseOf("domain:retail")).units()).To(Equal([]string{"kiosk"}))
	spec.Expect(synced).To(BeRefusedSaying("domain:default/empty has no members to release"))
	spec.Expect(spec.refusedAs(synced)).To(Equal("failed_precondition"))
	spec.Expect(vacant).To(BeRefusedSaying("domain:default/vacant has no members to release"))
	spec.Expect(spec.refusedAs(vacant)).To(Equal("failed_precondition"))
}

// Planning a release / A domain within a domain is not served, and is refused naming it.
func TestADomainWithinADomainIsNotServedAndIsRefusedNamingIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	beside := spec.releasable().releases("domain:company").releaseWorkspace().onBranch("services", "release/1.0")
	within := spec.releasable().subdomain("retail", "company").releaseWorkspace().onBranch("services", "release/1.0")

	_, member := within.planning(spec.releaseOf("banner"))
	_, parent := within.planning(spec.releaseOf("domain:company"))

	spec.Expect(beside.plan(spec.releaseOf("banner")).units()).To(Equal([]string{"kiosk"}))
	spec.Expect(member).To(BeRefusedSaying("domain:default/retail is a subdomain of domain:default/company, and releasing a domain within a domain is not served yet"))
	spec.Expect(spec.refusedAs(member)).To(Equal("unimplemented"))
	spec.Expect(parent).To(BeRefusedSaying("domain:default/retail is a subdomain of domain:default/company"))
	spec.Expect(within.plan(spec.releaseOf("greet")).units()).To(Equal([]string{"greet"}))
}

// Planning a release / A synced domain whose members are in two repositories is refused by name.
func TestASyncedDomainWhoseMembersAreInTwoRepositoriesIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.syncedRetail().domainHolds("retail", "arcade", "gateway").releaseWorkspace().onBranch("services", "release/1.0")

	_, refusal := workspace.planning(spec.releaseOf("domain:retail"))

	spec.Expect(refusal).To(BeRefusedSaying("retail's members are in services and platform; a unit is released from one repository"))
	spec.Expect(spec.refusedAs(refusal)).To(Equal("failed_precondition"))
	spec.Expect(workspace.plan(spec.releaseOf("log")).units()).To(Equal([]string{"log"}))
}

// Planning a release / Versions never go backwards: a domain continues above what its name and its systems were released as.
func TestVersionsNeverGoBackwardsADomainContinuesAboveWhatItsNameAndItsSystemsWereReleasedAs(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	fresh := spec.syncedRetail().releaseWorkspace().onBranch("services", "release/2.3")
	asSystem := spec.syncedRetail().releaseWorkspace().onBranch("services", "release/2.3").tagged("services", "kiosk/v2.3.0")
	asSystem.commits("fix: scroll the ticker", "services/ticker/ticker.go")
	asName := spec.syncedRetail().releaseWorkspace().onBranch("services", "release/0.77").tagged("services", "retail/v0.77.0")
	asName.commits("fix: scroll the ticker", "services/ticker/ticker.go")

	first := fresh.plan(spec.releaseOf("domain:retail")).unit("retail")
	aboveSystem := asSystem.plan(spec.releaseOf("domain:retail")).unit("retail")
	aboveName := asName.plan(spec.releaseOf("domain:retail")).unit("retail")

	spec.Expect(first.why()).To(Equal([]string{"first release"}))
	spec.Expect(first.next()).To(Equal("2.3.0"))
	spec.Expect(aboveSystem.last()).To(Equal("2.3.0"))
	spec.Expect(aboveSystem.next()).To(Equal("2.3.1"))
	spec.Expect(aboveSystem.tag()).To(Equal("retail/v2.3.1"))
	spec.Expect(aboveName.last()).To(Equal("0.77.0"))
	spec.Expect(aboveName.next()).To(Equal("0.77.1"))
	spec.Expect(aboveName.tag()).To(Equal("retail/v0.77.1"))
}

// Planning a release / A domain and a unit of one name in one repository are refused by name.
func TestADomainAndAUnitOfOneNameInOneRepositoryAreRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	apart := spec.syncedRetail().releaseWorkspace().onBranch("services", "release/1.0")
	alike := spec.releasable().domainHolds("kiosk", "lobby", "greet").releases("domain:kiosk", "grouping: synced").releaseWorkspace().onBranch("services", "release/1.0")

	_, refusal := alike.planning(spec.releaseOf("domain:kiosk"))

	spec.Expect(apart.plan(spec.releaseOf("domain:retail")).units()).To(Equal([]string{"retail"}))
	spec.Expect(refusal).To(BeRefusedSaying("domain:default/kiosk and system:default/kiosk spell their tags alike in services: kiosk/v{version}"))
	spec.Expect(spec.refusedAs(refusal)).To(Equal("failed_precondition"))
}

// Planning a release / What an entity is part of that the workspace does not hold is refused by name.
func TestWhatAnEntityIsPartOfThatTheWorkspaceDoesNotHoldIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.releasable().domainHolds("market", "lobby", "greet").releases("domain:market", "grouping: synced").describedElsewhere("market")
	held := source.releaseWorkspace().onBranch("services", "release/1.0")
	partial := source.releaseWorkspaceOf("services").onBranch("services", "release/1.0")

	_, refusal := partial.planning(spec.releaseOf("greet"))

	spec.Expect(held.plan(spec.releaseOf("greet")).units()).To(Equal([]string{"market"}))
	spec.Expect(refusal).To(BeRefusedSaying("system:default/lobby is part of domain:default/market, which this workspace does not hold: open the workspace with its catalog"))
	spec.Expect(spec.refusedAs(refusal)).To(Equal("failed_precondition"))
	spec.Expect(partial.plan(spec.releaseOf("banner")).units()).To(Equal([]string{"kiosk"}))
}

// Carrying a plan out is served as far as its tags: the units that move
// are built for production and tagged, in the daemon's checkout, and
// each tag is pushed to where its repository was brought from. A release
// whose tags are all there has succeeded; one of a commit made in the
// workspace and pushed nowhere keeps its tag there, and is unpublished.
// Releasing a plan / A release builds what moves for production and tags it, at the commit it was planned from.
func TestAReleaseBuildsWhatMovesForProductionAndTagsItAtTheCommitItWasPlannedFrom(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	workspace.changesGreet("fix: trim the greeting")

	shipped := workspace.release(spec.releaseOf("greet"))

	// The commit was made in the workspace and pushed nowhere: it is
	// built and tagged there, and its tag is not sent with a commit the
	// remote has not.
	spec.Expect(shipped.state()).To(Equal("unpublished"), shipped.release.GetFailure())
	spec.Expect(shipped.release.GetFailure()).To(ContainSubstring("greet/v1.0.0 was not pushed from services"))
	spec.Expect(shipped.release.GetFailure()).To(ContainSubstring("is on no branch of the remote"))
	spec.Expect(shipped.tagsOf("greet")).To(Equal([]string{"services:greet/v1.0.0"}))
	spec.Expect(workspace.tagsAtHead("services")).To(Equal([]string{"greet/v1.0.0"}))
	spec.Expect(shipped.built().outcome()).To(Equal("SUCCESS"))
	spec.Expect(shipped.built().actions()).To(ContainElement("//services/greet:go.compile"))
}

// Releasing a plan / What a release builds is what a production build builds, and not a development one.
func TestReleasingAPlanWhatAReleaseBuildsIsWhatAProductionBuildBuildsAndNotADevelopmentOne(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	asProduction := workspace.build(of("greet"), production())
	asDevelopment := workspace.build(of("greet"))

	shipped := workspace.release(spec.releaseOf("greet"))

	spec.Expect(asDevelopment.action("//services/greet:go.compile")).NotTo(Equal(asProduction.action("//services/greet:go.compile")), "the profiles build greet alike, so this proves nothing")
	spec.Expect(shipped.built().action("//services/greet:go.compile")).To(Equal(asProduction.action("//services/greet:go.compile")))
}

// Releasing a plan / A synced group is tagged once, with its unit's name.
func TestReleasingAPlanASyncedGroupIsTaggedOnceWithItsUnitSName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")

	shipped := workspace.release(spec.releaseOf("banner"))

	spec.Expect(shipped.tagsOf("kiosk")).To(Equal([]string{"services:kiosk/v1.0.0"}))
	spec.Expect(workspace.tagsAtHead("services")).To(Equal([]string{"kiosk/v1.0.0"}))
}

// Releasing a plan / A synced domain is built for production and tagged once, with its name.
func TestReleasingAPlanASyncedDomainIsBuiltForProductionAndTaggedOnceWithItsName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.syncedRetail().releaseWorkspace().onBranch("services", "release/1.0")

	shipped := workspace.release(spec.releaseOf("domain:retail"))

	spec.Expect(shipped.state()).To(Equal("succeeded"), shipped.release.GetFailure())
	spec.Expect(shipped.tagsOf("retail")).To(Equal([]string{"services:retail/v1.0.0"}))
	spec.Expect(workspace.tagsAtHead("services")).To(Equal([]string{"retail/v1.0.0"}))
	spec.Expect(shipped.built().actions()).To(ContainElements("//services/greet:go.compile", "//services/ticker:go.compile"))
}

// Releasing a plan / A unit that holds is neither built nor tagged, and one that moves beside it is both.
func TestReleasingAPlanAUnitThatHoldsIsNeitherBuiltNorTaggedAndOneThatMovesBesideItIsBoth(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0").tagged("services", "greet/v1.0.0").tagged("services", "kiosk/v1.0.0")
	workspace.commits("fix: scroll the ticker", "services/ticker/ticker.go")

	shipped := workspace.release(spec.releaseOf("greet", "system:kiosk"))

	spec.Expect(shipped.tagsOf("kiosk")).To(Equal([]string{"services:kiosk/v1.0.1"}))
	spec.Expect(workspace.tagsAtHead("services")).To(Equal([]string{"kiosk/v1.0.1"}))
	spec.Expect(shipped.release.GetUnits()).To(HaveLen(1))
	spec.Expect(shipped.built().actions()).To(ContainElement("//services/ticker:go.compile"))
	spec.Expect(shipped.built().actions()).NotTo(ContainElement("//services/greet:go.compile"))
	spec.Expect(workspace.build(of("greet")).actions()).To(ContainElement("//services/greet:go.compile"), "greet has no such action, so its absence proves nothing")
}

// Releasing a plan / Releasing what is released plans the same etag and changes nothing.
func TestReleasingWhatIsReleasedPlansTheSameEtagAndChangesNothing(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	planned := workspace.plan(spec.releaseOf("greet"))
	first := planned.release()

	after := workspace.plan(spec.releaseOf("greet"))
	again := after.release()

	spec.Expect(first.already("greet")).To(BeFalse())
	spec.Expect(after.unit("greet").why()).To(Equal([]string{"released"}))
	spec.Expect(after.etag()).To(Equal(planned.etag()))
	spec.Expect(again.already("greet")).To(BeTrue())
	spec.Expect(again.release.GetBuildId()).To(BeEmpty(), "what is released was built again")
	spec.Expect(workspace.tagsAtHead("services")).To(Equal([]string{"greet/v1.0.0"}))
}

// Releasing a plan / A plan whose source moved since is refused, and planned again it is released.
func TestReleasingAPlanAPlanWhoseSourceMovedSinceIsRefusedAndPlannedAgainItIsReleased(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	fresh, refusal := workspace.releasingAPlanOfGreetThatMoved()

	spec.Expect(refusal).To(BeRefusedSaying("the plan is no longer what releasing would do; plan again"))
	spec.Expect(spec.refusedAs(refusal)).To(Equal("aborted"))
	// What moved it is a commit of the workspace's own, pushed nowhere.
	spec.Expect(fresh.state()).To(Equal("unpublished"))
}

// Releasing a plan / What is not committed is not released: the repository is refused until it is its commit.
func TestWhatIsNotCommittedIsNotReleasedTheRepositoryIsRefusedUntilItIsItsCommit(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	workspace.edits("services/greet/greet.go")

	_, refusal := workspace.plan(spec.releaseOf("greet")).releasing()
	workspace.commitsAll("services", "fix: trim the greeting")
	shipped := workspace.release(spec.releaseOf("greet"))

	spec.Expect(refusal).To(BeRefusedSaying("services has changes that are not committed (greet/greet.go); a release is of a commit"))
	spec.Expect(spec.refusedAs(refusal)).To(Equal("failed_precondition"))
	// Its commit is the workspace's own, pushed nowhere: tagged, not sent.
	spec.Expect(shipped.state()).To(Equal("unpublished"), shipped.release.GetFailure())
}

// Releasing a plan / A unit whose production build fails is not tagged, and the release says which build.
func TestAUnitWhoseProductionBuildFailsIsNotTaggedAndTheReleaseSaysWhichBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	workspace.breaks("services/greet/greet.go")

	shipped := workspace.release(spec.releaseOf("greet"))

	spec.Expect(shipped.state()).To(Equal("failed"))
	spec.Expect(shipped.built().outcomeOf("//services/greet:go.compile")).To(Equal("failed"))
	spec.Expect(shipped.release.GetFailure()).To(ContainSubstring("the production build " + shipped.release.GetBuildId()))
	spec.Expect(shipped.release.GetUnits()).To(BeEmpty())
	spec.Expect(workspace.tagsAtHead("services")).To(BeEmpty())
}

// Releasing a plan / A release is read by its id.
func TestAReleaseIsReadByItsId(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	first := workspace.release(spec.releaseOf("greet"))
	workspace.release(spec.releaseOf("system:kiosk"))

	described, err := workspace.releasedBy(first.release.GetId())
	_, missing := workspace.releasedBy("rel_0000000000000000")

	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(described.GetState()).To(Equal(first.release.GetState()))
	spec.Expect(described.GetPlan().GetEtag()).To(Equal(first.release.GetPlan().GetEtag()))
	spec.Expect(spec.refusedAs(missing)).To(Equal("not_found"))
}

// Releasing a plan / A release is refused for what it is asked with, each in its own way.
func TestAReleaseIsRefusedForWhatItIsAskedWithEachInItsOwnWay(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.onRelease("1.0")
	planned := workspace.plan(spec.releaseOf("greet"))
	unnamed := &Plan{scenario: spec, of: workspace, request: planned.request, plan: &buildv1.ReleasePlan{}}

	_, none := unnamed.releasing()

	spec.Expect(spec.refusedAs(none)).To(Equal("invalid_argument"))
	spec.Expect(planned.release().state()).To(Equal("succeeded"))
}
