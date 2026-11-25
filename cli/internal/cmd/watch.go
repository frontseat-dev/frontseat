package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/build/daemon/life"
	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

// watching is whether build or test builds again each time something
// changed (--watch).
var watching bool

// looking looks at what a build of a directory is made from
// (machine.Look), and watchEvery is how long a watch waits between two
// looks. A test stands in for each.
var (
	looking    = machine.Look
	watchEvery = 250 * time.Millisecond
)

// watchNamed is how many of the files that changed a watch names.
const watchNamed = 3

// watched builds, and then again each time what a build is made from
// looks other than it did and has settled, until the command is
// interrupted: `frontseat build --watch` and `test --watch`.
//
// Each round is a build as the command without --watch starts one, of
// the daemon there is then: one that was replaced or ended itself since
// the round before is found or started again. What a round builds is
// the engine's to say: it lists and reads the files itself, and a file
// that changed under it fails what read it. The watch says only when
// to ask, and is held to never missing a change after a round began:
// the look a round is held against is taken before the round is asked
// for, with every file in it settled, so whatever is written from then
// on looks otherwise, whenever in the round it is written, and is built
// by the round after. A round under way is not cancelled for a change:
// what it ran is in the cache for the next, and a round cancelled for
// every write would say nothing while someone types.
//
// A round that failed, or that the daemon refused of the directory as
// it is, is a round's result, and waits for the next change. What ends
// the command is an interrupt, and a daemon that cannot be asked.
func watched(cmd *cobra.Command, start func(*frontseat.Workspace) (*frontseat.Build, error)) error {
	ctx := cmd.Context()
	within := inside()
	of, err := directory(within)
	if err != nil {
		return err
	}
	look := func() (*life.Seen, error) { return looking(ctx, of, within) }
	// What cannot be looked at when the command begins ends it: there
	// is nothing to hold a later look against.
	seen, _, err := awaited(ctx, look, nil, false)
	if err != nil {
		return err
	}
	said("watching %s: it is built again each time a file of it changes, until interrupted", of)
	for {
		if err := round(cmd, start); err != nil {
			if !ofTheRound(ctx, err) {
				return err
			}
			_, why := Failed(ctx, err)
			complained("%s", why)
		}
		// After a look that failed there is none to hold the next
		// against: the first that can be taken again is built.
		var changed []string
		seen, changed, err = awaited(ctx, look, seen, seen == nil)
		var unseen *unseen
		switch {
		case errors.As(err, &unseen):
			complained("%v", err)
			said("what is here cannot be looked at: building again, which says what is wrong with it")
		case err != nil:
			return err
		default:
			said("%s: building again", whatChanged(changed))
		}
	}
}

// endingAsked is how many times running a round asks again where the
// daemon it found was ending.
const endingAsked = 3

// round is one round of a watch: a build, as the command without
// --watch starts one, of the daemon there is then. A daemon found as it
// was ending (one that ended itself, having had nothing to do while the
// watch waited) refuses the build by name and has built nothing, so the
// round asks again at once, of the daemon there is by then, which the
// command line starts where there is none: the files are as they were
// looked at, and no change is waited for. Refused so again and again,
// the refusal is the command's.
func round(cmd *cobra.Command, start func(*frontseat.Workspace) (*frontseat.Build, error)) error {
	for asked := 1; ; asked++ {
		err := built(cmd, start)
		if reason, _ := frontseat.Reason(err); reason != frontseat.ReasonDaemonEnding || asked == endingAsked || cmd.Context().Err() != nil {
			return err
		}
		said("the build daemon was ending: asking the one there is now")
	}
}

// ofTheRound is whether a round that could not be followed to its end
// is a round's result, and no failure of the command: the daemon
// refused a build of the directory as it is now (a workspace that is
// being filled), which the next change may put right.
func ofTheRound(ctx context.Context, err error) bool {
	var refused *connect.Error
	return ctx.Err() == nil && errors.As(err, &refused) && refused.Code() == connect.CodeFailedPrecondition
}

// awaited waits until what a build is made from looks other than it
// did at base and has settled, and returns how it looks then, to hold
// the next look against, with the files that look otherwise. It looks
// every while (watchEvery), and never for more than a tenth of the
// time: after a look that took long it waits longer.
//
// Settled is two looks, one after the other, that are the same, every
// file in the second written longer ago than the moment its times are
// told in (life.Seen.Settled): a save, or a checkout, that is still
// writing is waited out, and the look returned is one a write after it
// shows against.
//
// With no base, there is nothing to look other than: it returns the
// first look that has settled, and a look that fails is the command's
// failure, unless it is patient, and waits for one that does not. With
// a base, a look that fails twice running is returned as unseen: the
// build asked for then lists the same files and says what is wrong
// with them, and the watch goes on, patient, from the first look that
// can be taken again.
func awaited(ctx context.Context, look func() (*life.Seen, error), base *life.Seen, patient bool) (*life.Seen, []string, error) {
	var (
		last   *life.Seen
		failed bool
		wait   = watchEvery
	)
	if base == nil {
		wait = 0
	}
	for {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(wait):
		}
		began := time.Now()
		now, err := look()
		wait = max(watchEvery, 9*time.Since(began))
		switch {
		case ctx.Err() != nil:
			return nil, nil, ctx.Err()
		case err != nil && patient:
		case err != nil && base == nil:
			return nil, nil, err
		case err != nil && failed:
			return nil, nil, &unseen{err}
		case err != nil:
			failed, last = true, nil
		case base != nil && len(now.Since(base)) == 0:
			// As it was, or as it was again: nothing to build.
			failed, last = false, nil
		case now.Settled() && (base == nil || last != nil && len(now.Since(last)) == 0):
			if base == nil {
				return now, nil, nil
			}
			return now, now.Since(base), nil
		default:
			failed, last = false, now
		}
	}
}

// unseen is what a build is made from, where it could not be looked
// at: why says what could not.
type unseen struct{ why error }

func (u *unseen) Error() string {
	return "what a build reads here could not be looked at: " + u.why.Error()
}

// whatChanged says which files changed, for the one line a watch says
// before it builds again: the first few by name, and how many there are
// where there are more. A name is a file's, which whoever writes the
// directory chose, and is shown as a line may hold it.
func whatChanged(changed []string) string {
	if len(changed) == 0 {
		return "what is here could not be looked at, and can be now"
	}
	named := make([]string, 0, watchNamed)
	for _, name := range changed[:min(len(changed), watchNamed)] {
		named = append(named, plain.Shown(name))
	}
	if more := len(changed) - len(named); more > 0 {
		return fmt.Sprintf("%s and %d more changed, %d files", strings.Join(named, ", "), more, len(changed))
	}
	return strings.Join(named, ", ") + " changed"
}
