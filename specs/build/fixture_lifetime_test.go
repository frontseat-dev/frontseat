package build_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

// Suite-owned fixture data survives the requesting test, while each caller's
// assertions and ordinary cleanup belong to that caller alone. The listener
// satisfies only the prerequisite reachability check; no grid action is asked.
func TestSharedCompanyOutlivesItsFirstTest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv(testgrid.Variable, listener.Addr().String())
	// The grid is this test's own stand-in, so it needs no stack, and runs
	// in the gate that runs with none.
	t.Setenv(testrequired.Variable, "")
	var directory string
	var first *scenario
	cleaned := false
	if !t.Run("first", func(t *testing.T) {
		first = newScenario(t)
		directory = first.company.Dir
		first.Cleanup(func() { cleaned = true })
	}) {
		return
	}
	if !cleaned {
		t.Fatal("first caller cleanup did not run")
	}
	t.Run("after-first-ended", func(t *testing.T) {
		next := newScenario(t)
		if next == first || next.company.scenario != next || next.company.Dir != directory {
			t.Fatal("shared fixture retained the first caller or was rebuilt")
		}
		if _, err := os.Stat(filepath.Join(directory, "services", "hello", "go.mod")); err != nil {
			t.Fatal(err)
		}
	})
}
