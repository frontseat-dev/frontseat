// Package testgrid is the grid tests run their actions on: the Buildbarn
// of the local stack. A test that needs it is skipped where it
// is not up.
package testgrid

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/local"
	"github.com/frontseat-dev/frontseat/local/ports"
	"github.com/frontseat-dev/frontseat/server/internal/confine"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
)

// Address returns the grid's address, and skips the test where no grid
// answers there. It is where the local stack serves the Remote
// Execution API, or FRONTSEAT_TEST_GRID where that is set. Where it is
// the local stack's, the workers are first given this checkout's
// confiner, frontseat-action (Confiner): a daemon this checkout builds starts every
// action by it, and a grid that another checkout's stack runs has only
// what that checkout installed. A grid FRONTSEAT_TEST_GRID names is given
// nothing: it must hold this checkout's form already, or its actions are
// not started (reapi says so).
func Address(t testing.TB) string {
	t.Helper()
	testrequired.Stack(t)
	address := net.JoinHostPort("localhost", strconv.Itoa(ports.Grid))
	given := os.Getenv("FRONTSEAT_TEST_GRID")
	if given != "" {
		address = given
	}
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Skipf("no grid at %s; start it with: mise start", address)
	}
	conn.Close()
	if given == "" {
		Confiner(t)
	}
	return address
}

// runtimeAnswers is whether the container runtime answers, asked once
// for the test process.
var runtimeAnswers = sync.OnceValue(func() error {
	return exec.Command("docker", "info", "--format", "{{.Architecture}}").Run()
})

// ConfinerVariable names where the gate installed this checkout's
// confiner, frontseat-action, for the local stack's workers, before the
// tests it runs: the path they run it by.
const ConfinerVariable = "FRONTSEAT_TEST_CONFINER"

// Confiner returns the path the local stack's workers run this
// checkout's confiner by. The gate installs it before the tests run
// (box.InstallConfiner, beside the forms there, and a confiner of its
// form already there, of whatever build, is left as it is), never a
// test: it is built from the source of the server, and a test of the
// server that built it would depend on itself unseen. A test the gate did not
// run fails, saying how it is run.
func Confiner(t testing.TB) string {
	t.Helper()
	testrequired.Stack(t)
	if err := runtimeAnswers(); err != nil {
		t.Skipf("no container runtime answers: %v", err)
	}
	at := os.Getenv(ConfinerVariable)
	if at == "" {
		t.Fatalf("%s names no confiner: the gate installs this checkout's frontseat-action before the tests it runs (`mise run go:test -- ...`)", ConfinerVariable)
	}
	return at
}

// Confined is what a command another tool sends the grid begins with, as
// frontseat's daemon begins each action's: this checkout's confiner, its
// spec and the way up to the action's root, its input root, where an
// action of Bazel's runs (confine.Arguments). The grid's runner runs no
// other command.
func Confined(t testing.TB) []string {
	t.Helper()
	confiner := Confiner(t)
	spec, err := confine.Encode(confine.Spec{Home: ".frontseat/home", Temporary: ".frontseat/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	return []string{confiner, spec, "."}
}

// InSandbox returns what a daemon in a sandbox on this machine is told
// of the grid: its address as a container reaches it, its asset service
// and its workers' platform. It skips the test where no grid answers.
func InSandbox(t testing.TB) local.Grid {
	t.Helper()
	_, port, err := net.SplitHostPort(Address(t))
	if err != nil {
		t.Fatal(err)
	}
	grid, err := files(t).Grid(net.JoinHostPort("host.docker.internal", port))
	if err != nil {
		t.Fatal(err)
	}
	return grid
}

// Assets returns the asset service's address, and skips the test where
// none answers there: where the local stack serves the Remote
// Asset API, or FRONTSEAT_TEST_ASSETS where that is set.
func Assets(t testing.TB) string {
	t.Helper()
	testrequired.Stack(t)
	address := net.JoinHostPort("localhost", strconv.Itoa(ports.Assets))
	if given := os.Getenv("FRONTSEAT_TEST_ASSETS"); given != "" {
		address = given
	}
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Skipf("no asset service at %s; start the stack with: mise start", address)
	}
	conn.Close()
	return address
}

// GoProxy is where the go command fetches modules from on the grid's
// workers: its own default, since the worker tells an action of no proxy
// and an action that fetches reaches the origin itself.
const GoProxy = "https://proxy.golang.org"

// Platform is what an action asks of the grid's workers: the properties
// the stack's worker advertises.
func Platform() map[string]string {
	platform, err := local.Embedded.Platform()
	if err != nil {
		panic(err)
	}
	return platform
}

// files are the local stack's files, as package local holds them: built
// into it, and never looked for (local's own test holds them to the
// repository's).
func files(t testing.TB) local.Files {
	t.Helper()
	return local.Embedded
}
