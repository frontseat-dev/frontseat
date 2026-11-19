package evaluate

import (
	"context"
	"errors"
	"fmt"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"slices"
	"sync"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"golang.org/x/sync/semaphore"
)

// SourceProvider takes a fresh view of this session's workspace. It must honor
// cancellation, own its returned entities, and not replace the plugin set.
type SourceProvider func(context.Context) (Source, []record.Located, error)

// ErrSessionClosed means admission ended permanently.
var ErrSessionClosed = errors.New("evaluation session is closed")

// Session keeps the evaluation of one workspace, for one set of plugins,
// between requests, and holds it to the source by what was read: a request
// over source that reads the same is given what was evaluated, and what
// changed is evaluated again. The daemon owns the source keeper and the
// compiled plugins; Close joins all work that might use them before the
// daemon releases them. A build is planned from its run's own snapshot; a
// graph request takes a fresh one. A run and a release plan evaluate by
// themselves (Evaluate), over the same Answers.
type Session struct {
	core   retainedEngine
	source SourceProvider
	// As many graph operations as the session admits own source at once,
	// each from taking it through the projection it returns. Waiting
	// callers retain request bookkeeping, not a fresh workspace snapshot.
	// Builds keep their independently owned starting snapshot and core gate.
	graphSource *semaphore.Weighted
	mu          sync.Mutex
	closed      bool
	active      map[uint64]context.CancelFunc
	next        uint64
	going       sync.WaitGroup
	once        sync.Once
}

// NewSession makes a session that keeps what its limits allow and admits
// so many graph requests at once (admitted): each owns source from taking
// it through the projection it returns, and the rest wait holding none.
// How many is the daemon's to say, with what it keeps; fewer than one
// is refused.
//
// What plugins answered is kept in answers, which is the daemon's one
// record of it: whatever else evaluates beside the session, a run or
// a release plan, and what reports on a check, asks the same, so nothing
// is asked or kept twice, and all of it is under the session's limits.
func NewSession(plugins []*pluginhost.Plugin, limits RetentionLimits, admitted int, answers *Answers, source SourceProvider) (*Session, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	if admitted < 1 {
		return nil, fmt.Errorf("a session admits %d graph requests at once, and must admit one", admitted)
	}
	if answers == nil {
		return nil, fmt.Errorf("a session is given where what plugins answered is kept")
	}
	if source == nil {
		return nil, fmt.Errorf("missing session source provider")
	}
	return &Session{core: retainedEngine{plugins: slices.Clone(plugins), answers: answers, retention: limits}, source: source, graphSource: semaphore.NewWeighted(int64(admitted)), active: map[uint64]context.CancelFunc{}}, nil
}

func (s *Session) start(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, ErrSessionClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	id := s.next
	s.next++
	s.active[id] = cancel
	s.going.Add(1)
	return ctx, func() {
		cancel()
		s.mu.Lock()
		delete(s.active, id)
		s.mu.Unlock()
		s.going.Done()
	}, nil
}

func (s *Session) operation(ctx context.Context, kind projectionKind) (projectionResult, error) {
	ctx, finish, err := s.start(ctx)
	if err != nil {
		return projectionResult{}, err
	}
	defer finish()
	if err := s.graphSource.Acquire(ctx, 1); err != nil {
		return projectionResult{}, err
	}
	defer s.graphSource.Release(1)
	src, entities, err := s.source(ctx)
	if err != nil {
		return projectionResult{}, err
	}
	return s.core.operationSelected(ctx, src, pluginv1.Profile_PROFILE_DEVELOPMENT, nil, nil, kind, entities)
}

// Projects returns an owned projection without materializing a full graph copy.
func (s *Session) Projects(ctx context.Context) (*buildv1.ListProjectsResponse, error) {
	out, err := s.operation(ctx, projectProjects)
	return out.projects, err
}

// Facts returns owned semantic evaluation facts without planning a build.
func (s *Session) Facts(ctx context.Context) (Evaluation, error) {
	out, err := s.operation(ctx, projectFacts)
	return out.facts, err
}

// Build plans from the caller's run-owned snapshot, which execution continues
// to use after this method returns. Source acquisition belongs to run lifetime.
// No mutable retained value or snapshot escapes from the engine.
func (s *Session) Build(ctx context.Context, src Source, profile pluginv1.Profile, entities []record.Located, targets, only []string) (BuildPlan, error) {
	ctx, finish, err := s.start(ctx)
	if err != nil {
		return BuildPlan{}, err
	}
	defer finish()
	out, err := s.core.operationSelected(ctx, src, profile, targets, only, projectPlan, entities)
	return BuildPlan{Needed: out.plan.Needed, Asked: out.plan.Asked, Entities: out.plan.Entities, Work: out.work}, err
}

// Close closes admission, cancels and joins source acquisition and evaluations,
// then drops retained state. Concurrent Close callers all wait for completion.
func (s *Session) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		cancellations := make([]context.CancelFunc, 0, len(s.active))
		for _, cancel := range s.active {
			cancellations = append(cancellations, cancel)
		}
		s.mu.Unlock()
		for _, cancel := range cancellations {
			cancel()
		}
		s.going.Wait()
		_ = s.core.forget(context.Background())
	})
}

// SessionStats is what a session has done and what it keeps now: how often
// it evaluated and planned, and what it keeps as its limits count it. No
// call of the API serves it and nothing is decided by it; the daemon's
// tests hold reuse and the limits to it.
type SessionStats struct {
	Evaluations, Plans int
	Records, WireBytes int64
}

// Stats waits its turn among the session's requests, and so says what was
// so between two of them.
func (s *Session) Stats(ctx context.Context) (SessionStats, error) {
	release, err := s.core.admit(ctx)
	if err != nil {
		return SessionStats{}, err
	}
	defer release()
	u := s.core.retainedUsage()
	return SessionStats{Evaluations: s.core.evaluations, Plans: s.core.plans, Records: u.Records, WireBytes: u.WireBytes}, nil
}
