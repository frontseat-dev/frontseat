// Package testbudget holds code to what it may cost. A budget is the
// most one operation may take and the most it may allocate; a test that
// holds one fails when a change makes the operation dearer than that.
//
// Allocations do not depend on the machine, so their budget is close to
// what was measured and catches a small regression. Time does, so its
// budget is several times what was measured and catches only what got
// many times slower; to compare two versions closely, run the same
// benchmarks on both and give the results to benchstat.
//
// A budget is the owner's to raise: a change that goes over one removes
// the cost, or asks, with the measurement and the reason.
package testbudget

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"testing"
	"time"
)

// Variable says a budget's time is held, and Timed is what it is set to:
// the gate's budget command sets it, on a machine doing nothing else
// (`gate budget`). Anywhere else, a test beside others on a grid worker
// above all, time is reported and not held, and allocations, which do
// not depend on the machine, are.
const (
	Variable = "FRONTSEAT_TEST_BUDGET"
	Timed    = "timed"
)

// Budget is what one operation may cost.
type Budget struct {
	// Time is the most it may take.
	Time time.Duration
	// Allocs is the most allocations it may make.
	Allocs int64
	// Most is the most each of what the benchmark reports of one
	// operation may be, by the unit it reports it in (b.ReportMetric):
	// what it keeps of the heap, in bytes, among them. Like allocations
	// it is held wherever the test runs, and so only what does not
	// depend on the machine is given here; what the benchmark reports
	// and is not given is not held.
	Most map[string]float64
}

// Hold runs a benchmark and fails the test where one operation costs
// more than the budget. The race detector makes everything slower and
// allocate more, so under it nothing is held.
func Hold(t *testing.T, budget Budget, benchmark func(b *testing.B)) {
	t.Helper()
	if raceDetector {
		t.Skip("a budget is held without the race detector")
	}
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		benchmark(b)
	})
	if result.N == 0 {
		t.Fatal("the benchmark did not run")
	}
	took := time.Duration(result.NsPerOp())
	t.Logf("%s and %d allocations an operation, of %s and %d allowed", took, result.AllocsPerOp(), budget.Time, budget.Allocs)
	for _, over := range exceeded(budget, took, result.AllocsPerOp(), os.Getenv(Variable) == Timed) {
		t.Error(over)
	}
	for _, over := range beyond(budget.Most, result.Extra) {
		t.Error(over)
	}
}

// beyond says how what a benchmark reported of one operation goes
// beyond the most it may be, of each unit given; one given that the
// benchmark did not report is beyond it, as a budget held of nothing.
func beyond(most, reported map[string]float64) []string {
	var over []string
	for _, unit := range slices.Sorted(maps.Keys(most)) {
		got, ok := reported[unit]
		switch {
		case !ok:
			over = append(over, fmt.Sprintf("the benchmark reported no %s, and its budget holds it", unit))
		case got > most[unit]:
			over = append(over, fmt.Sprintf("an operation reported %v %s, and its budget is %v", got, unit, most[unit]))
		}
	}
	return over
}

// exceeded says how an operation that took so long and made so many
// allocations exceeds its budget: its time only where time is held.
func exceeded(budget Budget, took time.Duration, allocs int64, timed bool) []string {
	var over []string
	if timed && took > budget.Time {
		over = append(over, fmt.Sprintf("an operation took %s, and its budget is %s", took, budget.Time))
	}
	if allocs > budget.Allocs {
		over = append(over, fmt.Sprintf("an operation made %d allocations, and its budget is %d", allocs, budget.Allocs))
	}
	return over
}
