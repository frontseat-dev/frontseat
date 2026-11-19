package build_test

import (
	"testing"

	. "github.com/onsi/gomega"
)

// A repository says what frontseat must not see in .frontseatignore at its
// root, written as git reads .gitignore. What it names is not part of the
// repository to a run: no plugin discovers a project there, reads a file
// there or is handed one. Fixtures that are meant to be malformed, and
// examples that are not the repository's own code, are what it is for.
// Without it, every manifest in the repository is one of the
// repository's, as the control shows.
// What a repository says frontseat must not see / A malformed manifest anywhere in a repository fails its build.
func TestAMalformedManifestAnywhereInARepositoryFailsItsBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.embeddedModule("nested")
	workspace.write("examples/broken/go.mod", "go 1.27\n")
	spec.Expect(workspace.projectFailure().GetMessage()).To(ContainSubstring("examples/broken/go.mod"))
}

// What a repository says frontseat must not see / What .frontseatignore names is neither discovered nor built.
func TestWhatFrontseatignoreNamesIsNeitherDiscoveredNorBuilt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.embeddedModule("nested")
	workspace.write("examples/broken/go.mod", "go 1.27\n")
	workspace.write("examples/broken/catalog-info.yaml", "not a catalog file\n")
	workspace.write("frontseat.yaml", "plugins: {go: {}, backstage: {}}\n")
	workspace.write(".frontseatignore", "# fixtures, which are meant to be wrong\nexamples/\n")
	spec.Expect(rootsOf(workspace.projects())).To(ConsistOf("nested"))
	spec.Expect(workspace.catalogEntities()).To(BeEmpty())
	built := workspace.build()
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.diagnostics())
}

// What a repository says frontseat must not see / .frontseatignore is read as git reads .gitignore: patterns anchor, match directories at any depth, and negate.
func TestFrontseatignoreIsReadAsGitReadsGitignorePatternsAnchorMatchDirectoriesAtAnyDepthAndNegate(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.embeddedModule("nested")
	workspace.write("examples/broken/go.mod", "go 1.27\n")
	workspace.write("examples/kept/go.mod", "module example.com/kept\n\ngo 1.27\n")
	workspace.write("examples/kept/kept.go", "package kept\n")
	workspace.write("deep/broken/go.mod", "go 1.27\n")
	workspace.write("deep/other/go.mod", "module example.com/other\n\ngo 1.27\n")
	workspace.write("deep/other/other.go", "package other\n")
	workspace.write(".frontseatignore", "/examples/*/\n!/examples/kept/\nbroken/\n")
	spec.Expect(rootsOf(workspace.projects())).To(ConsistOf("nested", "examples/kept", "deep/other"))
}
