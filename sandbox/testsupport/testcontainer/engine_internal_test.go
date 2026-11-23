package testcontainer

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
	"github.com/frontseat-dev/frontseat/testsupport/testrun"
)

// What the runtime says of a container is the same whether its engine
// is asked or its command is run: the engine is asked because it
// answers fifty times sooner, and for no other reason.
func TestTheEngineSaysOfAContainerWhatTheCommandSays(t *testing.T) {
	testrequired.Stack(t)
	if engine() == "" {
		testrequired.Missing(t, "the runtime on this machine has no engine to ask beside its command")
	}
	name := testrun.ID(t, "frontseat-engine-test")
	if out, err := exec.Command("docker", "run", "--detach", "--name", name, "--label", sandboxLabel+"=test", "--label", testrun.Label+"="+testrun.This(), "--publish", "127.0.0.1::7600", "busybox:1.37", "sleep", "300").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", name).Run() })
	ctx := context.Background()

	asked, err := askedOf(ctx, engine(), name)
	if err != nil {
		t.Fatal(err)
	}
	commanded, err := commandedFor(ctx, name)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(asked, commanded) {
		t.Errorf("the engine says %+v and the command %+v; want the same", asked, commanded)
	}
	if published := asked.NetworkSettings.Ports["7600/tcp"]; len(published) == 0 || strings.TrimSpace(published[0].HostPort) == "" {
		t.Errorf("the engine says the port is published at %v; want where it is", published)
	}
	// Reaching a port of a sandbox is done for every request to it, and
	// runs no command.
	before := commands.Load()
	for range 5 {
		conn, err := (Driver{}).Dial(ctx, name, 7600)
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	if ran := commands.Load() - before; ran != 0 {
		t.Errorf("reaching a port five times ran %d docker commands; want none", ran)
	}
	// A container there is none of is none to either.
	for how, ask := range map[string]func() error{
		"the engine":  func() error { _, err := askedOf(ctx, engine(), "frontseat-no-such-container"); return err },
		"the command": func() error { _, err := commandedFor(ctx, "frontseat-no-such-container"); return err },
	} {
		if err := ask(); !errors.Is(err, driver.ErrNotFound) {
			t.Errorf("%s says of a container there is none of: %v; want that it is not found", how, err)
		}
	}
}
