package build_test

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
)

// What a project requires from outside the workspace is fetched: a
// module its go.mod requires and nothing in the workspace provides, at
// the version its lock says.
//
// The ecosystem's own tool fetches, from the lock alone, in an action for
// each module the lock holds: for a Go module, go mod download of that
// module, given its own lines of the project's go.sum and nothing else.
// Such a fetch belongs to no project. It reads no source, so it is the
// same action whichever project of the repository locks the module, and
// a run fetches it once; it is named after the repository and the module,
// as //lab:go.fetch/github.com/spf13/cobra@v1.8.1. A module the lock holds
// only the go.mod of is fetched the same way, as go.metadata.
//
// A fetch is the one kind of action that reaches outside its sandbox,
// and it asks the proxy, not the public registry. What it fetched is kept
// by the grid, and handed to the actions that build, which reach nothing.
//
// The lock decides. The tool checks what it fetched against it, and a
// module that is not what the lock says fails the build before anything
// is built with it. Because a fetch reads one module's lines and nothing
// else, a change to one module of a lock fetches that module again and
// no other. A fetch runs with its tools' pins alone, and with what mise
// reads when it runs them: a task, or another tool's pin, changes none, and
// the environment the tools run with changes every one. Two projects that
// lock one version differently cannot share
// its fetch, and the build is refused, naming both.
//
// The specs use the company's source. In the repository lab, tool is a
// program that requires a module from outside, which requires two more.
// What a project requires from outside / A module that requires a module from outside is built.
func TestAModuleThatRequiresAModuleFromOutsideIsBuilt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("tool").build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
}

// What a project requires from outside / Each module it locks is fetched by an action of its own.
func TestEachModuleItLocksIsFetchedByAnActionOfItsOwn(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("tool").build()

	spec.Expect(build.actions()).To(ContainElements(cobraFetch, pflagFetch, mousetrapFetch, metadataOf("gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405")))
	spec.Expect(build.actions()).NotTo(ContainElement("//lab/tool:go.fetch"))
}

// What a project requires from outside / A fetch belongs to no entity.
func TestAFetchBelongsToNoEntity(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("tool").build()

	spec.Expect(build.ownerOf(cobraFetch)).To(BeEmpty())
	spec.Expect(build.ownerOf("//lab/tool:go.compile")).To(Equal("component:default/tool"))
}

// What a project requires from outside / A fetch asks the proxy.
func TestAFetchAsksTheProxy(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("tool").build()

	spec.Expect(build.printed(cobraFetch)).To(ContainSubstring("# get " + testgrid.GoProxy + "/github.com/spf13/cobra/@v/v1.8.1.zip"))
}

// What a project requires from outside / A fetch asks nothing but the proxy.
func TestAFetchAsksNothingButTheProxy(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("tool").build()

	for _, fetch := range spec.fetches(build) {
		spec.Expect(asked(build.printed(fetch))).To(HaveEach(HavePrefix(testgrid.GoProxy+"/")), fetch)
	}
}

// What a project requires from outside / A fetch asks for its own module and no other.
func TestAFetchAsksForItsOwnModuleAndNoOther(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("tool").build()

	spec.Expect(asked(build.printed(cobraFetch))).To(HaveEach(ContainSubstring("/github.com/spf13/cobra/@v/")))
}

// What a project requires from outside / A module that requires nothing from outside fetches nothing.
func TestAModuleThatRequiresNothingFromOutsideFetchesNothing(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("log").build()

	spec.Expect(spec.fetches(build)).To(BeEmpty())
}

// What a project requires from outside / The actions that build wait for what was fetched.
func TestTheActionsThatBuildWaitForWhatWasFetched(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("tool").build()

	spec.Expect(build.needsOf("//lab/tool:go.compile")).To(ContainElements(cobraFetch, pflagFetch, mousetrapFetch))
	spec.Expect(build.needsOf("//lab/tool:go.compile")).To(ConsistOf(spec.fetches(build)))
}

