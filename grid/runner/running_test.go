package runner

import (
	"context"
	"testing"
	"time"

	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
)

// neverRuns is a memory driver whose sandboxes, once made, never come
// to run.
type neverRuns struct{ *drivertest.Memory }

func (d neverRuns) Observe(ctx context.Context, id string) (driver.Sandbox, error) {
	now, err := d.Memory.Observe(ctx, id)
	now.State = driver.Starting
	return now, err
}

// asked asks for a sandbox as an attempt of a runner does, and returns
// its id.
func asked(t *testing.T, r *runner) string {
	t.Helper()
	id, err := runtimeAttempt(t, r).create(context.Background(), &sandboxv1.SandboxSpec{Image: "box", Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_ALL}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A sandbox whose making takes longer than the runner waits for one to
// run, as one whose image is pulled cold does, is run: the runner waits
// for its making as long as the action waits, and only once it is made
// for it to run. The control is a sandbox that is made and never runs,
// which the runner waits for no longer than it waits for one to run.
func TestASandboxMadeLongerThanTheRunnerWaitsForItToRunIsRun(t *testing.T) {
	const starts = 200 * time.Millisecond
	d := drivertest.NewMemory()
	r, _ := sandboxesOver(t, d)
	r.starts = starts
	let := d.Hold()
	defer let(nil)
	id := asked(t, r)
	time.AfterFunc(3*starts, func() { let(nil) })
	action, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.running(action, id); err != nil {
		t.Errorf("a sandbox made longer than the runner waits for it to run: %v, want it run", err)
	}

	control, _ := sandboxesOver(t, neverRuns{drivertest.NewMemory()})
	control.starts = starts
	began := time.Now()
	if err := control.running(action, asked(t, control)); err == nil {
		t.Error("the control: a sandbox that never runs is said to run")
	}
	if waited := time.Since(began); waited < starts || waited > starts+5*time.Second {
		t.Errorf("the control: a sandbox that never runs was waited for %s, want %s", waited, starts)
	}
}

// A sandbox that is being made is waited for no longer than the action
// waits: an action that stops waiting stops waiting for its making.
func TestASandboxBeingMadeIsWaitedForAsLongAsTheActionWaits(t *testing.T) {
	d := drivertest.NewMemory()
	r, _ := sandboxesOver(t, d)
	let := d.Hold()
	defer let(nil)
	id := asked(t, r)
	action, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	if err := r.running(action, id); err == nil {
		t.Error("a sandbox never made is said to run")
	}
	if waited := time.Since(began); waited > 5*time.Second {
		t.Errorf("an action that waited 200ms waited %s for a sandbox's making", waited)
	}
}
