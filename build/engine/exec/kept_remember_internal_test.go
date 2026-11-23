package exec

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// each is an executor that knows an action by its label.
type each struct{}

func (each) Key(_ context.Context, action Action) (string, error) { return action.Label, nil }
func (each) Execute(context.Context, Action) (Result, error) {
	return Result{Stdout: make([]byte, 1<<10)}, nil
}

// an answer that is no longer gone by, being older than three
// hours, is of no use to anyone: it is let go, and not held until eight
// more runs have come.
func TestAnAnswerTooOldToGoByIsLetGo(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	kept := &Remembering{Executor: each{}, now: func() time.Time { return now }}
	kept.Begin()
	for i := range 100 {
		if _, err := kept.Execute(context.Background(), Action{Label: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	// The control: while they are gone by, they are kept.
	if len(kept.answers) != 100 {
		t.Fatalf("%d answers kept; want 100", len(kept.answers))
	}

	now = now.Add(24 * time.Hour)
	kept.Begin()

	if len(kept.answers) != 0 {
		t.Errorf("a day later, as a run begins, %d answers are still held, none of which will be gone by again", len(kept.answers))
	}
}
