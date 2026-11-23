// Package poll asks again, at an interval, until what is asked is done:
// the server's one loop of asking, waiting and asking again, for what
// it can only ask after (a sandbox, a workspace, a socket, a grid). What
// to ask, when to give up and what to say then are each caller's.
package poll

import (
	"context"
	"time"
)

// Until calls try now and then again after each interval, until it
// says it is done or fails, and returns its error. Where ctx ends while
// it waits, it returns ctx's error (ctx.Err()), and calls try no more.
func Until(ctx context.Context, every time.Duration, try func() (done bool, err error)) error {
	for {
		done, err := try()
		if done || err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
}
