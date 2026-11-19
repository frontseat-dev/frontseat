package evaluate

import (
	"context"
	"fmt"
)

// RetentionLimits say how much may be kept for a session to keep an
// evaluation between requests. Records counts the answers and repository
// configuration kept and the queries of the trace; WireBytes counts each
// kept answer's response once and each query's request. Over either, what
// the evaluation does not rest on is let go and then the evaluation; the
// answers a workspace rests on are not, so those of a workspace that does
// not fit stay over, as a daemon that keeps no evaluation keeps them. They
// are no limit on the Go heap: parsed configuration, graphs, read sets and
// what others own are not counted. Zero in either keeps nothing between
// requests.
type RetentionLimits struct{ Records, WireBytes int64 }
type retentionUsage struct{ Records, WireBytes int64 }

func validateLimits(limits RetentionLimits) error {
	if limits.Records < 0 || limits.WireBytes < 0 {
		return fmt.Errorf("negative retained-session limit")
	}
	return nil
}

// retainedUsage is what the engine keeps now, as its limits count it. It is
// called under the engine's gate, or where nothing else uses the engine.
// What an answer holds never changes; the answers' lock guards which are kept.
func (p *retainedEngine) retainedUsage() retentionUsage {
	p.answers.mu.Lock()
	defer p.answers.mu.Unlock()
	out := retentionUsage{Records: int64(len(p.answers.kept) + len(p.answers.repositories))}
	seen := map[*answer]bool{}
	add := func(value *answer) {
		if value != nil && !seen[value] {
			seen[value] = true
			out.WireBytes += int64(len(value.response))
		}
	}
	for _, value := range p.answers.kept {
		add(value)
	}
	if p.trace != nil {
		out.Records += int64(len(p.trace.queries))
		for _, query := range p.trace.queries {
			out.WireBytes += int64(len(query.request))
			add(query.value)
		}
	}
	return out
}

// admit serializes operations and release. A cancelled queued release changes
// nothing; an admitted release runs while no evaluation can publish old state.
func (p *retainedEngine) admit(ctx context.Context) (func(), error) {
	p.once.Do(func() { p.gate = make(chan struct{}, 1) })
	select {
	case p.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-p.gate }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// forget drops only this owner's reuse. Caller-owned results, source.Keeper and
// compiled plugins have independent lifetimes. This adds no timer or goroutine.
func (p *retainedEngine) forget(ctx context.Context) error {
	release, err := p.admit(ctx)
	if err != nil {
		return err
	}
	defer release()
	p.discardAll()
	return nil
}

func (p *retainedEngine) discardDerived() {
	p.valid, p.trace, p.result, p.targets = false, nil, retainedResult{}, nil
	p.planValid, p.asked, p.profile, p.supplied, p.only = false, nil, 0, nil, nil
}

func (p *retainedEngine) discardAll() {
	p.discardDerived()
	p.answers.mu.Lock()
	defer p.answers.mu.Unlock()
	p.answers.kept, p.answers.repositories = nil, nil
}

// enforceRetention holds what is kept to the limits after each request,
// one that failed included; the request's own result was copied for its
// caller before. Where a limit is passed, the answers the evaluation kept
// does not rest on go first: those of another profile, of a run, or of
// what a file held before it changed. Where that is not enough, the
// evaluation and its plan go, and the answers it rested on stay, so that
// a workspace too large for the limits is evaluated for every request as
// by a daemon that keeps no evaluation, and no plugin is asked again for
// it. Zero in either limit keeps nothing at all.
func (p *retainedEngine) enforceRetention() {
	if p.retention.Records == 0 || p.retention.WireBytes == 0 {
		p.discardAll()
		p.evictions++
		return
	}
	over := func() bool {
		usage := p.retainedUsage()
		return usage.Records > p.retention.Records || usage.WireBytes > p.retention.WireBytes
	}
	if !over() {
		return
	}
	p.evictions++
	p.keepWhatIsRestedOn()
	if over() {
		p.discardDerived()
	}
}

// keepWhatIsRestedOn lets go of every answer the evaluation kept does not
// rest on. With no evaluation kept, nothing says which answers the
// workspace rests on, and none is let go: a request that failed where the
// evaluation before had gone over a limit leaves them for the next, and
// the clock lets go of what nothing asks for.
func (p *retainedEngine) keepWhatIsRestedOn() {
	if p.trace == nil {
		return
	}
	rested := map[*answer]bool{}
	for _, query := range p.trace.queries {
		rested[query.value] = true
	}
	for _, value := range p.trace.rested {
		rested[value] = true
	}
	p.answers.mu.Lock()
	defer p.answers.mu.Unlock()
	for key, value := range p.answers.kept {
		if !rested[value] {
			delete(p.answers.kept, key)
		}
	}
}

// stillAsked says the answers the evaluation kept rests on, and what the
// repositories it read said, were asked for now: a request answered from what is
// kept asks no plugin, and would otherwise leave them to be let go as
// answers nothing asked for (Answers.begin), though every request rests
// on them. What nothing rests on is then let go by the clock as before.
func (p *retainedEngine) stillAsked() {
	now := p.answers.clock()
	p.answers.mu.Lock()
	for _, query := range p.trace.queries {
		query.value.last = now
	}
	for _, value := range p.trace.rested {
		value.last = now
	}
	for _, repository := range p.trace.repositories {
		if said := p.answers.repositories[repository]; said != nil {
			said.last = now
		}
	}
	p.answers.mu.Unlock()
	p.answers.begin()
}
