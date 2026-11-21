package build

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/release"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/model"
)

// releasesKept is how many releases that have ended a daemon remembers,
// the newest; one that has not ended is always remembered.
const releasesKept = 12

// errReleaseNotFound is what asking for a release no daemon made, or one
// it remembers no more, fails with.
var errReleaseNotFound = errors.New("no release has that id")

// releases are the releases a daemon has made. A release carries out a
// plan: it builds the plan's units for production, and tags each unit
// that moves, in the daemon's checkout, once the build has passed.
// Nothing leaves the checkout: a release ends unpublished.
//
// A daemon makes one release at a time. What it remembers of them it
// remembers while it runs; what a release did that lasts is the tags.
type releases struct {
	graph *graphService

	mu       sync.Mutex
	made     []*made
	stopping bool
	// ctx is cancelled when the daemon ends, and joined waits for every
	// release then being made to have stopped.
	ctx    context.Context
	cancel context.CancelFunc
	joined sync.WaitGroup
}

// made is one release, as it is now.
type made struct {
	mu      sync.Mutex
	release *buildv1.Release
	// etag is the plan's, and ended is closed when the release has ended.
	etag  string
	ended chan struct{}
}

func (m *made) snapshot() *buildv1.Release {
	m.mu.Lock()
	defer m.mu.Unlock()
	return proto.Clone(m.release).(*buildv1.Release)
}

func (m *made) change(change func(*buildv1.Release)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	change(m.release)
}

func (m *made) over() bool {
	select {
	case <-m.ended:
		return true
	default:
		return false
	}
}

func newReleases(graph *graphService) *releases {
	ctx, cancel := context.WithCancel(context.Background())
	return &releases{graph: graph, ctx: ctx, cancel: cancel}
}

// stop ends every release being made, which then fails having tagged
// only what it had, and waits for each to have stopped. It is called
// before the runs are stopped: a release waits on a run.
func (rs *releases) stop() {
	rs.mu.Lock()
	rs.stopping = true
	rs.mu.Unlock()
	rs.cancel()
	rs.joined.Wait()
}

func (rs *releases) get(id string) (*made, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, m := range rs.made {
		if m.release.GetId() == id {
			return m, nil
		}
	}
	return nil, errReleaseNotFound
}

// refusedRelease is a release refused before it began, with the status
// it is refused by.
type refusedRelease struct {
	code    codes.Code
	reason  string
	message string
}

func (r *refusedRelease) Error() string { return r.message }

