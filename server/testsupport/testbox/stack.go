package testbox

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/local/stack"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrun"
)

// StackVariable names the stack's image the gate built for the tests it
// runs, on the box it names by Variable.
const StackVariable = "FRONTSEAT_TEST_LOCAL_IMAGE"

// StackImage returns the stack's image the gate built, on the box it
// names by Variable. A machine with no container runtime skips the
// test; a test the gate did not run fails, saying how it is run.
func StackImage(t testing.TB) string {
	t.Helper()
	Image(t)
	image := os.Getenv(StackVariable)
	if image == "" {
		t.Fatalf("%s names no stack image: the gate builds it before the tests it runs (`mise run go:test -- ...`)", StackVariable)
	}
	return image
}

// Stack starts the local stack as one container of the gate's image, as
// the stack starts it (stack.Container), publishing nothing, so that it
// runs beside whatever stack this machine runs; and returns its name,
// which reaches it (docker exec, --network container:<name>). The
// container and its volume are the test's own: named by an id no other
// run draws (testrun.ID), outside the shared stack's names, labelled
// with this run (testrun.Label) for a run that crashed to be found
// by, and removed when the test ends.
func Stack(t testing.TB) string {
	t.Helper()
	image := StackImage(t)
	own := testrun.ID(t, "frontseat-test-stack")
	profile, err := stack.Profile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		// The container, then its volume, which it holds until it is gone.
		for _, removing := range [][]string{{"rm", "--force", own}, {"volume", "rm", own}} {
			if out, err := exec.CommandContext(ctx, "docker", removing...).CombinedOutput(); err != nil {
				t.Errorf("removing the test's stack (%s): %v: %s", removing[0], err, out)
			}
		}
	})
	if out, err := exec.Command("docker", "volume", "create", "--label", testrun.Label+"="+testrun.This(), own).CombinedOutput(); err != nil {
		t.Fatalf("making the test's stack's volume: %v: %s", err, out)
	}
	c := stack.Container{Name: own, Image: image, Volume: own, Labels: map[string]string{testrun.Label: testrun.This()}}
	if out, err := exec.Command("docker", c.Arguments(profile)...).CombinedOutput(); err != nil {
		t.Fatalf("starting the stack's container: %v\n%s", err, out)
	}
	return own
}
