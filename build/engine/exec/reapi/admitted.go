package reapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/client"
	"google.golang.org/grpc/status"

	"github.com/frontseat-dev/frontseat/build/engine/exec/access"
)

// NotAdmitted is whether a grid answered that it does not admit whoever
// asks (access.NotAdmitted). What a worker was refused while it ran an
// action, which comes in the grid's answer for the action, is not that:
// the daemon was admitted.
func NotAdmitted(err error) bool {
	var where *ran
	return access.NotAdmitted(err) && !errors.As(err, &where) && !Unread(err)
}

// unread is the grid's answer, when it was dialled, that it serves the
// daemon no instance of a name it was told to read (grid.reads): said by
// the entry of grid.reads it is, and the code the grid answered, never
// the name nor the grid's words, which repeat it.
type unread struct {
	entry int
	said  error
}

func (u *unread) Error() string {
	return fmt.Sprintf("it serves the daemon no instance of the name its settings read at grid.reads' entry %d, answering %s: the name is none the grid serves", u.entry, status.Code(u.said))
}
func (u *unread) Unwrap() error { return u.said }

// Unread is whether a grid, when it was dialled, answered that it does
// not serve the daemon an instance it was told to read: the settings name
// an instance that is not the grid's, and asking again would be answered
// the same. The grid admitted the daemon.
func Unread(err error) bool {
	var is *unread
	return errors.As(err, &is)
}

// ran is the status of the grid's answer for an action, as apart from the
// status of the call that asked for it: what happened where the action
// ran, a worker refused by storage among it.
type ran struct{ said error }

func (r *ran) Error() string { return r.said.Error() }
func (r *ran) Unwrap() error { return r.said }

// Settled is whether asking a grid again would be answered as it was: it
// did not admit the daemon, could not be asked over TLS as the daemon was
// told to, or was not asked at all for a token that could not be read. A
// grid that is not there may yet be, and is not. A grid that serves the
// daemon no instance it reads has answered too (Unread).
func Settled(err error) bool {
	return NotAdmitted(err) || access.Untrusted(err) || access.Unsent(err) || Unread(err)
}

// said is what a call to the grid answered, as the daemon tells it: that
// nothing was sent, where its token could not be read (access.Told), and
// that the grid did not admit it, where that is what the grid answered.
// ctx is the call's, watched (access.Watch).
func (g *Grid) said(ctx context.Context, err error) error {
	err = access.Told(ctx, err)
	if !NotAdmitted(err) {
		return err
	}
	return g.access.Refused("the grid", err)
}

// unanswered is the error of a grid that did not say what it serves. One
// that did not admit the daemon is said to have refused it, and one that
// a daemon told to ask over TLS could not ask so is said so, apart from
// one that is not there; the grid's own status is kept in each. That TLS
// was refused is what the dial's own handshakes say (access.Handshakes),
// never what a grid's words say: a grid that answers chooses them.
func unanswered(ctx context.Context, config Config, shaken *access.Handshakes, err error) error {
	// The client wraps what it was answered in what it dialled with, and
	// hides the status in doing so.
	var made *client.InitError
	if errors.As(err, &made) {
		err = made.Err
	}
	err = access.Told(ctx, err)
	if said := shaken.Said("the grid", config.Address, err); access.Untrusted(said) {
		return fmt.Errorf("reapi: %w", said)
	}
	return fmt.Errorf("reapi: the grid at %s: %w", config.Address, config.Access.Refused("the grid", err))
}
