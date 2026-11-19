package container

// Written by an independent review of fe67226..f74db45.

import (
	"context"
	"errors"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrun"
)

// DEFECT. "A runtime that takes longer [than three minutes] is not
// waited on: it is reported" holds for the docker command and not for
// the engine, which is now what is asked first: a request to an engine
// that takes the connection and does not answer has no bound of its
// own. inspect's comment says the command is run "where the engine ...
// does not answer"; it is run only where the engine refuses. With a
// context that has no deadline (the server's recover, a delete) the
// driver waits for ever.
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

// What the driver says of a container that is no sandbox, and of one
// paused, stopped and gone. (Exploration: Dial is the one call of the
// driver that does not ask whether the container is a sandbox. It was
// so before these commits, and the control plane asks first.)
func TestWhatIsReachedIsASandbox(t *testing.T) {
	testrequired.Stack(t)
	if engine() == "" {
		t.Skip("no engine")
	}
	name := testrun.ID(t, "frontseat-review-not-a-sandbox")
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
	docker("pause", name)
	conn, err := d.Dial(ctx, name, 7600)
	t.Logf("paused: Dial says %v", err)
	if err == nil {
		conn.Close()
	}
	docker("unpause", name)
	docker("stop", "--time", "0", name)
	_, err = d.Dial(ctx, name, 7600)
	t.Logf("stopped: Dial says %v", err)
	docker("rm", "--force", name)
	_, err = d.Dial(ctx, name, 7600)
	t.Logf("gone: Dial says %v", err)
	if !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("reaching a container that is gone: %v; want not found", err)
	}
}
