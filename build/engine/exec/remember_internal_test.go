package exec

import (
	"context"
	"testing"
	"time"
)

// once is an executor that knows every action as the same one, and
// counts what it is asked.
type once struct{ ran int }

func (o *once) Key(context.Context, Action) (string, error) { return "the action", nil }

func (o *once) Execute(context.Context, Action) (Result, error) {
	o.ran++
	return Result{}, nil
}

// A grid says what it holds when it answers, and is free to let it go
// later. What it answered is gone by for as long as Bazel goes by what
// a remote cache answered, and then it is asked again.
func TestAnAnswerIsGoneByForAWhileAndThenAskedForAgain(t *testing.T) {
	grid := &once{}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	kept := &Remembering{Executor: grid, now: func() time.Time { return now }}
	ask := func() {
		t.Helper()
		if _, err := kept.Execute(context.Background(), Action{}); err != nil {
			t.Fatal(err)
		}
	}
	ask()

	now = now.Add(lasts - time.Second)
	ask()
	if grid.ran != 1 {
		t.Fatalf("a second short of how long an answer lasts, the grid was asked %d times; want once", grid.ran)
	}

	now = now.Add(2 * time.Second)
	ask()
	if grid.ran != 2 {
		t.Errorf("a second past how long an answer lasts, the grid was asked %d times; want it asked again", grid.ran)
	}
	// What it answered then lasts as long again.
	now = now.Add(lasts - time.Second)
	ask()
	if grid.ran != 2 {
		t.Errorf("the grid was asked %d times; want the newer answer gone by", grid.ran)
	}
}
