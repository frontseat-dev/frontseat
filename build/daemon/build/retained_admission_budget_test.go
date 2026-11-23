package build

import (
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
)

// A burst of eight ListProjects requests through the daemon's graph API,
// over a workspace whose evaluation is kept, is held to 300 ms and 195000
// allocations. It is a budget of the path every warm graph request takes,
// not a limit on the heap or on how many requests wait. The budget command
// of mise run check includes this package.
func TestBudgetRetainedConcurrentGraphRequests(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 300 * time.Millisecond, Allocs: 195000}, func(b *testing.B) {
		benchmarkRetainedGraphAPI(b, "ListProjects", 8)
	})
}
