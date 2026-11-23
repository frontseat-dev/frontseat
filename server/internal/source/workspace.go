// Package source is the source a daemon holds: the repositories in its
// directory, what they were made from, and whether they can be built.
// It is filled from repositories and entities, as the control plane
// tells it through the daemon's source API (Serve), and keeps no spec of
// its own.
package source

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/manifest"
	"github.com/frontseat-dev/frontseat/server/internal/record"
	"github.com/frontseat-dev/frontseat/server/internal/source/internal/populate"
	"github.com/frontseat-dev/frontseat/server/internal/vcs"
	"github.com/frontseat-dev/frontseat/server/internal/vcs/chosen"
)

// State is whether a workspace can be built.
type State int

const (
	// Empty is a workspace that holds nothing yet.
	Empty State = iota
	// Populating is a workspace that is being filled.
	Populating
	// Ready is a workspace that holds source that can be built.
	Ready
	// Failed is a workspace that could not be filled, and says why.
	Failed
)

// Workspace is what a daemon holds.
type Workspace struct {
	root string
	// system is the version control system the repositories are
	// brought with; none for a mounted checkout, which brings none.
	system vcs.System
	// mounted says the directory is a checkout the sandbox was given, one
	// repository with no name, which the daemon neither clones nor
	// records.
	mounted bool
	// within is how long one repository may take to be brought.
	within time.Duration
	// bringing ends when the workspace is closed, by stop: every pass
	// that brings repositories runs under it, and passes joins them.
	bringing context.Context
	stop     context.CancelFunc
	passes   sync.WaitGroup

	mu sync.Mutex
	// closed says the workspace was closed: no pass starts after it.
	closed   bool
	manifest manifest.Manifest
	state    State
	message  string
	// generation is the generation of the source it was last told to
	// hold; zero for one told none since it was opened.
	generation int64
	// changed is closed, and replaced, each time the state changes.
	changed chan struct{}
}

// broughtWithin is how long one repository may take to be brought,
// whole: the clone or the widening from its start to its end, however
// it is getting on. A version control system that fetches over a
// network waits for its remote without a limit of its own, and one that
// accepts a connection and answers nothing would leave the workspace
// being populated for ever, which refuses every later source. A
// repository that takes longer, though it is still arriving, is not
// brought.
const broughtWithin = 30 * time.Minute

// Open returns the workspace in a directory: a mounted checkout, or what
// the directory's manifest records, or nothing yet. Its repositories are
// brought with a version control system, which a mounted checkout, that
// brings none, does without. Close it when the daemon stops.
func Open(root string, mounted bool, system vcs.System) (*Workspace, error) {
	w := &Workspace{root: root, mounted: mounted, system: system, within: broughtWithin, changed: make(chan struct{})}
	w.bringing, w.stop = context.WithCancel(context.Background())
	if mounted {
		w.state = Ready
		return w, nil
	}
	if system == nil {
		return nil, errors.New("the workspace was given no version control system to bring its repositories with")
	}
	recorded, found, err := manifest.Read(root, system.Revision)
	if err != nil {
		return nil, fmt.Errorf("the workspace's manifest: %w", err)
	}
	// Nothing is brought while a workspace is opened: what a daemon that
	// ended left unfinished is cleared, so that it is brought anew.
	if err := manifest.Settle(root); err != nil {
		return nil, fmt.Errorf("the workspace's manifest: %w", err)
	}
	if err := populate.Settle(root, recorded.Repositories); err != nil {
		return nil, err
	}
	if found && len(recorded.Repositories) > 0 {
		w.manifest, w.state = recorded, Ready
	}
	return w, nil
}

// Close stops what is bringing the workspace's repositories and returns
// once it has stopped: a repository half brought is left out, and the
// workspace says it failed and why. Nothing is brought after it.
func (w *Workspace) Close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.stop()
	w.passes.Wait()
}

// NameOf returns the name a repository is given in the workspace when
// whoever asks for it gives none: empty where its remote names none, or
// the workspace brings no repository.
func (w *Workspace) NameOf(remote string) string {
	if w.system == nil {
		return ""
	}
	return w.system.NameOf(remote)
}

// Root is the workspace's directory.
func (w *Workspace) Root() string { return w.root }

// Mounted is whether the workspace is a checkout the sandbox was given,
// into which nothing is cloned.
func (w *Workspace) Mounted() bool { return w.mounted }

// Holding is a workspace as it stands.
type Holding struct {
	// State is whether it can be built, and Message why it cannot, where
	// filling it failed.
	State   State
	Message string
	// Generation is the generation it was last told to hold; zero for
	// none since it was opened.
	Generation int64
	// Repositories are what it holds.
	Repositories []record.Repository
}

// Now returns the workspace as it stands, and a channel that is closed
// when that changes.
func (w *Workspace) Now() (Holding, <-chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return Holding{State: w.state, Message: w.message, Generation: w.generation, Repositories: w.manifest.Repositories}, w.changed
}

// Entities returns the entities the workspace knows, each with where it
// lives.
func (w *Workspace) Entities() []record.Located {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]record.Located, 0, len(w.manifest.Known))
	for _, known := range w.manifest.Known {
		out = append(out, known.Located())
	}
	return out
}

// set moves the workspace to a state and wakes whoever waits. The caller
// holds the lock.
func (w *Workspace) set(state State, message string) {
	w.state, w.message = state, message
	close(w.changed)
	w.changed = make(chan struct{})
}

