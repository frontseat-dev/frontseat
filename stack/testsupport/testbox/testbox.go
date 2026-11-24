// Package testbox gives a test the box image, built from this repository
// as it stands by the box's one build (stack/box): the supervisor and the
// command line for Linux and the plugins, laid on the sandbox image the local
// grid's workers run.
//
// The image is built by the gate before the tests run (Variable), never
// by a test: the box is built from the source of the server and the
// command line, and a test of either that built it would make it depend
// on itself through the test support, unseen by the catalog. The gate
// names it by its tag of its content, which no other checkout's build
// moves.
package testbox

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/stack/local"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

// Variable names the box image the gate built for the tests it runs.
const Variable = "FRONTSEAT_TEST_BOX"

// answers is whether the container runtime answers, asked once for the
// test process.
var answers = sync.OnceValue(func() error {
	_, err := local.Architecture(context.Background())
	return err
})

// Runtime is said by a test that needs the container runtime the local
// stack runs on, before it asks it anything: it says the test needs the
// stack (testrequired.Stack) and, where the runtime does not answer,
// skips the test, or fails it where the gate runs against the stack
// (testrequired.Missing). It is the test support's one asking of it.
func Runtime(t testing.TB) {
	t.Helper()
	testrequired.Stack(t)
	if err := answers(); err != nil {
		testrequired.Missing(t, "no container runtime answers: %v", err)
	}
}

// GOARCH returns Go's name of the container runtime's architecture, as
// Runtime asks for it first; one the runtime is not known to say fails
// the test.
func GOARCH(t testing.TB) string {
	t.Helper()
	Runtime(t)
	said, err := local.Architecture(context.Background())
	if err != nil {
		t.Fatalf("the container runtime answers and does not say its architecture: %v", err)
	}
	goarch, known := local.GOARCH(said)
	if !known {
		t.Fatalf("unproven probe architecture %q", said)
	}
	return goarch
}

// Image returns the box image's tag, as the gate built it. A machine
// with no container runtime skips the test, as Runtime does; a test the
// gate did not run fails, saying how it is run.
func Image(t testing.TB) string {
	t.Helper()
	Runtime(t)
	tag := os.Getenv(Variable)
	if tag == "" {
		t.Fatalf("%s names no box image: the gate builds it before the tests it runs (`mise run go:test -- ...`)", Variable)
	}
	return tag
}

// Plain returns an image that runs what it is told, with what the box
// holds beside frontseat (bash, curl, python3): the sandbox image the
// box is laid on, which the grid's workers run, at its digest, which has
// no entrypoint of its own. It is on this machine wherever the stack is.
func Plain(t testing.TB) string {
	t.Helper()
	Image(t)
	image, err := local.Embedded.Image()
	if err != nil {
		t.Fatal(err)
	}
	return image
}
