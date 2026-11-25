package sandbox_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testbuild"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"

	. "github.com/onsi/gomega"
)

// What one workspace has built, nobody builds again. Two people each
// have a workspace of their own on one commit: each a sandbox with its
// own clone and its own build daemon, kept by a control plane of its
// own, with its own owner, and both building on the one grid. The first
// tests and builds, and a worker runs what no grid had kept. The second
// then asks the same of the same commit, and no worker runs anything:
// every action is the action the first sent, by its digest, and is
// answered from what the grid kept.
//
// The first is the control of the second: there a worker runs each
// action of the library and of the program that reads it, which say a
// word no run before said. The second control is a change: the second
// workspace changes what the library says, and a worker then runs the
// actions of the library and of the program that reads it, each named
// here, and no other; the module that reads neither and the npm package
// are still answered from what the grid kept.
//
// It asks each build daemon what `frontseat test` and `frontseat build`
// ask it (CreateBuild, with tests alone and then without), and runs
// neither command. Not proven: that two people's keys change nothing,
// since a spec has one key to open a shell with; and that a result is
// still kept after the grid's storage has turned over.
func TestWhatOneWorkspaceBuiltAnotherOnTheSameCommitDoesNotBuildAgain(t *testing.T) {
	spec := newScenario(t)

	word := fmt.Sprintf("word-%d", time.Now().UnixNano())
	checkout := spec.aRepository()
	testforge.Greeting(spec.TB, checkout, word)
	clone := spec.cloning(checkout)

	first := spec.inASandboxOfItsOwn(clone)
	tested, built := first.build(testsAlone).passing(), first.build().passing()
	// The control: these are of a word no run said, and a worker ran each.
	spec.Expect(append(tested.results().Executed(), built.results().Executed()...)).To(ContainElements(inCode(testforge.GreetingActions.Changed)),
		"the first workspace's actions of what no run had built: a worker runs each")

	// Another person's: another control plane, with another owner, makes
	// another sandbox of another name, whose build daemon is its own.
	elsewhere := clone
	elsewhere.Server = spec.anotherPlane()
	second := spec.inASandboxOfItsOwn(elsewhere)
	spec.Expect(second.Server).NotTo(Equal(first.Server))
	spec.Expect(second.Workspace).NotTo(Equal(first.Workspace))
	spec.Expect(second.Sandbox).NotTo(Equal(first.Sandbox))
	spec.Expect(second.commit()).To(Equal(first.commit()), "the two workspaces are of one commit")

	again, rebuilt := second.build(testsAlone).passing(), second.build().passing()
	spec.Expect(again.actions()).To(Equal(tested.actions()), "the second workspace's tests are the actions the first's were")
	spec.Expect(rebuilt.actions()).To(Equal(built.actions()), "the second workspace's build is the actions the first's was")
	spec.Expect(again.results().Rebuilt(tested.results(), nil)).To(BeEmpty(), "frontseat test in a second workspace on the commit the first tested")
	spec.Expect(rebuilt.results().Rebuilt(built.results(), nil)).To(BeEmpty(), "frontseat build in a second workspace on the commit the first built")
	spec.Expect(again.run.GetCost().GetActionsExecuted()+rebuilt.run.GetCost().GetActionsExecuted()).To(BeZero(), "what the second workspace's builds say a worker ran")

	// The control: one source file changed, and a worker runs what reads
	// it and nothing else.
	second.copyIn(code+"/greet/greet.go", testforge.Greets(word+"-changed"))
	changed := second.build().passing()
	spec.Expect(changed.results().Executed()).To(Equal(inCode(testforge.GreetingActions.Changed)),
		"after greet/greet.go changed, a worker runs the actions of greet and of hello, which reads it, and no other; of %v, which read neither, it runs none", inCode(testforge.GreetingActions.Unchanged))
	spec.Expect(changed.actions()).To(Equal(built.actions()), "the build after the change is of the same actions")
}

// testsAlone asks for the actions that run tests and what they need:
// what `frontseat test` asks for.
func testsAlone(r *buildv1.CreateBuildRequest) { r.Tests = true }

// inCode are actions as a workspace that cloned a repository as code
// prints them, in order, given as the repository alone prints them.
func inCode(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		out = append(out, "//code/"+strings.TrimPrefix(label, "//"))
	}
	slices.Sort(out)
	return out
}

// commit is the commit the workspace's one repository stands at, as its
// control plane says.
func (w *Workspace) commit() string {
	w.scenario.Helper()
	described, err := w.sdk().Describe(context.Background())
	w.scenario.Expect(err).NotTo(HaveOccurred())
	held := described.GetStatus().GetRepositories()
	w.scenario.Expect(held).To(HaveLen(1))
	w.scenario.Expect(held[0].GetCommit()).NotTo(BeEmpty())
	return held[0].GetCommit()
}

// passing holds that the build succeeded, saying what each action that
// did not pass said, and returns it.
func (b *Build) passing() *Build {
	b.Helper()
	var said []string
	for label, result := range b.results() {
		if result.GetOutcome() != buildv1.Outcome_OUTCOME_PASSED {
			said = append(said, fmt.Sprintf("%s %s: %s", label, result.GetOutcome(), result.GetMessage()))
		}
	}
	slices.Sort(said)
	b.Expect(b.outcome()).To(Equal("SUCCESS"), "%s\n%s", b.run.GetFailure(), strings.Join(said, "\n"))
	return b
}

// results are what the build says of its actions, as its workspace's
// build daemon says them.
func (b *Build) results() testbuild.Results {
	b.Helper()
	results, err := testbuild.ResultsOf(context.Background(), b.of.here().BuildByID(b.run.GetId()))
	b.Expect(err).NotTo(HaveOccurred())
	return results
}

// actions are the actions of the build, as they are printed, in order.
func (b *Build) actions() []string {
	b.Helper()
	var labels []string
	for label := range b.results() {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	return labels
}
