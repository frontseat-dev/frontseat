package testcontainer

import (
	"context"
	"errors"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrun"
)

// A runtime that does not answer is not waited on, the engine no more
// than the docker command: a request to an engine that takes the
// connection and says nothing is bounded, so that a context with no
// deadline (the server's recover, a delete) is not waited on for ever.
//
// The control is an engine that is not there: the command is run, and
// says at once that there is no such container.
func TestAnEngineThatDoesNotAnswerIsNotWaitedOnForEver(t *testing.T) {
	testrequired.Stack(t)
	dir, err := os.MkdirTemp("", "fs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	real := engine
	t.Cleanup(func() { engine = real })

	t.Run("control: no engine at the socket", func(t *testing.T) {
		engine = func() string { return filepath.Join(dir, "none.sock") }
		before := commands.Load()
		_, err := inspect(context.Background(), "frontseat-no-such-container")
		if !errors.Is(err, driver.ErrNotFound) || commands.Load() == before {
			t.Errorf("with no engine to ask: %v, %d commands run; want the command asked, and no such container", err, commands.Load()-before)
		}
	})

	t.Run("an engine that takes the connection and says nothing", func(t *testing.T) {
		socket := filepath.Join(dir, "mute.sock")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
			}
		}()
		engine = func() string { return socket }
		// The context has no deadline, as the server's own has not; it is
		// cancelled only so that the test can end.
		ctx, cancel := context.WithCancel(context.Background())
		answered := make(chan error, 1)
		start := time.Now()
		go func() {
			_, err := inspect(ctx, "frontseat-no-such-container")
			answered <- err
		}()
		select {
		case err := <-answered:
			t.Logf("answered after %s: %v", time.Since(start).Round(time.Second), err)
		case <-time.After(engineWithin + 30*time.Second):
			t.Errorf("nothing after %s, long past how long an engine is given to answer (%s)", time.Since(start).Round(time.Second), engineWithin)
			cancel()
			<-answered
		}
		cancel()
	})
}

// What the driver reaches is a sandbox: a container that carries no
// sandbox label is not found, by Observe and by Dial alike, and one
// that is gone is not found either.
func TestWhatIsReachedIsASandbox(t *testing.T) {
	testrequired.Stack(t)
	if engine() == "" {
		testrequired.Missing(t, "the container runtime has no engine on this machine to ask")
	}
	name := testrun.ID(t, "frontseat-not-a-sandbox")
	docker := func(args ...string) {
		t.Helper()
		if out, err := osexec.Command("docker", args...).CombinedOutput(); err != nil {
			t.Fatalf("docker %v: %v: %s", args, err, out)
		}
	}
	docker("run", "--detach", "--name", name, "--label", testrun.Label+"="+testrun.This(), "--publish", "127.0.0.1::7600", "busybox:1.37", "sleep", "300")
	t.Cleanup(func() { _ = osexec.Command("docker", "rm", "--force", name).Run() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d := Driver{}

	if _, err := d.Observe(ctx, name); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("observing a container that is no sandbox: %v; want not found", err)
	}
	if conn, err := d.Dial(ctx, name, 7600); err == nil {
		conn.Close()
		t.Errorf("the driver reached the port of %s, a container that carries no sandbox label and that Observe says is not found", name)
	}
	docker("rm", "--force", name)
	if _, err := d.Dial(ctx, name, 7600); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("reaching a container that is gone: %v; want not found", err)
	}
}
