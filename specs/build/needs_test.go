package build_test

import (
	"testing"

	. "github.com/onsi/gomega"
)

// Some actions read what another action left: a test reads what was
// compiled, a package reads the binary. An action says so in its inputs,
// the one list of what it reads, as a Bazel rule names a generated file
// in its srcs. Beside the patterns of its source, an input may be:
//
//   - one output of an action of the same entity, after a colon,
//     exactly as that action declares it: :made/ where write declares
//     made/;
//   - an action of the same entity, after a colon, as plugin.name, for
//     all it leaves, at home or in the repository: :go.compile;
//   - either, of another entity, by a label: the entity's directory
//     from the repository's root, a colon and the rest, //chain:made/,
//     //programs:go.compile, whatever the workspace holds the
//     repository beneath (it prints them //lab/chain:made/).
//
// A pattern never names what an action left: a pattern is of source.
// Nobody says what an action waits for. An action needs what it reads,
// and runs when what it reads has been left; actions that read nothing of
// each other run at the same time. What is named is one thing: a name
// that is both an action and an output, or neither, an output that more
// than one action leaves, or an action that leaves nothing, is refused.
//
// What an action reads is handed to it as files, at the place the other
// left them, and it sees nothing else an action left: of what an action
// leaves, a path hands that alone. What is handed on is known by its
// content: an action whose inputs were left the same as before is the
// same action, and is answered from what the grid kept.
//
// When something an action reads fails, the action is not run, and says
// which action stopped it. Whatever does not read the failure still runs.
//
// Asking for an action brings what it reads. A label that names nothing
// in the workspace, one that names both an action and what an action
// leaves, or actions that read each other in a circle, is a mistake that
// fails the build before anything runs, and is named.
//
// The specs use entities of the repository lab. Each has a program write,
// which writes made/word.txt, and a program read, which prints that file
// and fails where it is not there:
//
//   - chain: read reads :made/; report reads all of :go.compile.
//   - apart: read does not read made/.
//   - globbed: read reads made/*, a pattern.
//   - doubly: read reads :custom.write and :made/, which it leaves.
//   - blocked: read reads :made/, and write fails.
//   - hollow: write says it writes made/, and writes nothing.
//   - borrower: read reads //chain:made/, of chain, which it
//     depends on.
//   - unclaimed: read reads //chain:spare/, which nothing leaves.
//   - twofold: write leaves custom.write, which read names as
//     :custom.write.
//   - rivals: write and copy both leave made/, which read names.
//   - idle: read reads :go.lint, which leaves nothing.
//   - shadow: the repository holds made/word.txt, and read reads both
//     made/** and :made/.
//   - picky: both leaves made/ and spare/; made reads :made/ and spare
//     reads :spare/, and each prints spare/word.txt.
//   - stray: read reads an action of an entity nobody has.
//   - circle: first reads second, and second reads first.
//
// Actions that read what other actions left / An action sees what an action left at a path it reads.
func TestAnActionSeesWhatAnActionLeftAtAPathItReads(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("chain").build()

	spec.Expect(build.outcomeOf("//lab/chain:custom.read")).To(Equal("passed"))
}

// Actions that read what other actions left / An action does not see what an action left that it does not read.
func TestAnActionDoesNotSeeWhatAnActionLeftThatItDoesNotRead(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("apart").build()

	spec.Expect(build.outcomeOf("//lab/apart:custom.read")).To(Equal("failed"))
}

// Actions that read what other actions left / A pattern never names what an action left.
func TestAPatternNeverNamesWhatAnActionLeft(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("globbed").build(of("//lab/globbed:custom.read"))

	spec.Expect(build.actions()).To(Equal([]string{"//lab/globbed:custom.read"}))
	spec.Expect(build.outcomeOf("//lab/globbed:custom.read")).To(Equal("failed"))
}

// Actions that read what other actions left / An action that reads one path of what an action left is handed that path alone.
func TestAnActionThatReadsOnePathOfWhatAnActionLeftIsHandedThatPathAlone(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("picky").build()

	spec.Expect(build.outcomeOf("//lab/picky:custom.spare")).To(Equal("passed"))
	spec.Expect(build.outcomeOf("//lab/picky:custom.made")).To(Equal("failed"))
}

// Actions that read what other actions left / An action can read all an action of a plugin left.
func TestAnActionCanReadAllAnActionOfAPluginLeft(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("chain").build(of("//lab/chain:custom.report"))

	spec.Expect(build.actions()).To(Equal([]string{"//lab/chain:custom.report", "//lab/chain:go.compile"}))
}

// Actions that read what other actions left / An action can read what an action of another entity left.
func TestAnActionCanReadWhatAnActionOfAnotherEntityLeft(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("borrower").build()

	spec.Expect(build.outcomeOf("//lab/borrower:custom.read")).To(Equal("passed"))
}

// Actions that read what other actions left / Asking for an action brings what it reads.
func TestAskingForAnActionBringsWhatItReads(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("chain").build(of("//lab/chain:custom.read"))

	spec.Expect(build.actions()).To(Equal([]string{"//lab/chain:custom.read", "//lab/chain:custom.write"}))
}

