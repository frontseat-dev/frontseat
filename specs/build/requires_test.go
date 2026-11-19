package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// Nobody writes what a project needs of another. Its ecosystem's own
// files already say it: a go.mod requires a module, a pom.xml depends on
// an artifact, a package.json on a package. A plugin reads that, and
// says of its own project what it can be required by.
//
// Where what one project requires is something another project of the
// workspace provides, the first is built with the second: it is handed
// the other's source, or what the other built, whichever its tool
// needs, placed where its tool looks. For a Go module that is the source
// of the module it requires.
//
// What nothing in the workspace provides comes from outside it.
//
// Because what is handed is known by its content, a change in what a
// project requires changes what requires it, and nothing else.
//
// You can always ask what a project requires, where that is said, and
// what provides it. Nothing is run to answer.
//
// The specs use the company's source. greet is a library; hello, beside
// it in the repository services, and gateway, in the repository platform,
// call it, and each says so on the fifth line of its go.mod. log is a
// library nothing requires. In the repository lab, outsider requires a
// module nothing in the company holds, and twins is two modules that go
// by one name.
// What a project requires / A module that requires another in the workspace is built with it.
func TestAModuleThatRequiresAnotherInTheWorkspaceIsBuiltWithIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	spec.workspaceOf("gateway").build().succeeded()
}

// What a project requires / A module that requires one beside it in its repository is built with it.
func TestAModuleThatRequiresOneBesideItInItsRepositoryIsBuiltWithIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("hello").build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
}

// What a project requires / What a project requires is said with the line that says it, the version its lock resolves it to, and what provides it.
func TestWhatAProjectRequiresIsSaidWithTheLineThatSaysItTheVersionItsLockResolvesItToAndWhatProvidesIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	spec.Expect(workspace.requires("platform/gateway")).To(Equal([]Requirement{{
		what:       "go:example.com/greet",
		saidAt:     "platform/gateway/go.mod:5",
		at:         "v0.0.0",
		providedBy: "services/greet",
	}}))
}

// What a project requires / What nothing in the workspace provides is from outside it.
func TestWhatNothingInTheWorkspaceProvidesIsFromOutsideIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("outsider")

	spec.Expect(workspace.requires("lab/outsider")).To(Equal([]Requirement{{
		what:       "go:example.com/elsewhere",
		saidAt:     "lab/outsider/go.mod:5",
		at:         "v1.0.0",
		providedBy: "outside the workspace",
	}}))
}

// What a project requires / A change in what a project requires changes what requires it.
func TestAChangeInWhatAProjectRequiresChangesWhatRequiresIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")
	before := workspace.build()

	workspace.write("services/greet/greet.go", "package greet\n\n// Hello is a greeting.\nfunc Hello() string { return \"good day\" }")
	after := workspace.build()

	spec.Expect(after.action("//platform/gateway:go.compile")).NotTo(Equal(before.action("//platform/gateway:go.compile")))
}

// What a project requires / A change in what a project requires changes nothing that does not require it.
func TestAChangeInWhatAProjectRequiresChangesNothingThatDoesNotRequireIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway").add("log")
	before := workspace.build()

	workspace.write("services/greet/greet.go", "package greet\n\n// Hello is a greeting.\nfunc Hello() string { return \"good day\" }")
	after := workspace.build()

	spec.Expect(after.action("//platform/log:go.compile")).To(Equal(before.action("//platform/log:go.compile")))
}

// What a project requires / A change in the tests of what a project requires changes nothing that requires it.
func TestAChangeInTheTestsOfWhatAProjectRequiresChangesNothingThatRequiresIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")
	before := workspace.build()

	workspace.write("services/greet/greet_test.go", "package greet\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() == \"\" {\n\t\tt.Fatal(\"no greeting\")\n\t}\n}")
	after := workspace.build()

	spec.Expect(after.action("//platform/gateway:go.compile")).To(Equal(before.action("//platform/gateway:go.compile")))
}

// What a project requires / Two projects that provide the same thing are refused, naming both.
func TestTwoProjectsThatProvideTheSameThingAreRefusedNamingBoth(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("twins").build()

	spec.Expect(build.says()).To(ContainSubstring("go:example.com/twin is provided by lab/twins/one (go) and lab/twins/two (go)"))
}
