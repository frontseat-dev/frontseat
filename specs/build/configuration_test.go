package build_test

import (
	"testing"

	. "github.com/onsi/gomega"
)

// A repository says how it is built in one file at its root,
// frontseat.yaml: which plugins build it, and what it sets of the options
// those plugins take.
//
//	plugins:
//	  go:
//	    test-timeout: 2m
//
// An option is something a plugin has said it takes, with a kind and a
// value it has when nobody sets it. You cannot set what a plugin does not
// take: a misspelt option is refused, not ignored.
//
// A repository's setting holds for everything in it. An entity can set an
// option too, in its own catalog file, for its own directory and what is
// beneath:
//
//	spec:
//	  frontseat:
//	    plugins:
//	      go:
//	        test-timeout: 30s
//
// The nearest setting wins: the entity's over the repository's, the
// repository's over the plugin's own. You can always ask what a module is
// built with, and where each value comes from.
//
// Each repository of a workspace is read on its own. What one says does
// not reach another.
//
// The specs use the company's source, changed where a spec says so. The
// Go plugin takes the option test-timeout, how long a module's tests may
// run, which is 10m unless set, and tags, a list of build tags.
// The settings of test-timeout the specs read: the plugin's own, the
// platform repository's and gateway's.
var (
	pluginTimeout     = Setting{value: "10m", from: "the plugin"}
	repositoryTimeout = Setting{value: "2m", from: "platform/frontseat.yaml"}
	entityTimeout     = Setting{value: "30s", from: "platform/gateway/catalog-info.yaml"}
)

// Configuration / An option has its plugin's value until something sets it.
func TestAnOptionHasItsPluginSValueUntilSomethingSetsIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	spec.Expect(workspace.option("platform/gateway", "test-timeout")).To(Equal(pluginTimeout))
}

// Configuration / A repository sets an option for everything in it.
func TestARepositorySetsAnOptionForEverythingInIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.platformTimeoutWorkspace()

	spec.Expect(workspace.option("platform/gateway", "test-timeout")).To(Equal(repositoryTimeout))
}

// Configuration / What one repository sets does not reach another.
func TestWhatOneRepositorySetsDoesNotReachAnother(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.platformTimeoutWorkspace()

	spec.Expect(workspace.option("services/greet", "test-timeout")).To(Equal(pluginTimeout))
}

// Configuration / An entity sets an option for its own directory.
func TestAnEntitySetsAnOptionForItsOwnDirectory(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().entitySets("gateway", "test-timeout", "30s")

	workspace := source.workspaceOf("gateway")

	spec.Expect(workspace.option("platform/gateway", "test-timeout")).To(Equal(entityTimeout))
}

// Configuration / What an entity sets does not reach the directory beside it.
func TestWhatAnEntitySetsDoesNotReachTheDirectoryBesideIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().entitySets("gateway", "test-timeout", "30s")

	workspace := source.workspaceOf("gateway").add("log")

	spec.Expect(workspace.option("platform/log", "test-timeout")).To(Equal(pluginTimeout))
}

// Configuration / The nearest setting wins.
func TestTheNearestSettingWins(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().
		sets("platform", "test-timeout", "2m").
		entitySets("gateway", "test-timeout", "30s")

	workspace := source.workspaceOf("gateway")

	spec.Expect(workspace.option("platform/gateway", "test-timeout")).To(Equal(entityTimeout))
}

// Configuration / A list is set whole.
func TestAListIsSetWhole(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().sets("platform", "tags", "[integration, slow]")

	workspace := source.workspaceOf("gateway")

	spec.Expect(workspace.option("platform/gateway", "tags")).To(Equal(Setting{value: []string{"integration", "slow"}, from: "platform/frontseat.yaml"}))
}

// Configuration / The graph preserves list entries containing commas.
func TestTheGraphPreservesListEntriesContainingCommas(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	one := spec.changedSource().sets("platform", "tags", "['integration, slow']").workspaceOf("gateway")
	two := spec.changedSource().sets("platform", "tags", "[integration, slow]").workspaceOf("gateway")

	spec.Expect(one.option("platform/gateway", "tags").value).NotTo(Equal(two.option("platform/gateway", "tags").value))
	spec.Expect(one.option("platform/gateway", "tags").value).To(Equal([]string{"integration, slow"}))
	spec.Expect(two.option("platform/gateway", "tags").value).To(Equal([]string{"integration", "slow"}))
}

// Configuration / An option a plugin does not take is refused by name.
func TestAnOptionAPluginDoesNotTakeIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.misspeltOptionBuild()

	spec.Expect(build.says()).To(ContainSubstring("the go plugin takes no option test-timout"))
}

// Configuration / A value of the wrong kind is refused.
func TestAValueOfTheWrongKindIsRefused(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().sets("platform", "tags", "integration")

	build := source.workspaceOf("gateway").build()

	spec.Expect(build.says()).To(ContainSubstring("tags is a list"))
}

// Configuration / A mistake in configuration fails the build.
func TestAMistakeInConfigurationFailsTheBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.misspeltOptionBuild()

	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}

// Configuration / A plugin a repository does not list does nothing there.
func TestAPluginARepositoryDoesNotListDoesNothingThere(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().listsNoPlugins("platform")

	build := source.workspaceOf("gateway").build()

	spec.Expect(build.covers()).To(Equal([]string{"services/greet"}))
}

// Configuration / A plugin nobody has is refused by name.
func TestAPluginNobodyHasIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().lists("platform", "cobol")

	build := source.workspaceOf("gateway").build()

	spec.Expect(build.says()).To(ContainSubstring("there is no plugin cobol"))
}

// Configuration / A repository that does not say how it is built is not built.
func TestARepositoryThatDoesNotSayHowItIsBuiltIsNotBuilt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource().saysNothing("platform")

	build := source.workspaceOf("gateway").build()

	spec.Expect(build.says()).To(ContainSubstring("platform has no frontseat.yaml"))
}
