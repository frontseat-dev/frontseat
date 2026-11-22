package poll_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/local/internal/poll"
)

// What is asked is asked once and then again as many more times as it
// is told, and says whether it held; one that holds is asked no more.
func TestWhatIsAskedIsAskedOnceAndThenAgainAsToldTimes(t *testing.T) {
	asked := 0
	held, err := poll.Tries(context.Background(), 3, time.Millisecond, func() bool { asked++; return false })
	if held || err != nil || asked != 4 {
		t.Errorf("asked %d times, held %v, ended %v; want 4 times, not held, nil", asked, held, err)
	}
	asked = 0
	held, err = poll.Tries(context.Background(), 3, time.Millisecond, func() bool { asked++; return asked == 2 })
	if !held || err != nil || asked != 2 {
		t.Errorf("asked %d times, held %v, ended %v; want twice, held, nil", asked, held, err)
	}
}

// Where the caller gives up while it waits, it ends with the context's
// error.
func TestAWaitGivenUpOnEndsWithTheContextsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	held, err := poll.Tries(ctx, 3, time.Hour, func() bool { cancel(); return false })
	if held || !errors.Is(err, context.Canceled) {
		t.Errorf("held %v, ended %v; want not held, context.Canceled", held, err)
	}
}