// What a project requires from outside / A module two projects lock alike is fetched once, for both.
func TestAModuleTwoProjectsLockAlikeIsFetchedOnceForBoth(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource()
	spec.kit(source, testforge.ToolLock)

	build := source.workspaceOf("tool").build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"), build.diagnostics())
	spec.Expect(spec.labelled(build, cobraFetch)).To(Equal(1))
	spec.Expect(build.needsOf("//lab/tool/kit:go.compile")).To(ContainElement(cobraFetch))
	spec.Expect(build.needsOf("//lab/tool:go.compile")).To(ContainElement(cobraFetch))
}

// What a project requires from outside / A module locked at two versions, one for its go.mod alone, is built with both.
func TestAModuleLockedAtTwoVersionsOneForItsGoModAloneIsBuiltWithBoth(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource()
	source.Write(spec.TB, "lab", "tool/go.sum", testforge.ToolLock+"github.com/spf13/pflag v1.0.3/go.mod h1:DYY7MBk1bdzusC3SYhjObp+wFpr4gzcvqqNjLnInEg4=\n")

	build := source.workspaceOf("tool").build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"), build.diagnostics())
	spec.Expect(build.actions()).To(ContainElements(pflagFetch, metadataOf("github.com/spf13/pflag@v1.0.3")))
}

// What a project requires from outside / Two projects that lock one version differently are refused, naming both.
func TestTwoProjectsThatLockOneVersionDifferentlyAreRefusedNamingBoth(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource()
	spec.kit(source, spec.wrongChecksum(testforge.ToolLock, "github.com/spf13/pflag", false))

	build := source.workspaceOf("tool").build()

	spec.Expect(build.outcome()).NotTo(Equal("SUCCESS"))
	spec.Expect(build.says()).To(SatisfyAll(ContainSubstring("github.com/spf13/pflag@v1.0.5"), ContainSubstring(`"lab/tool"`), ContainSubstring(`"lab/tool/kit"`)))
}

// What a project requires from outside / A change to the source fetches nothing again.
func TestWhatAProjectRequiresFromOutsideAChangeToTheSourceFetchesNothingAgain(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("tool")
	before := workspace.build()

	workspace.write("lab/tool/main.go", "package main\n\nimport \"github.com/spf13/cobra\"\n\nfunc main() { _ = (&cobra.Command{Use: \"changed\"}).Execute() }")
	workspace.write("lab/tool/go.mod", workspace.file("lab/tool/go.mod")+"\n// changed\n")
	after := workspace.build()

	spec.Expect(after.action(cobraFetch)).To(Equal(before.action(cobraFetch)))
	spec.Expect(after.cameFrom(cobraFetch)).To(Equal("grid_cache"))
}

// What a project requires from outside / A change to a task or to another tool's pin fetches and builds nothing again.
func TestAChangeToATaskOrToAnotherToolSPinFetchesAndBuildsNothingAgain(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("tool")
	before := workspace.build()

	pins := workspace.file("lab/mise.toml")
	workspace.write("lab/mise.toml", strings.Replace(pins, "[tools]\n", "[tools]\njq = \"1.7.1\"\n", 1)+"\n\n[tasks.lint]\nrun = \"go vet ./...\"\n")
	after := workspace.build()

	spec.Expect(after.outcome()).To(Equal("SUCCESS"), after.diagnostics())
	for _, label := range append(spec.fetches(before), "//lab/tool:go.compile") {
		spec.Expect(after.action(label)).To(Equal(before.action(label)), label)
		spec.Expect(after.cameFrom(label)).To(Equal("grid_cache"), label)
	}
}

// What a project requires from outside / A change to the environment its tools run with fetches again.
func TestAChangeToTheEnvironmentItsToolsRunWithFetchesAgain(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("tool")
	before := workspace.build()

	workspace.write("lab/mise.toml", workspace.file("lab/mise.toml")+"\n\n[env]\nFRONTSEAT_SPEC = \"changed\"\n")
	after := workspace.build()

	spec.Expect(after.outcome()).To(Equal("SUCCESS"), after.diagnostics())
	spec.Expect(after.action(cobraFetch)).NotTo(BeEmpty())
	spec.Expect(after.action(cobraFetch)).NotTo(Equal(before.action(cobraFetch)))
}

