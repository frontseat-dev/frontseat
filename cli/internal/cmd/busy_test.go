package cmd

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
)

// A command that builds waits for the daemon of its directory under its
// own context: interrupted, it ends then, and is said to have been
// interrupted. One refused a daemon that has a build in hand is refused,
// by the code of a refusal, with what the refusal says; the control is a
// daemon that failed otherwise, which is no refusal.
func TestACommandWaitingForItsDaemonEndsWhenInterruptedAndIsRefusedABusyOne(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	found, stood := builder, directory
	t.Cleanup(func() { builder, directory = found, stood })
	directory = func(bool) (string, error) { return t.TempDir(), nil }

	builder = func(ctx context.Context, _ string, _ bool) (machine.Builder, error) {
		select {
		case <-ctx.Done():
			return machine.Builder{}, ctx.Err()
		case <-time.After(10 * time.Second):
			return machine.Builder{}, errors.New("the daemon was waited for after the command was interrupted")
		}
	}
	ctx, interrupt := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, interrupt)
	began := time.Now()
	if _, code, err := runAt(t, ctx, "", "build", "list"); code != ExitInterrupted || time.Since(began) > 5*time.Second {
		t.Errorf("interrupted while it waited for its daemon, a build ended %d (%v) after %s; want %d, at once", code, err, time.Since(began).Round(time.Millisecond), ExitInterrupted)
	}

	builder = func(context.Context, string, bool) (machine.Builder, error) {
		return machine.Builder{}, fmt.Errorf("%w: it differs in the program", machine.ErrBusy)
	}
	if _, code, err := runAt(t, context.Background(), "", "build", "list"); code != ExitRefused || !errors.Is(err, machine.ErrBusy) {
		t.Errorf("a daemon with a build in hand: ended %d (%v); want %d, and the refusal", code, err, ExitRefused)
	}
	builder = func(context.Context, string, bool) (machine.Builder, error) {
		return machine.Builder{}, errors.New("the daemon could not be started")
	}
	if _, code, _ := runAt(t, context.Background(), "", "build", "list"); code == ExitRefused {
		t.Errorf("the control: a daemon that failed otherwise ended %d, as a refusal", code)
	}
}
