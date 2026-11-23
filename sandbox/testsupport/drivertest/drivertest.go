// Package drivertest is what a driver is tested with, this repository's
// drivers and any other: the conformance every driver is held to (a
// sandbox's lifecycle, applying twice, the transport to a port, and a
// refusal for each capability the driver does not offer: Conforms), a
// sandbox for a test to work with (one the driver made for this run,
// running, and ended when the test ends: Running), and a driver whose
// sandboxes are held in memory, which the sandbox API and the control
// plane are tested over (Memory).
package drivertest

import (
	"context"
	"errors"
	"testing"

	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/testsupport/testrun"
)

// Labelled is a sandbox with the label of this run (testrun.Label) beside its
// own.
func Labelled(record driver.Sandbox) driver.Sandbox {
	labels := map[string]string{}
	for k, v := range record.Labels {
		labels[k] = v
	}
	labels[testrun.Label] = testrun.This()
	record.Labels = labels
	return record
}

// Running has a driver make a sandbox, waits for it to run, and ends it
// when the test ends. The id is the test's own (ID): one the driver holds
// already is another run's, and is refused, never made over or ended.
// What is ended is the sandbox this call asked for, so it is ended
// whether or not the answer to that came back. Each of before is called
// as the test ends, before the sandbox is: what it has to say of the
// test is said while there is a sandbox to say it of.
func Running(t testing.TB, d driver.Driver, record driver.Sandbox, before ...func()) {
	t.Helper()
	ctx := context.Background()
	if held, err := d.Observe(ctx, record.ID); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("the id %s is held already (%v, %v): it is not this test's to make or end", record.ID, held.State, err)
	}
	record = Labelled(record)
	t.Cleanup(func() {
		for _, each := range before {
			each()
		}
		_ = d.Delete(ctx, record.ID)
	})
	if _, err := d.Apply(ctx, record); err != nil {
		t.Fatal(err)
	}
	Await(t, d, record.ID, driver.Running)
}
