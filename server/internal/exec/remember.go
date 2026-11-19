package exec

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Keyed is an executor that can say what an action is without running
// it: two actions it gives one key are, to it, the same action, and
// have the same answer.
type Keyed interface {
	Executor
	// Key returns what the executor knows an action by.
	Key(ctx context.Context, action Action) (string, error)
}

// Remembering is an executor that keeps what another answered, for a
// daemon's whole life, and answers the same action with it: the grid is
// asked only about an action it was not asked about before. This is
// what Bazel's server and Buck2's daemon do for an action whose inputs
// have not changed. An executor that cannot say what an action is is
// asked every time.
//
// What is kept is what a grid keeps: the answer for an action that
// passed. One that failed is asked again, as the grid runs it again.
// An answer is kept with what its action printed, so one that printed
// more than a little is not kept: the grid is asked for it each time,
// and answers from what it kept.
//
// An answer names content by its digest, and the grid may let content
// go. Nothing here says that it has; the action that is handed such
// content finds out, and says so (ErrNotKept). Everything kept is let
// go then, since any of it may name what is gone, and whoever runs the
// actions starts over and asks for each again; a run that starts over
// for another reason lets go of it itself (Forget).
//
// For the same reason an answer is gone by only for a while after the
// grid gave it, and is then let go: three hours, which is how long
// Bazel takes a remote cache to keep what it has just answered with
// (--experimental_remote_cache_ttl). That is three hours by the clock
// on the wall, whatever the machine slept through.
type Remembering struct {
	// Executor is asked what is not known.
	Executor Executor

	// now is the time; time.Now unless a test says otherwise.
	now func() time.Time

	mu      sync.Mutex
	answers map[string]remembered
	merged  map[string]mergedAt
	listed  map[Digest]listedAt
}

type listedAt struct {
	files map[string]Held
	at    time.Time
}

var (
	_ Executor = (*Remembering)(nil)
	_ Merging  = (*Remembering)(nil)
)

// mergedAt is directories put together, and when the grid did it.
type mergedAt struct {
	held Held
	at   time.Time
}

// remembered is an answer, and when the grid gave it.
type remembered struct {
	result Result
	at     time.Time
}

// lasts is how long an answer is gone by after the grid gave it.
const lasts = 3 * time.Hour

// printed is the most an action may have printed for its answer to be
// kept.
const printed = 64 << 10

// clock returns the time by the clock on the wall.
func (r *Remembering) clock() time.Time {
	if r.now != nil {
		return r.now().Round(0)
	}
	return time.Now().Round(0)
}

// Begin says a run begins. The answers that are too old to be gone by
// are let go.
func (r *Remembering) Begin() {
	now := r.clock()
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, each := range r.answers {
		if now.Sub(each.at) >= lasts {
			delete(r.answers, key)
		}
	}
	for key, each := range r.merged {
		if now.Sub(each.at) >= lasts {
			delete(r.merged, key)
		}
	}
	for key, each := range r.listed {
		if now.Sub(each.at) >= lasts {
			delete(r.listed, key)
		}
	}
}

// Forget lets go of everything kept: what a run does when it starts
// over, having found that the grid let something go.
func (r *Remembering) Forget() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answers, r.merged = nil, nil
	r.listed = nil
}

// List remembers immutable directory metadata by its content digest. Consumers
// must not mutate the returned map. Eviction/retry forgets this with all other
// answers; the grid may have dropped any referenced blob.
func (r *Remembering) List(ctx context.Context, directory Held) (map[string]Held, error) {
	listing, ok := r.Executor.(Listing)
	if !ok {
		return nil, fmt.Errorf("the grid cannot list generated outputs")
	}
	if !directory.Directory {
		return nil, fmt.Errorf("generated output is not a directory")
	}
	r.mu.Lock()
	known, kept := r.listed[directory.Digest]
	r.mu.Unlock()
	if kept && r.clock().Sub(known.at) < lasts {
		return known.files, nil
	}
	files, err := listing.List(ctx, directory)
	if errors.Is(err, ErrNotKept) {
		r.Forget()
	}
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listed == nil {
		r.listed = map[Digest]listedAt{}
	}
	r.listed[directory.Digest] = listedAt{files: files, at: r.clock()}
	return files, nil
}

// Materialize implements Materializing, asking the grid each time: what
// is written is kept by whoever asked, keyed by what it is.
func (r *Remembering) Materialize(ctx context.Context, directory Held, into string) error {
	materializing, ok := r.Executor.(Materializing)
	if !ok {
		return fmt.Errorf("the grid cannot hand back a directory it holds")
	}
	err := materializing.Materialize(ctx, directory, into)
	if errors.Is(err, ErrNotKept) {
		r.Forget()
	}
	return err
}

// Merge implements Merging. What was put together is kept as an answer
// is, for as long and let go with it: a run in which nothing changed
// asks the grid to put nothing together.
func (r *Remembering) Merge(ctx context.Context, all []Held) (Held, error) {
	merging, can := r.Executor.(Merging)
	if !can {
		return Held{}, ErrCannotMerge
	}
	names := make([]string, len(all))
	for i, each := range all {
		names[i] = fmt.Sprintf("%s-%d-%t", each.Digest.Hash, each.Digest.Size, each.Directory)
	}
	sort.Strings(names)
	key := strings.Join(names, " ")
	r.mu.Lock()
	known, kept := r.merged[key]
	r.mu.Unlock()
	if kept && r.clock().Sub(known.at) < lasts {
		return known.held, nil
	}
	held, err := merging.Merge(ctx, all)
	if errors.Is(err, ErrNotKept) {
		r.Forget()
	}
	if err != nil {
		return Held{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.merged == nil {
		r.merged = map[string]mergedAt{}
	}
	r.merged[key] = mergedAt{held: held, at: r.clock()}
	return held, nil
}

// Execute implements Executor.
func (r *Remembering) Execute(ctx context.Context, action Action) (Result, error) {
	keyed, can := r.Executor.(Keyed)
	if !can {
		return r.Executor.Execute(ctx, action)
	}
	key, err := keyed.Key(ctx, action)
	if err != nil {
		return Result{}, err
	}
	r.mu.Lock()
	known, kept := r.answers[key]
	r.mu.Unlock()
	if kept && r.clock().Sub(known.at) < lasts {
		// Kept from an earlier execution, and of no worker's for this one.
		again := known.result
		again.Cached, again.Started, again.Worked, again.Duration = true, time.Time{}, 0, 0
		return again, nil
	}
	result, err := r.Executor.Execute(ctx, action)
	if errors.Is(err, ErrNotKept) {
		r.Forget()
	}
	if err != nil || result.ExitCode != 0 || len(result.Stdout)+len(result.Stderr) > printed {
		return result, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.answers == nil {
		r.answers = map[string]remembered{}
	}
	r.answers[key] = remembered{result: result, at: r.clock()}
	return result, nil
}
