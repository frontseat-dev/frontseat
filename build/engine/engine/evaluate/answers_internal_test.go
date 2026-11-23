package evaluate

import (
	"testing"
	"time"
)

// An answer nothing asks for any more is let go, so that a daemon keeps
// no more than its evaluations use; one that is asked for is kept,
// however many evaluations there are.
func TestAnAnswerNothingAsksForIsLetGo(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	answers := &Answers{now: func() time.Time { return now }}
	answers.kept = map[string]*answer{"asked for": {last: now}, "not asked for": {last: now}}
	answers.repositories = map[string]*saying{"asked for": {last: now}, "not asked for": {last: now}}

	// Many evaluations in a short while let go of nothing.
	for range 50 {
		answers.begin()
	}
	if len(answers.kept) != 2 || len(answers.repositories) != 2 {
		t.Fatalf("fifty evaluations in no time left %d answers and %d repositories; want both of each", len(answers.kept), len(answers.repositories))
	}

	for range 4 {
		now = now.Add(time.Hour)
		answers.begin()
		answers.kept["asked for"].last = now
		answers.repositories["asked for"].last = now
	}

	for name, kept := range map[string]bool{"asked for": true, "not asked for": false} {
		if _, there := answers.kept[name]; there != kept {
			t.Errorf("the answer %s: kept %v after four hours, want %v", name, there, kept)
		}
		if _, there := answers.repositories[name]; there != kept {
			t.Errorf("what the repository %s said: kept %v after four hours, want %v", name, there, kept)
		}
	}
}
