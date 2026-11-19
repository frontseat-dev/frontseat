// Package testrequired says what a test needs that it is not given by its
// own code: the local stack, and prerequisites that make a missing one
// fail an acceptance test. Unit tests may use optional infrastructure
// helpers directly; a required acceptance scenario must not pass because
// such a helper skipped it.
//
// A gate that runs with no stack (`mise run check`) says so in
// FRONTSEAT_TEST_STACK=none: then every test that needs the stack, by a
// helper of the test support or by Stack, is skipped by Stack, required or
// not, and the gate starts no container and asks no grid, whatever this
// machine runs. Elsewhere a test that needs the stack uses whatever
// answers. The gate program sets the variable for every test it runs, so
// one exported by a person changes nothing.
package testrequired

import (
	"os"
	"testing"
)

// Variable names the gate's word on the stack, and NoStack its value
// where the gate runs with none.
const (
	Variable = "FRONTSEAT_TEST_STACK"
	NoStack  = "none"
)

// Stackless reports whether the gate running the test runs with no stack.
func Stackless() bool { return os.Getenv(Variable) == NoStack }

// Stack is said by a test that needs the local stack, or the container
// runtime it runs on, before it asks either anything: it skips the test
// where the gate runs with none. It is the one skip a required test
// takes there: TB lets it through, and no other, where Stack is given
// what TB returned (of a TB too). A value that holds one within it, as
// a struct that embeds it, is not looked into: Stack's skip is then a
// skip of the TB within, which fails the test, and says so.
func Stack(t testing.TB) {
	t.Helper()
	if !Stackless() {
		return
	}
	reason := "needs the local stack, and this gate runs with none (" + Variable + "=" + NoStack + "): mise run test runs it"
	if required, ok := t.(test); ok {
		required.skipForStack(reason)
	}
	t.Skip(reason)
}

// TB preserves the test's cleanup and failure behavior, but treats a
// helper's request to skip as an unavailable required prerequisite. Where
// the gate runs with no stack, Stack skips it before anything is asked;
// any other skip is still a failure.
func TB(t testing.TB) testing.TB { return test{TB: t} }

type test struct{ testing.TB }

// skipForStack skips the test for Stack, through every TB beneath this
// one, and through nothing else.
func (t test) skipForStack(reason string) {
	t.Helper()
	if inner, ok := t.TB.(test); ok {
		inner.skipForStack(reason)
	}
	t.TB.Skip(reason)
}

func (t test) Skip(args ...any) {
	t.Helper()
	t.Fatal(append([]any{"required test prerequisite unavailable:"}, args...)...)
}

func (t test) Skipf(format string, args ...any) {
	t.Helper()
	t.Fatalf("required test prerequisite unavailable: "+format, args...)
}

func (t test) SkipNow() {
	t.Helper()
	t.Fatal("required test prerequisite unavailable")
}
