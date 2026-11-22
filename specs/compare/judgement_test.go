package compare_test

// How a case is judged, written with what a run would give.

import (
	"testing"
	"time"
)

// rounds of a case where frontseat took so many times as long as Bazel,
// each with a little of its own noise.
func roundsOf(times ...float64) []sample {
	out := make([]sample, len(times))
	for i, each := range times {
		out[i] = sample{frontseat: built{took: time.Duration(each * float64(time.Second))}, bazel: built{took: time.Second}}
	}
	return out
}

// A case is over its allowance only where it is clearly so: every round
// over it is over, and so are rounds over it but for one quick one, which
// a busy moment of the other tool's can give; rounds about it, some under
// and some over, are not; nor are rounds under it, nor quick ones but for
// one slow one. The same rounds are always judged alike.
func TestACaseIsOverItsAllowanceOnlyWhereItIsClearlySo(t *testing.T) {
	for name, c := range map[string]struct {
		times []float64
		over  bool
	}{
		"every round over it":             {[]float64{1.5, 1.45, 1.55, 1.5, 1.6, 1.4, 1.52, 1.48, 1.51, 1.49}, true},
		"one quick round among slow ones": {[]float64{1.5, 1.45, 1.55, 0.9, 1.6, 1.4, 1.52, 1.48, 1.51, 1.49}, true},
		"rounds about it":                 {[]float64{0.95, 1.05, 1.0, 0.98, 1.03, 1.01, 0.97, 1.04, 0.99, 1.02}, false},
		"the control, every round under":  {[]float64{0.5, 0.52, 0.48, 0.5, 0.51, 0.49, 0.5, 0.53, 0.47, 0.5}, false},
		"one slow round among quick ones": {[]float64{0.5, 0.52, 0.48, 3.0, 0.51, 0.49, 0.5, 0.53, 0.47, 0.5}, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := ratios(roundsOf(c.times...))
			if got := over(r, 1.0); got != c.over {
				low, high := interval(r)
				t.Errorf("judged over: %v, want %v (median %.2f, interval %.2f to %.2f)", got, c.over, median(r), low, high)
			}
			low, high := interval(r)
			again, more := interval(r)
			if low != again || high != more {
				t.Error("the same rounds gave another interval")
			}
			if !(low <= median(r) && median(r) <= high) {
				t.Errorf("the interval %.2f to %.2f does not hold the median %.2f", low, high, median(r))
			}
		})
	}
}

// A case is judged in no fewer rounds than one quick round cannot decide:
// with fewer, rounds clearly over their allowance but for one are judged
// within it; with the fewest allowed, they are judged over.
func TestNoCaseIsJudgedInRoundsOneQuickRoundDecides(t *testing.T) {
	quickAmongSlow := func(n int) []float64 {
		times := make([]float64, n)
		for i := range times {
			times[i] = 1.5
		}
		times[n/2] = 0.9
		return ratios(roundsOf(times...))
	}
	for n := 4; n < minimumRounds; n += 2 {
		if over(quickAmongSlow(n), 1.2) {
			t.Errorf("in %d rounds one quick round did not decide, so %d is not the fewest rounds allowed", n, minimumRounds)
		}
	}
	if r := quickAmongSlow(minimumRounds); !over(r, 1.2) {
		low, high := interval(r)
		t.Errorf("in %d rounds, the fewest allowed, one quick round still decides: interval %.2f to %.2f", minimumRounds, low, high)
	}
}

// What Bazel had the grid run is what it counts as remote, however many:
// not what the grid answered from what it kept, nor its own. What it
// says that cannot be read so is not taken for a count.
func TestWhatBazelRanIsWhatItHadTheGridRun(t *testing.T) {
	for printed, want := range map[string]string{
		"INFO: 151 processes: 1 internal, 150 remote.\n":                 "150",
		"INFO: 3,001 processes: 1 internal, 3,000 remote.\n":             "3000",
		"INFO: 7 processes: 6 remote cache hit, 1 internal.\n":           "0",
		"INFO: 9 processes: 3 remote cache hit, 1 internal, 5 remote.\n": "5",
		"INFO: 1 process: 1 internal.\n":                                 "0",
		"INFO: Build completed successfully, 1 total action\n":           "?",
		"INFO: 4 processes: 1 internal, four remote.\n":                  "?",
	} {
		if got := remotely(printed); got != want {
			t.Errorf("%q counted %s, want %s", printed, got, want)
		}
	}
}

// Every case built has an allowance, and nothing is allowed that is not
// built.
func TestEveryCaseHasItsAllowance(t *testing.T) {
	for _, name := range cases {
		if allowances[name] < 1 {
			t.Errorf("%s has no allowance", name)
		}
	}
	if len(allowances) != len(cases) {
		t.Errorf("%d allowances for %d cases", len(allowances), len(cases))
	}
}