// Populate clones what was asked for, widens the partial checkouts the
// workspace already holds, and records it, with where each entity
// located is. It returns once that is under way; Now tells whoever waits
// how it ends. Whether it may start and its starting are one step, so
// that of two asked at once, one is refused; the generation is the one
// it holds once it starts. Each repository is brought within a bound,
// and none once the workspace is closed: either ends the pass as a
// failure that says so. A source told once the workspace is closed is
// held at its generation and failed, so that whoever reads it back reads
// that generation refused because the daemon stopped.
func (w *Workspace) Populate(generation int64, checkouts []record.Checkout, located []record.Located) error {
	if w.mounted {
		return errMounted
	}
	// Everything asked is held to the workspace before any of it starts:
	// one repository it cannot hold, and none is cloned.
	for _, c := range checkouts {
		if err := c.Within(); err != nil {
			return err
		}
	}
	for _, l := range located {
		if err := (manifest.Entity{Ref: l.Ref, Repository: l.Repository, Root: l.Root}).Within(); err != nil {
			return err
		}
	}
	w.mu.Lock()
	if w.state == Populating {
		w.mu.Unlock()
		return errPopulating
	}
	held := append([]record.Repository(nil), w.manifest.Repositories...)
	for _, c := range checkouts {
		if err := another(held, c); err != nil {
			w.mu.Unlock()
			return err
		}
	}
	w.generation = generation
	if w.closed {
		w.set(Failed, errClosed.Error())
		w.mu.Unlock()
		return nil
	}
	w.set(Populating, "")
	// Counted with the lock held, as Close reads closed: no pass is
	// counted once Close waits for them.
	w.passes.Add(1)
	w.mu.Unlock()

	go func() {
		defer w.passes.Done()
		fail := func(err error) {
			w.mu.Lock()
			w.set(Failed, err.Error())
			w.mu.Unlock()
		}
		for _, c := range checkouts {
			at := -1
			for i, r := range held {
				if r.Name == c.Name {
					at = i
				}
			}
			ctx, done := context.WithTimeout(w.bringing, w.within)
			var err error
			if at < 0 {
				var cloned record.Repository
				if cloned, err = populate.Clone(ctx, w.system, w.root, c); err == nil {
					held = append(held, cloned)
				}
			} else {
				var widened record.Repository
				if widened, err = populate.Widen(ctx, w.system, w.root, held[at], c.Roots); err == nil {
					held[at] = widened
				}
			}
			// Where it was stopped, what stopped it is said, and not what
			// its version control system said of being stopped; any other
			// failure is said as its own.
			switch {
			case errors.Is(err, context.Canceled) && w.bringing.Err() != nil:
				err = fmt.Errorf("the repository %s: %w", c.Name, errClosed)
			case errors.Is(err, context.DeadlineExceeded):
				err = fmt.Errorf("the repository %s was not brought within %s", c.Name, w.within)
			}
			done()
			if err != nil {
				// What was cloned by now is the workspace's, and is
				// recorded: whoever asks again finishes the job.
				w.mu.Lock()
				w.manifest.Repositories = held
				recorded := w.manifest
				w.mu.Unlock()
				if kept := w.record(recorded); kept != nil {
					err = errors.Join(err, kept)
				}
				fail(err)
				return
			}
		}
		w.mu.Lock()
		w.manifest.Repositories = held
		for _, found := range located {
			known := false
			for _, held := range w.manifest.Known {
				known = known || held.Ref == found.Ref
			}
			if !known {
				w.manifest.Known = append(w.manifest.Known, manifest.Entity{Ref: found.Ref, Repository: found.Repository, Root: found.Root})
			}
		}
		recorded := w.manifest
		w.mu.Unlock()
		if err := w.record(recorded); err != nil {
			fail(err)
			return
		}
		w.mu.Lock()
		w.set(Ready, "")
		w.mu.Unlock()
	}()
	return nil
}

// record keeps the manifest, and clears what said the repositories it
// records were being cloned: once recorded, they are finished.
func (w *Workspace) record(m manifest.Manifest) error {
	if err := manifest.Write(w.root, m); err != nil {
		return err
	}
	return populate.Settle(w.root, m.Repositories)
}

// errPopulating is a source told while another is being brought: it is
// told again once that is done.
var errPopulating = errors.New("the workspace is being populated already")

// errClosed is a source told to a daemon that is stopping, or one it
// was bringing when it stopped.
var errClosed = errors.New("the daemon stopped before the workspace was populated")

// errMounted is a source told to a daemon that was given a checkout.
var errMounted = errors.New("the workspace is a checkout the sandbox was given; nothing is cloned into it")

// another says a checkout asks for a second checkout of a repository
// the workspace holds: the same name from another remote, at another
// branch or at another commit. A workspace holds one checkout of a
// repository, and what it holds is not passed off as what was asked.
func another(held []record.Repository, c record.Checkout) error {
	for _, r := range held {
		switch {
		case r.Name != c.Name:
		case r.Remote != c.Remote:
			// Neither remote is repeated: an address may hold a token.
			return fmt.Errorf("the workspace holds %s from another remote than the one it was asked for from: a workspace holds one checkout of a repository", r.Name)
		case r.Ref != c.Ref:
			return fmt.Errorf("the workspace holds %s at %s, and it was asked for at %s: a workspace holds one checkout of a repository", r.Name, at(r.Ref), at(c.Ref))
		}
	}
	return nil
}

// at names a branch or tag as it is spoken of.
func at(ref string) string {
	if ref == "" {
		return "its remote's default branch"
	}
	return ref
}

// New opens a workspace with the default version control system.
func New(root string, mounted bool) (*Workspace, error) {
	return Open(root, mounted, chosen.System())
}