// create carries out a plan. The request is planned again, and must come
// to the plan the etag names; the repositories it is planned from must be
// their commits, with nothing uncommitted. A release of the same plan
// that is being made is the one returned.
func (rs *releases) create(ctx context.Context, request *buildv1.CreateReleaseRequest) (*made, error) {
	if request.GetEtag() == "" {
		return nil, &refusedRelease{codes.InvalidArgument, "ETAG_MISSING", "name the plan to release, by its etag"}
	}
	evaluated, err := rs.graph.evaluated(ctx)
	if err != nil {
		return nil, err
	}
	workspace := rs.graph.runs.workspace
	planned, err := release.Prepare(ctx, releasing(evaluated), strategies(rs.graph.runs.plugins), workspace, &buildv1.PlanReleaseRequest{Entities: request.GetEntities()})
	if err != nil {
		return nil, err
	}
	if planned.Plan.GetEtag() != request.GetEtag() {
		return nil, &refusedRelease{codes.Aborted, "ETAG_MISMATCH", "the plan is no longer what releasing would do; plan again"}
	}
	// A release is of commits: what is not committed is in no tag, and
	// what a unit is built from is in every repository of the workspace.
	for _, at := range planned.Plan.GetCommits() {
		if err := rs.committed(ctx, at.GetRepository()); err != nil {
			return nil, err
		}
	}
	// What moves is built for production, as a production build builds
	// it: every member that has something to build, in every directory of
	// its own. A member that builds nothing, as an API described beside
	// the code that serves it, is released with its unit and built by
	// nothing.
	var targets []string
	for _, unit := range planned.Plan.GetUnits() {
		if unit.GetNextVersion() == "" {
			continue
		}
		for _, member := range unit.GetMembers() {
			target := entity.FromProto(member).String()
			if _, err := plan.Pointed(evaluated.Actions, evaluated.Entities, []string{target}, nil); err == nil {
				targets = append(targets, target)
			}
		}
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.stopping {
		return nil, errStopped
	}
	for _, m := range rs.made {
		if m.over() {
			continue
		}
		if m.etag == request.GetEtag() {
			return m, nil
		}
		return nil, &refusedRelease{codes.FailedPrecondition, "RELEASE_BUSY", fmt.Sprintf("the release %s is being made; a daemon makes one at a time", m.release.GetId())}
	}
	id, err := newReleaseID()
	if err != nil {
		return nil, err
	}
	m := &made{
		etag:  request.GetEtag(),
		ended: make(chan struct{}),
		release: &buildv1.Release{
			Id: id, Plan: planned.Plan, State: buildv1.Release_STATE_BUILDING,
			CreateTime: timestamppb.New(rs.graph.runs.clock()),
		},
	}
	// What moves is built for production by a run made with the release,
	// so that whoever made it follows the build by the run it is answered
	// with.
	var built *run
	if len(targets) > 0 {
		if built, err = rs.graph.runs.create(rs.ctx, "production", pluginv1.Profile_PROFILE_PRODUCTION, targets, nil, false, false, nil, versionsOf(planned.Plan)); err != nil {
			return nil, err
		}
		m.release.BuildId = built.snapshot().GetId()
	}
	rs.made = append(rs.made, m)
	rs.forget()
	rs.joined.Add(1)
	go func() {
		defer rs.joined.Done()
		rs.carryOut(m, planned, built)
	}()
	return m, nil
}

// versionsOf is what a plan tells the build made for it: each member of
// a unit that moves is released at the unit's version, under its tag,
// as part of that unit.
func versionsOf(planned *buildv1.ReleasePlan) *versions {
	out := &versions{units: map[string]pluginhost.Version{}, commits: map[string]string{}}
	for _, at := range planned.GetCommits() {
		out.commits[at.GetRepository()] = at.GetCommit()
	}
	for _, unit := range planned.GetUnits() {
		if unit.GetNextVersion() == "" {
			continue
		}
		version := pluginhost.Version{Version: unit.GetNextVersion(), Tag: unit.GetTag(), Unit: entity.FromProto(unit.GetEntity()).String()}
		for _, member := range unit.GetMembers() {
			out.units[entity.FromProto(member).String()] = version
		}
	}
	return out
}

// forget lets go of releases that have ended beyond the newest kept.
// It is called with the lock held.
func (rs *releases) forget() {
	over := 0
	for _, m := range rs.made {
		if m.over() {
			over++
		}
	}
	kept := rs.made[:0]
	for _, m := range rs.made {
		if m.over() && over > releasesKept {
			over--
			continue
		}
		kept = append(kept, m)
	}
	rs.made = kept
}

// committed holds a repository of the workspace to being its commit, as
// far as a build reads it: nothing its version control system says is
// uncommitted, or cannot say is committed, and nothing that system
// ignores that a run would read all the same.
func (rs *releases) committed(ctx context.Context, repository string) error {
	workspace := rs.graph.runs.workspace
	uncommitted, err := workspace.Uncommitted(ctx, repository)
	if err != nil {
		return fmt.Errorf("the history of %s could not be read: %w", repository, err)
	}
	ignored, directory, err := workspace.Ignored(ctx, repository)
	if err != nil {
		return fmt.Errorf("the history of %s could not be read: %w", repository, err)
	}
	read, err := source.Seen(directory, ignored)
	if err != nil {
		return fmt.Errorf("%s could not be read: %w", repository, err)
	}
	if len(uncommitted) > 0 {
		return &model.Mistake{Message: fmt.Sprintf("%s has changes that are not committed (%s); a release is of a commit", repository, some(uncommitted))}
	}
	if len(read) > 0 {
		return &model.Mistake{Message: fmt.Sprintf("%s holds files its commit does not, which a build reads (%s); a release is of a commit: remove them, or name them in %s", repository, some(read), source.IgnoreFile)}
	}
	return nil
}

// some names the first few of a list, and says how many more there are.
func some(paths []string) string {
	if len(paths) > 3 {
		return fmt.Sprintf("%s and %d more", strings.Join(paths[:3], ", "), len(paths)-3)
	}
	return strings.Join(paths, ", ")
}

// carryOut builds a plan's units and tags those that move.
func (rs *releases) carryOut(m *made, planned *release.Planned, built *run) {
	end := func(state buildv1.Release_State, failure string) {
		now := rs.graph.runs.clock()
		m.change(func(r *buildv1.Release) {
			r.State, r.Failure, r.EndTime = state, failure, timestamppb.New(now)
		})
		close(m.ended)
	}
	fail := func(format string, a ...any) {
		end(buildv1.Release_STATE_FAILED, fmt.Sprintf(format, a...))
	}
	ctx := rs.ctx
	workspace := rs.graph.runs.workspace
	planning := planned.Plan
	commit := map[string]string{}
	for _, at := range planning.GetCommits() {
		commit[at.GetRepository()] = at.GetCommit()
	}
	if built != nil {
		for {
			_, ended, changed := built.since(1 << 62)
			if ended {
				break
			}
			select {
			case <-changed:
			case <-ctx.Done():
				fail("the daemon ended before its units were built; nothing is tagged")
				return
			}
		}
		if ran := built.snapshot(); ran.GetExitCode().GetCode() != 0 {
			fail("the production build %s ended %s; nothing is tagged", ran.GetId(), ran.GetExitCode().GetName())
			return
		}
	}
	m.change(func(r *buildv1.Release) { r.State = buildv1.Release_STATE_TAGGING })
	// What was built is what is tagged: each repository is still the
	// commit it was planned from, with nothing uncommitted. A change that
	// was made and undone while the build ran is not seen here.
	for _, at := range planning.GetCommits() {
		standing, err := workspace.Standing(ctx, at.GetRepository())
		switch {
		case err == nil && standing.Revision != at.GetCommit():
			err = fmt.Errorf("it stands at %s", standing.Revision)
		case err == nil:
			err = rs.committed(ctx, at.GetRepository())
		}
		if err != nil {
			fail("%s is no longer the commit %s it was planned from: %v; nothing is tagged", at.GetRepository(), at.GetCommit(), err)
			return
		}
	}
	tagged := false
	for _, unit := range planning.GetUnits() {
		if ctx.Err() != nil {
			fail("the daemon ended before every unit was tagged")
			return
		}
		ref := entity.FromProto(unit.GetEntity())
		repository := planned.Repositories[ref]
		became := &buildv1.ReleasedUnit{Entity: unit.GetEntity()}
		switch {
		case unit.GetNextVersion() != "" && unit.GetTag() == "":
			// A prerelease its unit does not tag is built, and that is
			// all a release makes of it here.
			became.Version = unit.GetNextVersion()
			m.change(func(r *buildv1.Release) { r.Units = append(r.Units, became) })
			continue
		case unit.GetNextVersion() != "":
			if err := workspace.Tag(ctx, repository, unit.GetTag(), commit[repository]); err != nil {
				fail("%s could not be tagged %s: %v", ref.Name, unit.GetTag(), err)
				return
			}
			became.Version = unit.GetNextVersion()
		case len(unit.GetReasons()) > 0 && unit.GetReasons()[0].GetKind() == buildv1.ReasonKind_REASON_KIND_RELEASED:
			became.Version, became.Already = unit.GetLastVersion(), true
		default:
			// A unit that holds is neither built nor tagged.
			continue
		}
		became.Tags = []*buildv1.ReleaseTag{{Repository: repository, Name: unit.GetTag(), Commit: commit[repository]}}
		m.change(func(r *buildv1.Release) { r.Units = append(r.Units, became) })
		tagged = true
	}
	// Its tags are in the daemon's checkout and nowhere else: it is not
	// published. A release of nothing that moves has nothing to publish.
	if tagged {
		end(buildv1.Release_STATE_UNPUBLISHED, "")
		return
	}
	end(buildv1.Release_STATE_SUCCEEDED, "")
}

func newReleaseID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "rel_" + hex.EncodeToString(b[:]), nil
}