// What a project requires from outside / A change to one module of the lock fetches that module again, and no other.
func TestAChangeToOneModuleOfTheLockFetchesThatModuleAgainAndNoOther(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("tool")
	before := workspace.build()

	workspace.locksPflagWrongly()
	after := workspace.build()

	spec.Expect(after.action(pflagFetch)).NotTo(Equal(before.action(pflagFetch)))
	spec.Expect(after.action(cobraFetch)).To(Equal(before.action(cobraFetch)))
	spec.Expect(after.action(mousetrapFetch)).To(Equal(before.action(mousetrapFetch)))
}

// What a project requires from outside / A module that is not what the lock says fails the fetch.
func TestAModuleThatIsNotWhatTheLockSaysFailsTheFetch(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("tool")
	workspace.locksPflagWrongly()

	build := workspace.build()

	spec.Expect(build.printed(pflagFetch)).To(ContainSubstring("verifying github.com/spf13/pflag@v1.0.5: checksum mismatch"))
}

// What a project requires from outside / Nothing is built with a module that is not what the lock says.
func TestNothingIsBuiltWithAModuleThatIsNotWhatTheLockSays(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("tool")
	workspace.locksPflagWrongly()

	build := workspace.build()

	spec.Expect(build.outcomeOf("//lab/tool:go.compile")).To(Equal("skipped"))
}

// What a project requires from outside / Locked module metadata is checked before compilation.
func TestLockedModuleMetadataIsCheckedBeforeCompilation(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("tool")
	control := workspace.build()
	spec.Expect(control.outcome()).To(Equal("SUCCESS"), control.diagnostics())
	checkV1 := metadataOf("gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405")
	workspace.write("lab/tool/go.sum", spec.wrongChecksum(testforge.ToolLock, "gopkg.in/check.v1", true))
	failed := workspace.build()
	spec.Expect(failed.printed(checkV1)).To(ContainSubstring("checksum mismatch"))
	spec.Expect(failed.outcomeOf("//lab/tool:go.compile")).To(Equal("skipped"))
}

// The fetches of the modules tool locks.
var (
	cobraFetch     = fetchOf("github.com/spf13/cobra@v1.8.1")
	pflagFetch     = fetchOf("github.com/spf13/pflag@v1.0.5")
	mousetrapFetch = fetchOf("github.com/inconshreveable/mousetrap@v1.1.0")
)

// fetchOf is the label of the fetch of one module of the repository lab.
func fetchOf(module string) string { return "//lab:go.fetch/" + module }

// metadataOf is the label of the fetch of one module's go.mod alone.
func metadataOf(module string) string { return "//lab:go.metadata/" + module }

// fetches are the build's fetches of modules and of their metadata, in
// any repository.
func (spec *scenario) fetches(b *Build) []string {
	spec.Helper()
	var out []string
	for _, label := range b.actions() {
		if strings.Contains(label, ":go.fetch/") || strings.Contains(label, ":go.metadata/") {
			out = append(out, label)
		}
	}
	return out
}

// labelled is how many of the build's results carry a label.
func (spec *scenario) labelled(b *Build, label string) int {
	spec.Helper()
	n := 0
	for _, result := range b.results() {
		if result.GetLabel() == label {
			n++
		}
	}
	return n
}

// kit is a second module inside tool's directory that requires what tool
// requires, with the lock given.
func (spec *scenario) kit(source *Source, lock string) {
	spec.Helper()
	source.Write(spec.TB, "lab", "tool/kit/go.mod", "module example.com/kit\n\ngo 1.27\n\nrequire github.com/spf13/cobra v1.8.1\n\nrequire (\n\tgithub.com/inconshreveable/mousetrap v1.1.0 // indirect\n\tgithub.com/spf13/pflag v1.0.5 // indirect\n)\n")
	source.Write(spec.TB, "lab", "tool/kit/go.sum", lock)
	source.Write(spec.TB, "lab", "tool/kit/kit.go", "package kit\n\nimport _ \"github.com/spf13/cobra\"\n")
}