// Actions that read what other actions left / Keeping one kind of action keeps what it reads.
func TestKeepingOneKindOfActionKeepsWhatItReads(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("chain").build(only("read"))

	spec.Expect(build.actions()).To(Equal([]string{"//lab/chain:custom.read", "//lab/chain:custom.write"}))
}

// Actions that read what other actions left / When what an action reads fails, the action is not run.
func TestWhenWhatAnActionReadsFailsTheActionIsNotRun(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("blocked").build()

	spec.Expect(build.outcomeOf("//lab/blocked:custom.read")).To(Equal("skipped"))
}

// Actions that read what other actions left / An action that was not run says which action stopped it.
func TestAnActionThatWasNotRunSaysWhichActionStoppedIt(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("blocked").build()

	spec.Expect(build.saysOf("//lab/blocked:custom.read")).To(ContainSubstring("//lab/blocked:custom.write failed"))
}

// Actions that read what other actions left / What does not read a failure still runs.
func TestWhatDoesNotReadAFailureStillRuns(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("blocked").build()

	spec.Expect(build.outcomeOf("//lab/blocked:go.compile")).To(Equal("passed"))
}

// Actions that read what other actions left / A build with an action that was not run has failed.
func TestABuildWithAnActionThatWasNotRunHasFailed(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("blocked").build()

	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}

// Actions that read what other actions left / An action that does not leave what it says it writes fails.
func TestAnActionThatDoesNotLeaveWhatItSaysItWritesFails(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("hollow").build()

	spec.Expect(build.saysOf("//lab/hollow:custom.write")).To(ContainSubstring("it left no made/"))
}

// Actions that read what other actions left / An action whose inputs were left the same as before is the same action.
func TestAnActionWhoseInputsWereLeftTheSameAsBeforeIsTheSameAction(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.workspaceOf("chain")
	before := workspace.build()

	workspace.write("lab/chain/write/note.txt", "the program is as it was")
	after := workspace.build()

	spec.Expect(after.action("//lab/chain:custom.write")).NotTo(Equal(before.action("//lab/chain:custom.write")))
	spec.Expect(after.action("//lab/chain:custom.read")).To(Equal(before.action("//lab/chain:custom.read")))
}

// Actions that read what other actions left / What is named twice is read once.
func TestWhatIsNamedTwiceIsReadOnce(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("doubly").build(of("//lab/doubly:custom.read"))

	spec.Expect(build.needsOf("//lab/doubly:custom.read")).To(Equal([]string{"//lab/doubly:custom.write"}))
	spec.Expect(build.outcomeOf("//lab/doubly:custom.read")).To(Equal("passed"))
}

// Actions that read what other actions left / Reading an action of an entity that is not in the workspace is refused by name.
func TestReadingAnActionOfAnEntityThatIsNotInTheWorkspaceIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("stray").build()

	spec.Expect(build.says()).To(ContainSubstring("//lab/stray:custom.read reads //nowhere:custom.write, which names no action and nothing an action leaves"))
}

// Actions that read what other actions left / Reading what no action of the workspace leaves is refused by name.
func TestReadingWhatNoActionOfTheWorkspaceLeavesIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("unclaimed").build()

	spec.Expect(build.says()).To(ContainSubstring("//lab/unclaimed:custom.read reads //chain:spare/, which names no action and nothing an action leaves"))
	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}

// Actions that read what other actions left / Reading an action that leaves nothing is refused by name.
func TestReadingAnActionThatLeavesNothingIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("idle").build()

	spec.Expect(build.says()).To(ContainSubstring("//lab/idle:custom.read reads :go.lint, which leaves nothing"))
	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}

// Actions that read what other actions left / What is handed where a file of the repository is read is refused by name.
func TestWhatIsHandedWhereAFileOfTheRepositoryIsReadIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("shadow").build()

	spec.Expect(build.saysOf("//lab/shadow:custom.read")).To(ContainSubstring("//lab/shadow:custom.read is handed lab/shadow/made, which is also a file of the repository it reads"))
	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}

// Actions that read what other actions left / A name that is both an action and what an action leaves is refused by name.
func TestANameThatIsBothAnActionAndWhatAnActionLeavesIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("twofold").build()

	spec.Expect(build.says()).To(ContainSubstring("//lab/twofold:custom.read reads :custom.write, which names both an action and what an action leaves"))
	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}

// Actions that read what other actions left / An output that more than one action leaves is refused, naming them.
func TestAnOutputThatMoreThanOneActionLeavesIsRefusedNamingThem(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("rivals").build()

	spec.Expect(build.says()).To(ContainSubstring("//lab/rivals:custom.read reads :made/, which more than one action leaves: //lab/rivals:custom.write and //lab/rivals:custom.copy"))
	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}

// Actions that read what other actions left / Actions that read each other are refused, naming the circle.
func TestActionsThatReadEachOtherAreRefusedNamingTheCircle(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("circle").build()

	spec.Expect(build.says()).To(ContainSubstring("//lab/circle:custom.first reads what //lab/circle:custom.second leaves, which reads what //lab/circle:custom.first leaves"))
}

// Actions that read what other actions left / A mistake in what actions read fails the build.
func TestAMistakeInWhatActionsReadFailsTheBuild(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("circle").build()

	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
}
