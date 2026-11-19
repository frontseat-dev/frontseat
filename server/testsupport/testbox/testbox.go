// Package testbox gives a test the box image, built from this repository
// as it stands by the box's one build (local/box): the daemon and the
// client for Linux and the plugins, laid on the sandbox image the local
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
	"os"
	"os/exec"
	"testing"

	"github.com/frontseat-dev/frontseat/local"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
)

// Variable names the box image the gate built for the tests it runs.
const Variable = "FRONTSEAT_TEST_BOX"

// Image returns the box image's tag, as the gate built it. A machine
// with no container runtime skips the test; a test the gate did not run
// fails, saying how it is run.
func Image(t testing.TB) string {
	t.Helper()
	testrequired.Stack(t)
	if _, err := exec.Command("docker", "info", "--format", "{{.Architecture}}").Output(); err != nil {
		t.Skipf("no container runtime answers: %v", err)
	}
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
