// Package source is the source a daemon holds: the repositories in its
// directory, what they were made from, and whether they can be built.
// It is filled from repositories and entities, as the control plane
// tells it through the daemon's source API (Serve), and keeps no spec of
// its own.
package source

import "github.com/frontseat-dev/frontseat/server/internal/source/record"

import (
	"context"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/source/internal/vcs/git"
	"path/filepath"
	"sync"

	"github.com/frontseat-dev/frontseat/server/internal/source/internal/manifest"
	"github.com/frontseat-dev/frontseat/server/internal/source/internal/populate"
	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
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

	mu       sync.Mutex
	manifest manifest.Manifest
	state    State
	message  string
	// generation is the generation of the source it was last told to
	// hold; zero for one told none since it was opened.
	generation int64
	// changed is closed, and replaced, each time the state changes.
	changed chan struct{}
}

// Open returns the workspace in a directory: a mounted checkout, or what
// the directory's manifest records, or nothing yet. Its repositories are
// brought with a version control system, which a mounted checkout, that
// brings none, does without.
func Open(root string, mounted bool, system vcs.System) (*Workspace, error) {
	w := &Workspace{root: root, mounted: mounted, system: system, changed: make(chan struct{})}
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
	if found && len(recorded.Repositories) > 0 {
		w.manifest, w.state = recorded, Ready
	}
	return w, nil
}

// history returns what tells the history of one of the workspace's
// repositories, and the directory it is in. A repository is named as the
// workspace holds it, and no other directory is asked of.
func (w *Workspace) history(repository string) (vcs.History, string, error) {
	if w.mounted {
		return nil, "", errors.New("the history of a directory the daemon was given is not read yet; release from a workspace the daemon brought")
	}
	told, ok := w.system.(vcs.History)
	if !ok {
		return nil, "", errors.New("the workspace's version control system tells no history")
	}
	held, _ := w.Now()
	for _, each := range held.Repositories {
		if each.Name == repository {
			return told, filepath.Join(w.root, each.Name), nil
		}
	}
	return nil, "", fmt.Errorf("the workspace holds no repository %q", repository)
}

// Standing says where one of the workspace's repositories stands.
func (w *Workspace) Standing(ctx context.Context, repository string) (vcs.Standing, error) {
	told, directory, err := w.history(repository)
	if err != nil {
		return vcs.Standing{}, err
	}
	return told.Standing(ctx, directory)
}

// Tags returns the tags of one of the workspace's repositories.
func (w *Workspace) Tags(ctx context.Context, repository string) ([]vcs.Tag, error) {
	told, directory, err := w.history(repository)
	if err != nil {
		return nil, err
	}
	return told.Tags(ctx, directory)
}

// Changes returns what changed directories of one of the workspace's
// repositories since a revision.
func (w *Workspace) Changes(ctx context.Context, repository, since string, directories []string) ([]vcs.Change, error) {
	told, directory, err := w.history(repository)
	if err != nil {
		return nil, err
	}
	return told.Changes(ctx, directory, since, directories)
}

// Uncommitted returns the files of one of the workspace's repositories
// that are not as its revision has them.
func (w *Workspace) Uncommitted(ctx context.Context, repository string) ([]string, error) {
	told, directory, err := w.history(repository)
	if err != nil {
		return nil, err
	}
	return told.Uncommitted(ctx, directory)
}

// Ignored returns what is in one of the workspace's repositories that
// its version control system is told to ignore, and the directory the
// repository is in.
func (w *Workspace) Ignored(ctx context.Context, repository string) (paths []string, directory string, err error) {
	told, directory, err := w.history(repository)
	if err != nil {
		return nil, "", err
	}
	paths, err = told.Ignored(ctx, directory)
	return paths, directory, err
}

// Tag names a revision of one of the workspace's repositories by a tag.
func (w *Workspace) Tag(ctx context.Context, repository, name, revision string) error {
	told, directory, err := w.history(repository)
	if err != nil {
		return err
	}
	return told.Tag(ctx, directory, name, revision)
}

// TagName holds a name to one the workspace's version control system
// takes for a tag.
func (w *Workspace) TagName(ctx context.Context, name string) error {
	told, ok := w.system.(vcs.History)
	if !ok {
		return errors.New("the workspace's version control system tells no history")
	}
	return told.TagName(ctx, name)
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
// it holds once it starts.
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
	w.set(Populating, "")
	w.mu.Unlock()

	go func() {
		ctx := context.Background()
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
			if err != nil {
				// What was cloned by now is the workspace's, and is
				// recorded: whoever asks again finishes the job.
				w.mu.Lock()
				w.manifest.Repositories = held
				recorded := w.manifest
				w.mu.Unlock()
				if kept := manifest.Write(w.root, recorded); kept != nil {
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
		if err := manifest.Write(w.root, recorded); err != nil {
			fail(err)
			return
		}
		w.mu.Lock()
		w.set(Ready, "")
		w.mu.Unlock()
	}()
	return nil
}

// errPopulating is a source told while another is being brought: it is
// told again once that is done.
var errPopulating = errors.New("the workspace is being populated already")

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
			return fmt.Errorf("the workspace holds %s from %s, and it was asked for from %s: a workspace holds one checkout of a repository", r.Name, r.Remote, c.Remote)
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

// Fetching is what the default version control system says of fetching a
// repository from inside a sandbox, for whatever lets a sandbox fetch.
func Fetching() vcs.Fetching { return git.System{} }

// New opens a workspace with the default version control system.
func New(root string, mounted bool) (*Workspace, error) {
	return Open(root, mounted, git.System{})
}
