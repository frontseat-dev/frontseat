package build_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/testsupport/testpins"
	. "github.com/onsi/gomega"
)

// A Crossplane Configuration is a directory that holds its metadata,
// crossplane.yaml, and the YAML of its objects. Its package is built by
// `crossplane xpkg build`, with the crossplane the repository pins, into
// a file named for the Configuration, which is what its project ships;
// its examples are validated against its own definitions by `crossplane
// resource validate`, given nothing it would download.
//
// Crossplane writes a package of the objects it read, not of their
// files: the same objects give the same bytes, however the files that
// hold them were written. A Configuration whose metadata names no
// package is refused by name before anything runs, though Crossplane
// would build it.

// The fixture's actions, and the Component's own action that prints the
// package's digest, which reads what the package left.
const (
	crossplanePackage  = "//app:crossplane.package"
	crossplaneValidate = "//app:crossplane.validate"
	crossplaneDigest   = "//app:custom.digest"
)

// Crossplane Configurations / A Configuration built twice, of files that differ in nothing it holds, is the same package; one that holds something else is another.
func TestAConfigurationBuiltTwiceOfFilesThatDifferInNothingItHoldsIsTheSamePackage(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.crossplaneWorkspace()
	definition := workspace.file("app/apis/definition.yaml")
	// Each build is of a comment no build has had, so that the grid has
	// kept neither and both are run: the package holds no comment.
	written := func(when string) string {
		return fmt.Sprintf("# %s, at %d\n%s", when, time.Now().UnixNano(), definition)
	}
	workspace.write("app/apis/definition.yaml", written("built first"))
	first := workspace.build(of(crossplaneDigest)).succeeded()
	spec.Expect(first.cameFrom(crossplanePackage)).To(Equal("executed"))
	workspace.write("app/apis/definition.yaml", written("built again"))
	again := workspace.build(of(crossplaneDigest)).succeeded()
	spec.Expect(again.cameFrom(crossplanePackage)).To(Equal("executed"))
	// Two actions, each run, of one package.
	spec.Expect(again.action(crossplanePackage)).NotTo(Equal(first.action(crossplanePackage)))
	spec.Expect(digestIn(spec, again.printed(crossplaneDigest))).To(Equal(digestIn(spec, first.printed(crossplaneDigest))))

	// The control: a definition that says something else is another
	// package.
	workspace.write("app/apis/definition.yaml", strings.Replace(written("built otherwise"), "enum: [eu, us]", "enum: [eu, us, ap]", 1))
	otherwise := workspace.build(of(crossplaneDigest)).succeeded()
	spec.Expect(digestIn(spec, otherwise.printed(crossplaneDigest))).NotTo(Equal(digestIn(spec, first.printed(crossplaneDigest))))
}

// Crossplane Configurations / A Configuration whose metadata names no package is refused by name, and one that names it is built.
func TestAConfigurationWhoseMetadataNamesNoPackageIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.crossplaneWorkspace()
	named := workspace.file("app/crossplane.yaml")
	workspace.write("app/crossplane.yaml", strings.Replace(named, "metadata:\n  name: network\n", "", 1))
	_, finding := workspace.projectRefused("INVALID_MANIFEST", "crossplane.plugins.frontseat.dev")
	spec.Expect(finding.GetRuleId()).To(Equal("crossplane/invalid-manifest"))
	spec.Expect(finding.GetSourceLocation().GetPath()).To(Equal("app/crossplane.yaml"))
	spec.Expect(workspace.build().outcome()).To(Equal("BUILD_FAILURE"))

	// The control: the same metadata, naming its package, is a project,
	// and its package is built.
	workspace.write("app/crossplane.yaml", named)
	spec.Expect(rootsOf(workspace.projects())).To(ContainElement("app"))
	workspace.build(of(crossplanePackage)).succeeded()
}

// Crossplane Configurations / What validate finds in an example is said at the example, and an example its definition holds passes.
func TestWhatValidateFindsInAnExampleIsSaidAtTheExample(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.crossplaneWorkspace()
	passed := workspace.build(of(crossplaneValidate)).succeeded()
	spec.Expect(passed.diagnosticsOf(crossplaneValidate)).To(BeEmpty())

	workspace.write("app/examples/network.yaml", strings.Replace(workspace.file("app/examples/network.yaml"), "region: eu", "region: mars", 1))
	failed := workspace.build(of(crossplaneValidate))
	spec.Expect(failed.outcomeOf(crossplaneValidate)).To(Equal("failed"), failed.diagnostics())
	found := failed.diagnosticsOf(crossplaneValidate)
	spec.Expect(found).To(HaveLen(1), failed.diagnostics())
	spec.Expect(found[0].GetRuleId()).To(Equal("crossplane/validate"))
	spec.Expect(found[0].GetMessage()).To(SatisfyAll(HavePrefix("Network default/eu: spec.region"), ContainSubstring("mars")))
	spec.Expect(found[0].GetSourceLocation().GetPath()).To(Equal("app/examples/network.yaml"))
	spec.Expect(found[0].GetSourceLocation().GetLine()).To(Equal(int32(1)))
}

// crossplaneWorkspace is the Crossplane plugin's fixture workspace, pinned
// as this repository is, and to crossplane: app is a Configuration of one definition, one
// Composition and one example. It is also a component with an action of
// its own, which reads the package and prints its sha256, so that a spec
// sees what the build left through the API; its catalog file beside the
// package is no object of it.
func (spec *scenario) crossplaneWorkspace() *Workspace {
	spec.Helper()
	workspace := spec.fixtureWorkspace("crossplane", "plugins:\n  crossplane: {}\n  backstage: {}\n")
	// This repository pins no crossplane: the fixture is pinned it as a
	// test's alone is.
	testpins.Crossplane(spec.TB, workspace.directory)
	return workspace.write("app/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: app\nspec:\n  type: library\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n      digest:\n        command: [sha256sum, network.xpkg]\n        inputs: [\":crossplane.package\"]\n")
}

// digestIn is the sha256 sha256sum printed of the package.
func digestIn(spec *scenario, printed string) string {
	spec.Helper()
	fields := strings.Fields(printed)
	spec.Expect(fields).To(HaveLen(2), printed)
	spec.Expect(fields[1]).To(Equal("network.xpkg"), printed)
	spec.Expect(fields[0]).To(HaveLen(64), printed)
	return fields[0]
}
