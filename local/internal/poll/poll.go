// Package poll asks again, at an interval, until what it asks holds: the
// one way the stack's program and the box's build wait for the stack's
// container to come to something.
package poll

import (
	"context"
	"time"
)

// Tries asks holds once, then again after each interval, up to tries
// more times, and says whether it held. It returns ctx's error where ctx
// ends while it waits.
func Tries(ctx context.Context, tries int, every time.Duration, holds func() bool) (bool, error) {
	for tried := 0; !holds(); tried++ {
		if tried >= tries {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(every):
		}
	}
	return true, nil
}
