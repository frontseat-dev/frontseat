package build_test

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

// What an action of a Go module is given is what the go command reads
// to do it: the Go files, and with them what they embed, the assembly
// beside them with the headers it includes, and for the tests the
// fixtures they open. An action sees nothing it was not given, so a
// file left out is a build that fails here and nowhere else.
// What a Go module is built from / A go.mod under a directory named testdata is a fixture and not a module, as the go command has it.
func TestAGoModUnderADirectoryNamedTestdataIsAFixtureAndNotAModuleAsTheGoCommandHasIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.embeddedModule("app")
	workspace.write("app/testdata/fixture/go.mod", "go 1.27\n")
	workspace.write("app/testdata/fixture/main.go", "package main\nfunc main() {}\n")
	spec.Expect(rootsOf(workspace.projects())).To(ConsistOf("app"))
	// The same files under any other name are a module, and a broken one.
	workspace.write("app/fixtures/fixture/go.mod", "go 1.27\n")
	spec.Expect(workspace.projectFailure().GetMessage()).To(ContainSubstring("app/fixtures/fixture/go.mod"))
}

// What a Go module is built from / Go embed directives preserve indentation and Go string escapes.
func TestGoEmbedDirectivesPreserveIndentationAndGoStringEscapes(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	for _, directive := range []string{
		"//go:embed asset.txt",
		"\t//go:embed asset.txt",
		`//go:embed "\x61sset.txt"`,
		`//go:embed "\141sset.txt"`,
		`//go:embed "\u0061sset.txt"`,
	} {
		workspace := spec.embeddedModule("app")
		workspace.write("app/main.go", "package main\n\nimport _ \"embed\"\n\n"+directive+"\nvar asset string\n\nfunc main() { println(asset) }")
		build := workspace.build(of("./app"), only("compile"))
		spec.Expect(build.outcome()).To(Equal("SUCCESS"), directive+"\n"+build.diagnostics())
	}
}

// What a Go module is built from / A module directory is a literal name when discovering embedded files.
func TestAModuleDirectoryIsALiteralNameWhenDiscoveringEmbeddedFiles(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	for _, root := range []string{"ordinary", "pkg[1]"} {
		workspace := spec.embeddedModule(root)
		build := workspace.build(of("./"+root), only("compile"))
		spec.Expect(build.outcome()).To(Equal("SUCCESS"), build.diagnostics())
		workspace.write("pkg1/unrelated.go", "package other\n")
		again := workspace.build(of("./"+root), only("compile"))
		spec.Expect(again.outcome()).To(Equal("SUCCESS"), again.diagnostics())
		spec.Expect(again.action("//" + root + ":go.compile")).To(Equal(build.action("//" + root + ":go.compile")))
		workspace.write(root+"/asset.txt", "changed embedded content\n")
		changed := workspace.build(of("./"+root), only("compile"))
		spec.Expect(changed.outcome()).To(Equal("SUCCESS"), changed.diagnostics())
		spec.Expect(changed.action("//" + root + ":go.compile")).NotTo(Equal(build.action("//" + root + ":go.compile")))
	}
}

// What a Go module is built from / A module is built with the files it embeds.
func TestAModuleIsBuiltWithTheFilesItEmbeds(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("embeds").build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
}

// What a Go module is built from / A file a module embeds is one it is built from: changed, the module is built again.
func TestAFileAModuleEmbedsIsOneItIsBuiltFromChangedTheModuleIsBuiltAgain(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("embeds")
	workspace.build()

	// What it is changed to, no build before this one was made from.
	second := workspace.write("lab/embeds/static/deep/more.css", "/* "+time.Now().Format(time.RFC3339Nano)+" */").build()

	spec.Expect(second.outcome()).To(Equal("SUCCESS"))
	// Its lint, format and compile, the program of its tests, and its
	// test, whose program embeds the file too.
	spec.Expect(second.ranOnAWorker()).To(Equal(5))
}

// What a Go module is built from / A file a module does not embed is not one it is built from.
func TestAFileAModuleDoesNotEmbedIsNotOneItIsBuiltFrom(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("embeds")
	workspace.build()

	// What it is changed to, no build before this one was made from.
	second := workspace.write("lab/embeds/notes.txt", "nothing reads this "+time.Now().Format(time.RFC3339Nano)+"\n").build()

	// Nothing is built again; its test is run again alone, given its
	// package's directory as the go command runs it there, where it may
	// read the file.
	spec.Expect(second.outcome()).To(Equal("SUCCESS"))
	spec.Expect(second.cameFrom("//lab/embeds:go.compile")).NotTo(Equal("executed"))
	spec.Expect(second.cameFrom("//lab/embeds:go.test/.")).To(Equal("executed"))
	spec.Expect(second.ranOnAWorker()).To(Equal(1))
}

// What a Go module is built from / A module's tests read their fixtures.
func TestAModuleSTestsReadTheirFixtures(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("fixtures").build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
}

// What a Go module is built from / A fixture is read by the tests alone: changed, only they are run again.
func TestAFixtureIsReadByTheTestsAloneChangedOnlyTheyAreRunAgain(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("fixtures")
	workspace.build()

	second := workspace.write("lab/fixtures/testdata/golden.txt", "not golden\n").build()

	spec.Expect(second.ranOnAWorker()).To(Equal(1))
	spec.Expect(second.outcome()).To(Equal("TESTS_FAILED"))
}

// What a Go module is built from / A module is built with its assembly and the headers that includes.
func TestAModuleIsBuiltWithItsAssemblyAndTheHeadersThatIncludes(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("assembly").build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
}

// What a module requires is worked out from its go.mod. A line of it
// that says a required module comes from somewhere else is not read
// past: the module would be built with something other than its author
// wrote, and nothing would say so.
// What a go.mod says that is not read / A module that replaces what it requires fails the build, at the line that does.
func TestAModuleThatReplacesWhatItRequiresFailsTheBuildAtTheLineThatDoes(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	build := workspace.write("platform/gateway/go.mod", "module example.com/gateway\n\ngo 1.27\n\nrequire example.com/greet v0.0.0\n\nreplace example.com/greet => ../../services/greet").build()

	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
	spec.Expect(build.says()).To(And(ContainSubstring("platform/gateway/go.mod"), ContainSubstring("line 7"), ContainSubstring("replace")))
}

// What a go.mod says that is not read / Nothing is run for a workspace that holds such a module.
func TestNothingIsRunForAWorkspaceThatHoldsSuchAModule(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("gateway")

	build := workspace.write("platform/gateway/go.mod", "module example.com/gateway\n\ngo 1.27\n\nreplace example.com/greet => ../../services/greet").build()

	spec.Expect(build.actions()).To(BeEmpty())
}
