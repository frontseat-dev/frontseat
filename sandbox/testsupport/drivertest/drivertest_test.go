package drivertest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
	"github.com/frontseat-dev/frontseat/testsupport/testrun"
)

// recorder is a test that Running is handed: what it was told to clean
// up, and the failure that ended it.
type recorder struct {
	testing.TB
	cleanups []func()
	failed   string
}

type stopped struct{}

func (r *recorder) Helper()           {}
func (r *recorder) Cleanup(f func())  { r.cleanups = append(r.cleanups, f) }
func (r *recorder) Fatal(args ...any) { r.failed = fmt.Sprint(args...); panic(stopped{}) }
func (r *recorder) Fatalf(format string, args ...any) {
	r.failed = fmt.Sprintf(format, args...)
	panic(stopped{})
}

// run has Running make the record for a recorder, as a test would, and
// then ends that test as it ends.
func run(t *testing.T, d driver.Driver, record driver.Sandbox) *recorder {
	t.Helper()
	r := &recorder{TB: t}
	func() {
		defer func() {
			if v := recover(); v != nil {
				if _, ok := v.(stopped); !ok {
					panic(v)
				}
			}
		}()
		drivertest.Running(r, d, record)
	}()
	for i := len(r.cleanups) - 1; i >= 0; i-- {
		r.cleanups[i]()
	}
	return r
}

// A sandbox another run holds under an id is not made over, nor ended,
// by a test given that id; the control is a test given an id of its own,
// whose sandbox is made and then ended.
func TestRunningEndsNoSandboxItDidNotMake(t *testing.T) {
	ctx := context.Background()
	d := drivertest.NewMemory()
	another := driver.Sandbox{ID: "taken", Image: "none", Labels: map[string]string{"owner": "another"}}
	if _, err := d.Apply(ctx, another); err != nil {
		t.Fatal(err)
	}
	if r := run(t, d, driver.Sandbox{ID: "taken", Image: "none", Labels: map[string]string{"owner": "this"}}); r.failed == "" {
		t.Error("a test was given an id another run holds and went on")
	}
	if got, err := d.Observe(ctx, "taken"); err != nil || got.Labels["owner"] != "another" {
		t.Errorf("the other run's sandbox is %+v, %v; want it as it was", got, err)
	}

	own := driver.Sandbox{ID: testrun.ID(t, "mine"), Image: "none"}
	if r := run(t, d, own); r.failed != "" {
		t.Fatalf("a test of an id of its own failed: %s", r.failed)
	}
	if d.Holds(own.ID) {
		t.Error("the sandbox the test made outlived it")
	}
}

// Two runs never draw one id, and an id keeps its name.
func TestAnIDIsTheRunsOwn(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := testrun.ID(t, "frontseat-openshell-push")
		if seen[id] || !strings.HasPrefix(id, "frontseat-openshell-push-") || len(id) != len("frontseat-openshell-push-")+8 {
			t.Fatalf("drew %q", id)
		}
		seen[id] = true
	}
}
