package sandbox_test

import (
	"testing"

	. "github.com/onsi/gomega"
)

// Some systems send agents to do work. Such a system has no machines of
// its own: for each piece of work it asks for a sandbox, puts an agent in
// it, follows what the agent does, and reads what came of it. It speaks
// only the Frontseat API, and so can run wherever frontseat does.
//
// These are the things it relies on.
//
// A sandbox is asked for with what it is for, and found again by that, so
// that whoever asked can lose track of it and still find it. A request
// that is sent twice, because the answer to the first was lost, makes one
// sandbox, not two.
//
// The agent's own program runs in the sandbox, and whoever sent it speaks
// with it over one stream that stays open. What the agent builds can be
// read by whoever sent it.
// Work an agent is sent to do / A sandbox is asked for with what it is for, and found by that.
func TestASandboxIsAskedForWithWhatItIsForAndFoundByThat(t *testing.T) {
	spec := newScenario(t)

	spec.askForASandbox(labelled("drive=42", "session=worker"))

	spec.Expect(spec.sandboxesLabelled("drive=42", "session=worker")).To(HaveLen(1))
}

// Work an agent is sent to do / A sandbox that is for something else is not found.
func TestASandboxThatIsForSomethingElseIsNotFound(t *testing.T) {
	spec := newScenario(t)

	spec.askForASandbox(labelled("drive=43", "session=worker"))

	spec.Expect(spec.sandboxesLabelled("drive=43", "session=critic")).To(BeEmpty())
}

// Work an agent is sent to do / A request sent twice makes one sandbox.
func TestARequestSentTwiceMakesOneSandbox(t *testing.T) {
	spec := newScenario(t)

	request := aRequestName()

	spec.askForASandbox(labelled("drive=44"), sentAs(request))
	spec.askForASandbox(labelled("drive=44"), sentAs(request))

	spec.Expect(spec.sandboxesLabelled("drive=44")).To(HaveLen(1))
}

// Work an agent is sent to do / The agent's program stays on one open stream.
func TestTheAgentSProgramStaysOnOneOpenStream(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	answers := workspace.answersTo("cat", "plan the work", "hand it in")

	spec.Expect(answers).To(Equal([]string{"plan the work", "hand it in"}))
}

// Work an agent is sent to do / Whoever sent the agent reads what the build did.
func TestWhoeverSentTheAgentReadsWhatTheBuildDid(t *testing.T) {
	spec := newScenario(t)

	build := spec.theWorkspace().build()

	// Its module has no tests: a lint, a format and a compile.
	spec.Expect(build.passed()).To(Equal(3))
}
