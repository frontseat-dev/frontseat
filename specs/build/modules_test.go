package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// The repository's real modules are built through the API on the grid. Local
// requirements are inferred from go.mod and handed to Go as source. Each module
// owns its inputs: changing an unrelated module must not change this action,
// while changing a required module must change it.
// The repository's Go module boundaries / The client builds from its local modules and only their inputs affect its action.
func TestTheClientBuildsFromItsLocalModulesAndOnlyTheirInputsAffectItsAction(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.ownCheckout()
	before := workspace.build(of("./cli"), only("compile"))
	spec.Expect(before.outcome()).To(Equal("SUCCESS"), before.diagnostics())
	workspace.write("specs/compare/doc.go", workspace.file("specs/compare/doc.go")+"\n// An unrelated comparison change.\n")
	unrelated := workspace.build(of("./cli"), only("compile"))
	spec.Expect(unrelated.outcome()).To(Equal("SUCCESS"), unrelated.says())
	spec.Expect(unrelated.action("//cli:go.compile")).To(Equal(before.action("//cli:go.compile")))
	workspace.write("sdk/go/entity/reference.go", workspace.file("sdk/go/entity/reference.go")+"\n// A required change to the Go SDK.\n")
	changed := workspace.build(of("./cli"), only("compile"))
	spec.Expect(changed.outcome()).To(Equal("SUCCESS"), changed.says())
	spec.Expect(changed.action("//cli:go.compile")).NotTo(Equal(before.action("//cli:go.compile")))
}

// The repository's Go module boundaries / The server builds with its extracted build, workspace and sandbox features.
func TestTheServerBuildsWithItsExtractedBuildWorkspaceAndSandboxFeatures(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.ownCheckout().build(of("./server"), only("compile"))
	spec.Expect(build.outcome()).To(Equal("SUCCESS"), build.diagnostics())
}
