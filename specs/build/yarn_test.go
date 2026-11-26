package build_test

import (
	"testing"

	. "github.com/onsi/gomega"
)

// Yarn / A workspace is installed offline from what its fetch kept, as
// one project.
func TestAYarnWorkspaceIsInstalledOfflineFromWhatItsFetchKept(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	// A Yarn workspace of two packages, as Yarn 4 wrote its lock: app
	// depends on lib and on is-number 7, lib on is-number 6, which Yarn
	// cannot hoist beside 7.
	workspace := spec.yarnWorkspace("yarn: {}, backstage: {}", "yarn", "portal")
	var kinds []string
	for _, project := range workspace.projects() {
		kinds = append(kinds, project.GetKind())
	}
	spec.Expect(kinds).To(ConsistOf("yarn-install"))
	const install = "//portal:yarn.install"
	built := workspace.build(of(install))
	spec.Expect(built.says()).To(BeEmpty())
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), "%s\n%s\n%s", built.diagnostics(), built.printed("//portal:yarn.fetch"), built.printed(install))
	spec.Expect(built.printed(install)).To(ContainSubstring("Link step"))
	// Built again with nothing changed, nothing is installed again.
	spec.Expect(workspace.build(of(install)).cameFrom(install)).NotTo(Equal("executed"))
	// A manifest the lock no longer matches is refused by Yarn, as a
	// developer's `yarn install --immutable` refuses it, and nothing is
	// installed.
	workspace.write("portal/packages/lib/package.json", `{"name":"lib","version":"1.0.0","dependencies":{"is-number":"5.0.0"}}`)
	stale := workspace.build(of(install))
	spec.Expect(stale.outcome()).To(Equal("BUILD_FAILURE"), stale.diagnostics())
	spec.Expect(stale.printed("//portal:yarn.fetch")).To(ContainSubstring("YN0028"))
	spec.Expect(stale.outcomeOf(install)).To(Equal("skipped"))
}
