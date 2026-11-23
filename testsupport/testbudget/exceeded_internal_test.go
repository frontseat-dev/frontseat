package testbudget

import (
	"testing"
	"time"
)

// Time is held only where the gate's budget command says so; allocations
// are held everywhere. The control: a slow operation fails where time is
// held.
func TestTimeIsHeldOnlyWhereTheBudgetCommandSaysSo(t *testing.T) {
	budget := Budget{Time: time.Millisecond, Allocs: 10}
	if over := exceeded(budget, time.Second, 5, false); len(over) != 0 {
		t.Fatalf("a slow operation failed where time is not held: %v", over)
	}
	if over := exceeded(budget, time.Second, 5, true); len(over) != 1 {
		t.Fatalf("a slow operation passed where time is held: %v", over)
	}
	if over := exceeded(budget, 0, 11, false); len(over) != 1 {
		t.Fatalf("too many allocations passed where time is not held: %v", over)
	}
}
