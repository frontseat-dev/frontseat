package build_test

import (
	. "github.com/onsi/gomega"
	"testing"
)

// A build runs code that somebody wrote: yours, a dependency's, an
// agent's. Each action of a build is therefore confined on the worker
// that runs it, by what the kernel enforces, and not by trusting the
// command.
//
// An action sees what it was given and nothing beside it, and cannot
// change it: a worker gives every action the same files, so one that an
// action changed would be changed for the actions after it. It cannot
// open a connection: not to the internet, and not to anything near the
// worker.
// The one kind of action that may is a fetch, which is how tools and
// dependencies come in, and what it brings is handed on as files.
//
// This costs a build nothing it should have. A tool comes with the
// action, so there is nothing on the worker to look for.
//
// The specs use three entities of the repository lab whose tests pass
// only if they are stopped: caller, whose test fails if it can open a
// connection, snoop, whose test fails if it can reach the runner of its
// sandbox or write its slot, and vandal, whose test fails if it can write
// to a file it was given.
// What a build is kept from / An action cannot open a connection.
func TestAnActionCannotOpenAConnection(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("caller").build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
	// By its test, which a worker ran for this build: a result kept
	// from another would say what a worker did then.
	spec.Expect(build.outcomeOf("//lab/caller:go.test/.")).To(Equal("passed"))
	spec.Expect(build.cameFrom("//lab/caller:go.test/.")).To(Equal("executed"))
}

// What a build is kept from / An action cannot reach the runner of its sandbox, nor write its slot.
func TestAnActionCannotReachItsRunnerNorWriteItsSlot(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("snoop").build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
	spec.Expect(build.outcomeOf("//lab/snoop:go.test/.")).To(Equal("passed"))
	spec.Expect(build.cameFrom("//lab/snoop:go.test/.")).To(Equal("executed"))
}

// What a build is kept from / An action cannot change what it was given.
func TestAnActionCannotChangeWhatItWasGiven(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("vandal").build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
	spec.Expect(build.outcomeOf("//lab/vandal:go.test/.")).To(Equal("passed"))
	spec.Expect(build.cameFrom("//lab/vandal:go.test/.")).To(Equal("executed"))
}

// What a build is kept from / An action still runs with its tools.
func TestAnActionStillRunsWithItsTools(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	build := spec.workspaceOf("gateway").build()

	spec.Expect(build.passed()).To(Equal(6))
}
