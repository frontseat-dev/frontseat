package evaluate

import (
	"context"
	"slices"
	"sync"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
)

// retainedResult is what an engine keeps: an evaluation, and the actions
// of the plan last made of it.
type retainedResult struct {
	evaluation Evaluation
	actions    []plan.Action
	// publishes are the actions of the evaluation that publish, found
	// once: few workspaces publish, and no build should look again.
	publishes []plan.Action
}

// trackedReads is what an evaluation read of each repository beside what
// its plugins' kept answers read: by it the evaluation is held to a later
// source.
type trackedReads struct {
	repositories []string
	byRepository map[string][]pluginhost.Read
}

func (r *trackedReads) same(ctx context.Context, src Source) (bool, error) {
	if r == nil || !slices.Equal(r.repositories, src.Repositories()) {
		return false, nil
	}
	for _, name := range r.repositories {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		same, err := pluginhost.Same(ctx, repositoryView{source: src, repository: name}, r.byRepository[name])
		if err != nil || !same {
			return false, err
		}
	}
	return true, nil
}

// trackedSource is a source that records what is read through it.
type trackedSource struct {
	Source
	mu        sync.Mutex
	recorders map[string]*pluginhost.Recorder
	digests   map[string][]pluginhost.Read
}

func (o *trackedSource) recorder(name string) *pluginhost.Recorder {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.recorders[name] == nil {
		o.recorders[name] = &pluginhost.Recorder{Source: repositoryView{source: o.Source, repository: name}}
	}
	return o.recorders[name]
}
func (o *trackedSource) Read(ctx context.Context, repository, path string) ([]byte, bool, error) {
	return o.recorder(repository).Read(ctx, path)
}
func (o *trackedSource) Within(ctx context.Context, repository, directory string, patterns []string) ([]string, error) {
	return o.recorder(repository).Glob(ctx, directory, patterns)
}
func (o *trackedSource) Digest(ctx context.Context, repository, path string) (string, bool, error) {
	digest, found, err := o.Source.Digest(ctx, repository, path)
	if err == nil {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.digests == nil {
			o.digests = map[string][]pluginhost.Read{}
		}
		if !found {
			digest = ""
		}
		o.digests[repository] = append(o.digests[repository], pluginhost.Read{Import: "read", Argument: path, Found: found, Digest: digest})
	}
	return digest, found, err
}
func (o *trackedSource) reads() *trackedReads {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := &trackedReads{repositories: slices.Clone(o.Source.Repositories()), byRepository: map[string][]pluginhost.Read{}}
	for _, name := range r.repositories {
		all := slices.Clone(o.digests[name])
		if recorder := o.recorders[name]; recorder != nil {
			all = append(all, recorder.Reads()...)
		}
		seen := map[pluginhost.Read]bool{}
		for _, read := range all {
			if !seen[read] {
				r.byRepository[name] = append(r.byRepository[name], read)
				seen[read] = true
			}
		}
	}
	return r
}
