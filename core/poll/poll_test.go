package poll_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/core/poll"
)

// What is asked is asked again until it is done, and its error ends the
// asking at once.
func TestWhatIsAskedIsAskedAgainUntilItIsDoneOrFails(t *testing.T) {
	asked := 0
	if err := poll.Until(context.Background(), time.Millisecond, func() (bool, error) {
		asked++
		return asked == 3, nil
	}); err != nil || asked != 3 {
		t.Errorf("asked %d times and ended %v, want 3 times and nil", asked, err)
	}
	failed := errors.New("it failed")
	asked = 0
	if err := poll.Until(context.Background(), time.Millisecond, func() (bool, error) {
		asked++
		return false, failed
	}); !errors.Is(err, failed) || asked != 1 {
		t.Errorf("asked %d times and ended %v, want once and its error", asked, err)
	}
}

// Where the caller gives up while it waits, it ends with the context's
// error and asks no more; a control that is not given up on asks again.
func TestAWaitGivenUpOnEndsWithTheContextsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	asked := 0
	err := poll.Until(ctx, time.Hour, func() (bool, error) {
		asked++
		cancel()
		return false, nil
	})
	if !errors.Is(err, context.Canceled) || asked != 1 {
		t.Errorf("asked %d times and ended %v, want once and context.Canceled", asked, err)
	}
}
