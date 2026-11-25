package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
)

// watching is whether build or test builds again each time something
// changed (--watch).
var watching bool

// askedAgain is how long a watch waits before it asks again for a round
// the daemon refused of the directory as it was, having built nothing
// to wait on. A test stands in for it.
var askedAgain = time.Second

// watchNamed is how many of the files that changed a watch names.
const watchNamed = 3

// watched builds, and then again each time what the build before read
// looks other than that build held it to, until the command is
// interrupted: `frontseat build --watch` and `test --watch`.
//
// Each round is a build as the command without --watch starts one, of
// the daemon there is then: one that was replaced or ended itself since
// the round before is found or started again. What a round builds is
// the engine's to say: it lists and reads the files itself, and a file
// that changed under it fails what read it. When to ask is the daemon's
// to say too: after a round, the watch asks the daemon that built it to
// wait until anything that build listed or read, by its listing or by
// name, looks otherwise than the build held it to, and has settled
// (WaitSourceChanged). The command line looks at no file itself. A
// write made while a round is under way is after what the round held
// its files to, and so is answered as soon as the round has ended: it
// is built by the round after. A round under way is not cancelled for
// a change: what it ran is in the cache for the next, and a round
// cancelled for every write would say nothing while someone types.
//
// A round that failed is a round's result, and waits for the next
// change. A round the daemon refused of the directory as it was (a
// workspace being filled) built nothing to wait on, and is asked again
// a while after (askedAgain). A wait the daemon could not answer (it
// was replaced by another command, or no longer keeps what the build
// read) is answered by building again, of the daemon there is then;
// where the wait after that cannot be answered either, the command
// ends with why. What ends the command is an interrupt, and a daemon
// that cannot be asked.
func watched(cmd *cobra.Command, start func(*frontseat.Workspace) (*frontseat.Build, error)) error {
	ctx := cmd.Context()
	of, err := directory(inside())
	if err != nil {
		return err
	}
	said("watching %s: it is built again each time a file of it changes, until interrupted", of)
	unanswered := false
	for {
		build, err := round(cmd, start)
		if err != nil {
			if !ofTheRound(ctx, err) {
				return err
			}
			_, why := Failed(ctx, err)
			complained("%s", why)
		}
		if build == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(askedAgain):
			}
			continue
		}
		changed, err := build.WaitSourceChanged(ctx)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil && unanswered:
			return err
		case err != nil:
			unanswered = true
			_, why := Failed(ctx, err)
			said("the build daemon did not wait for a change (%s): building again, with the daemon there is now", why)
		default:
			unanswered = false
			said("%s: building again", whatChanged(changed.GetPaths()))
		}
	}
}

// endingAsked is how many times running a round asks again where the
// daemon it found was ending.
const endingAsked = 3

// round is one round of a watch: a build, as the command without
// --watch starts one, of the daemon there is then, and the build it
// made, if it made one. A daemon found as it was ending (one that ended
// itself, having had nothing to do) refuses the build by name and has
// built nothing, so the round asks again at once, of the daemon there
// is by then, which the command line starts where there is none: no
// change is waited for. Refused so again and again, the refusal is the
// command's.
func round(cmd *cobra.Command, start func(*frontseat.Workspace) (*frontseat.Build, error)) (*frontseat.Build, error) {
	for asked := 1; ; asked++ {
		build, err := made(cmd, start)
		if reason, _ := frontseat.Reason(err); reason != frontseat.ReasonDaemonEnding || asked == endingAsked || cmd.Context().Err() != nil {
			return build, err
		}
		said("the build daemon was ending: asking the one there is now")
	}
}

// ofTheRound is whether a round that could not be followed to its end
// is a round's result, and no failure of the command: the daemon
// refused a build of the directory as it is now (a workspace that is
// being filled), which may be put right.
func ofTheRound(ctx context.Context, err error) bool {
	var refused *connect.Error
	return ctx.Err() == nil && errors.As(err, &refused) && refused.Code() == connect.CodeFailedPrecondition
}

// whatChanged says which files changed, for the one line a watch says
// before it builds again: the first few by name, and how many there are
// where there are more. A name is a file's, which whoever writes the
// directory chose, and is shown as a line may hold it.
func whatChanged(changed []string) string {
	named := make([]string, 0, watchNamed)
	for _, name := range changed[:min(len(changed), watchNamed)] {
		named = append(named, plain.Shown(name))
	}
	if more := len(changed) - len(named); more > 0 {
		return fmt.Sprintf("%s and %d more changed, %d files", strings.Join(named, ", "), more, len(changed))
	}
	return strings.Join(named, ", ") + " changed"
}
