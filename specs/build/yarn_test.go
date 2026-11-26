package build_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/specs/buildtest"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"

	. "github.com/onsi/gomega"
)

// yarnWorkspace is a workspace pinned with Node and Yarn, listing the
// plugins named, that holds what testdata/<fixture> holds, beneath
// directory (the workspace's root where it is empty).
func (spec *scenario) yarnWorkspace(plugins, fixture, directory string) *Workspace {
	spec.Helper()
	root := spec.TB.TempDir()
	testpins.Node(spec.TB, root)
	testpins.Yarn(spec.TB, root)
	w := &Workspace{scenario: spec, directory: root, at: buildtest.Mounted(spec.TB, root)}
	w.write("frontseat.yaml", "plugins: {"+plugins+"}\n")
	from := filepath.Join("testdata", fixture)
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		w.write(filepath.ToSlash(filepath.Join(directory, relative)), string(content))
		return err
	})
	spec.Expect(err).NotTo(HaveOccurred())
	return w
}

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
