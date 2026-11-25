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

// What a benchmark reports beyond the most its budget gives is held, of
// each unit given, and a unit given that it did not report fails as a
// budget held of nothing. Control: what it reports within the most, and
// a unit it reports that the budget does not give, pass.
func TestWhatABenchmarkReportsIsHeldToTheMostItsBudgetGives(t *testing.T) {
	most := map[string]float64{"kept-bytes/run": 100}
	for name, tc := range map[string]struct {
		reported map[string]float64
		over     int
	}{
		"the control, within the most":        {map[string]float64{"kept-bytes/run": 100, "files": 9}, 0},
		"beyond the most":                     {map[string]float64{"kept-bytes/run": 101}, 1},
		"what the budget holds, not reported": {map[string]float64{"files": 9}, 1},
	} {
		if over := beyond(most, tc.reported); len(over) != tc.over {
			t.Errorf("%s: %v; want %d", name, over, tc.over)
		}
	}
}
