package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// A Go module can hold programs: packages named main, each of which the
// go command makes a file to run of. Compiling a module leaves its
// programs, where the go command installs them: go/bin in the action's
// home, each named after its directory. An action that reads the compile
// finds them there, as it finds anything an action it reads left.
//
// Which packages are programs is the go command's to say. A module with
// none leaves none, and compiles all the same; so does one whose only
// program is for an architecture the worker is not of.
//
// The go command names a program after its directory and writes them all
// to one place. Of two programs in directories of one name it leaves
// one, and so does a compile: what it leaves is what `go install` leaves.
//
// An action that reads the compile of two modules is handed the programs
// of both: what they left in one place is put together. Where one path
// would hold two different things, one would be lost, so the action
// fails and says which two actions left them, and which path.
//
// The specs use entities of the repository lab:
//
//   - programs: a library and the programs hello, bye and runner; its
//     own action run runs runner, which runs the hello the compile left
//     and says which programs are there.
//   - clash: the programs a/tool, b/tool and list; list, its own action,
//     says what programs it was handed.
//   - generator: the programs cmd/gen and list, and a library gen with a
//     file marked to be left out that says it is a program; list, its
//     own action, says what programs it was handed.
//   - forarm, foramd: one program each, for one architecture alone.
//   - more: the program extra. same: a program hello of its own.
//   - gather: its own action list reads the compile of programs and of
//     more, and says what programs it was handed.
//   - collide: the same, of programs and of same; after reads list, and
//     beside reads neither.
//
// A Go module's programs / Compiling a module leaves its programs for the action that reads it.
func TestCompilingAModuleLeavesItsProgramsForTheActionThatReadsIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("programs").build()

	spec.Expect(build.printed("//lab/programs:custom.run")).To(ContainSubstring("hello from a program"))
}

// A Go module's programs / Every program of the module is left, each named after its directory.
func TestEveryProgramOfTheModuleIsLeftEachNamedAfterItsDirectory(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("programs").build()

	spec.Expect(build.printed("//lab/programs:custom.run")).To(ContainSubstring("left: bye hello runner"))
}

// A Go module's programs / An action that reads two modules' compiles is handed the programs of both.
func TestAnActionThatReadsTwoModulesCompilesIsHandedTheProgramsOfBoth(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gather").build(of("//lab/gather:custom.list"))

	spec.Expect(build.printed("//lab/gather:custom.list")).To(ContainSubstring("handed: bye extra hello runner"))
}

// A Go module's programs / Two modules that leave a different program of one name fail the action that reads both, naming them and the program.
func TestTwoModulesThatLeaveADifferentProgramOfOneNameFailTheActionThatReadsBothNamingThemAndTheProgram(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("collide").build(of("//lab/collide:custom.list"))

	spec.Expect(build.outcomeOf("//lab/collide:custom.list")).To(Equal("failed"))
	spec.Expect(build.saysOf("//lab/collide:custom.list")).To(And(ContainSubstring("//lab/programs:go.compile"), ContainSubstring("//lab/same:go.compile"), ContainSubstring("go/bin/hello")))
}

// A Go module's programs / An action that reads what could not be handed to the action it reads is not run.
func TestAnActionThatReadsWhatCouldNotBeHandedToTheActionItReadsIsNotRun(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("collide").build()

	spec.Expect(build.outcomeOf("//lab/collide:custom.after")).To(Equal("skipped"))
}

// A Go module's programs / What does not read both modules goes on.
func TestWhatDoesNotReadBothModulesGoesOn(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("collide").build()

	spec.Expect(build.outcomeOf("//lab/collide:custom.beside")).To(Equal("passed"))
}

// A Go module's programs / A module with no program compiles.
func TestAModuleWithNoProgramCompiles(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("fixtures").build(only("compile"))

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
}

// A Go module's programs / A module with two programs of one name leaves one of them, as the go command does.
func TestAModuleWithTwoProgramsOfOneNameLeavesOneOfThemAsTheGoCommandDoes(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("clash").build(of("//lab/clash:custom.list"))

	spec.Expect(build.printed("//lab/clash:custom.list")).To(ContainSubstring("handed: list tool"))
}

// A Go module's programs / A file the go command leaves out of the build is no program.
func TestAFileTheGoCommandLeavesOutOfTheBuildIsNoProgram(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("generator").build(of("//lab/generator:custom.list"))

	spec.Expect(build.printed("//lab/generator:custom.list")).To(ContainSubstring("handed: gen list"))
}

// A Go module's programs / A module whose one program is for another architecture than the worker's compiles.
func TestAModuleWhoseOneProgramIsForAnotherArchitectureThanTheWorkerSCompiles(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("forarm", "foramd").build(only("compile"))

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
}
