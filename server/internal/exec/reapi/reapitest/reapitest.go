// Package reapitest gives a test a client of the grid tests run their
// actions on, closed when the test ends (Dial), and a grid of the test's
// own on the loopback that answers what it serves and keeps what each
// call carried, in the clear or over TLS (Answering).
package reapitest

import (
	"context"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/exec/reapi"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
)

// Dial is a client of the test grid. A test that asks for it is skipped
// where the grid is not up, as testgrid skips it.
func Dial(t testing.TB) *reapi.Grid {
	t.Helper()
	grid, err := reapi.Dial(context.Background(), reapi.Config{Address: testgrid.Address(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { grid.Close() })
	return grid
}
