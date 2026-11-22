package build

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/release"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec/publish"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
)

// errReleaseNotFound is what asking for a release no daemon made, or one
// it remembers no more, fails with.
var errReleaseNotFound = errors.New("no release has that id")

// releases are the releases a daemon has made. A release carries out a
// plan: it builds the plan's units for production, and tags each unit
// that moves, in the daemon's checkout, once the build has passed.
// Each tag is then pushed to where its repository was brought from, and
// nothing else leaves the checkout: a release whose tags are not all
// there ends unpushed, and says why.
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
	lasting[*buildv1.Release]
	// etag is the plan's.
	etag string
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
		if m.state.GetId() == id {
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
	// A workspace's daemon publishes nothing yet: what publishes runs
	// where the daemon is, which is the workspace's sandbox, and that is
	// let through to no forge or registry. It is refused before anything
	// is built or tagged, and not once the tags are pushed, where a
	// publish that fails leaves a release that is not finished by making
	// it again. A daemon given a checkout, as a job's is, publishes.
	if !workspace.Mounted() {
		if label, found := publishedBy(evaluated.Actions, evaluated.Entities, planned); found {
			return nil, &refusedRelease{codes.Unimplemented, "PUBLISH_NOT_SERVED", fmt.Sprintf("%s publishes what this release ships, and a release from a workspace publishes nothing: release from a checkout given the daemon, as a job does (frontseatd --checkout)", label)}
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
		return nil, &refusedRelease{codes.FailedPrecondition, "RELEASE_BUSY", fmt.Sprintf("the release %s is being made; a daemon makes one at a time", m.state.GetId())}
	}
	id, err := newID("rel_", 8)
	if err != nil {
		return nil, err
	}
	m := &made{
		etag: request.GetEtag(),
		lasting: lasting[*buildv1.Release]{ended: make(chan struct{}), state: &buildv1.Release{
			Id: id, Plan: planned.Plan, State: buildv1.Release_STATE_BUILDING,
			CreateTime: timestamppb.New(rs.graph.runs.clock()),
		}},
	}
	// What moves is built for production by a run made with the release,
	// so that whoever made it follows the build by the run it is answered
	// with.
	var built *run
	if len(targets) > 0 {
		if built, err = rs.graph.runs.create(rs.ctx, typev1.Profile_PROFILE_PRODUCTION, targets, nil, false, false, nil, versionsOf(planned.Plan)); err != nil {
			return nil, err
		}
		m.state.BuildId = built.snapshot().GetId()
	}
	rs.made = remembered(append(rs.made, m))
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

// pushWithin is how long one tag may take to be pushed.
const pushWithin = 5 * time.Minute

// carryOut builds a plan's units, tags those that move, and pushes the
// tags.
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
		if !built.wait(ctx.Done()) {
			fail("the daemon ended before its units were built; nothing is tagged")
			return
		}
		if ran := built.snapshot(); ran.GetExitCode() != exitSuccess {
			fail("the production build %s ended %s; nothing is tagged", ran.GetId(), bazelName(ran.GetExitCode()))
			return
		}
	}
	// What the build packaged is what the release ships, each file by
	// its own name: two of one name could not both be published, and
	// the release says which before anything is tagged.
	shipped := map[string][]artifact{}
	if built != nil {
		shipped = built.shipped()
	}
	publishes := false
	if built != nil {
		with := built.published()
		publishes = with != nil && len(with.actions) > 0
	}
	named := map[string]artifact{}
	for _, unit := range planning.GetUnits() {
		if unit.GetNextVersion() == "" {
			continue
		}
		for _, member := range unit.GetMembers() {
			for _, each := range shipped[entity.FromProto(member).String()] {
				// What ships is a file: a package that left a directory
				// where it said a file left nothing that can be shipped.
				if each.directory {
					fail("%s left the directory %s, and a package leaves files; nothing is tagged", each.file.GetLabel(), each.at)
					return
				}
				// A name a publish could not be given is said before
				// anything is tagged, and not once the tags are pushed.
				if err := publish.Named(each.file.GetName()); err != nil && publishes {
					fail("%s left what cannot be published: %v; nothing is tagged", each.file.GetLabel(), err)
					return
				}
				if other, taken := named[each.file.GetName()]; taken && other.at != each.at {
					fail("%s and %s are each left as an artifact named %s, by %s and %s; nothing is tagged", other.at, each.at, each.file.GetName(), other.file.GetLabel(), each.file.GetLabel())
					return
				}
				named[each.file.GetName()] = each
			}
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
		if unit.GetNextVersion() != "" {
			for _, member := range unit.GetMembers() {
				for _, each := range shipped[entity.FromProto(member).String()] {
					became.Artifacts = append(became.Artifacts, each.file)
				}
			}
			sort.Slice(became.Artifacts, func(i, j int) bool { return became.Artifacts[i].GetName() < became.Artifacts[j].GetName() })
		}
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
		case len(unit.GetReasons()) > 0 && unit.GetReasons()[0].GetKind() == buildv1.ReleaseReason_KIND_RELEASED:
			became.Version, became.AlreadyReleased = unit.GetLastVersion(), true
		default:
			// A unit that holds is neither built nor tagged.
			continue
		}
		became.Tags = []*buildv1.ReleaseTag{{Repository: repository, Name: unit.GetTag(), Commit: commit[repository]}}
		m.change(func(r *buildv1.Release) { r.Units = append(r.Units, became) })
		tagged = true
	}
	// A release of nothing that was tagged has nothing to send.
	if !tagged {
		end(buildv1.Release_STATE_SUCCEEDED, "")
		return
	}
	// Each tag is sent to where its repository was brought from, the tag
	// alone, those of units found released already among them: a release
	// whose tags were not all sent is finished by making it again. A
	// workspace that may not push is refused there, and its tags stay in
	// the daemon's checkout, which the release then says.
	m.change(func(r *buildv1.Release) { r.State = buildv1.Release_STATE_PUSHING })
	for _, unit := range m.snapshot().GetUnits() {
		for _, tag := range unit.GetTags() {
			if ctx.Err() != nil {
				end(buildv1.Release_STATE_UNPUSHED, "the daemon ended before every tag was pushed; its tags are in the workspace")
				return
			}
			// A remote that takes a push and never answers would hold
			// the daemon's one release for as long as it runs.
			pushing, cancel := context.WithTimeout(ctx, pushWithin)
			err := workspace.PushTag(pushing, tag.GetRepository(), tag.GetName())
			cancel()
			if err != nil {
				end(buildv1.Release_STATE_UNPUSHED, fmt.Sprintf("%s was not pushed from %s: %v; its tags are in the workspace, and the release made again sends them", tag.GetName(), tag.GetRepository(), err))
				return
			}
		}
	}
	// What was shipped is published: by each action that publishes, for
	// each unit of its own that was tagged here. A unit found released
	// already had nothing built to publish, and a prerelease that is not
	// tagged has no tag to publish under.
	failed, stopped := rs.publish(ctx, m, planned, built, commit, shipped)
	if stopped {
		end(buildv1.Release_STATE_FAILED, "the daemon ended before everything was published; every tag is pushed, and a publish it ended may be half done")
		return
	}
	if len(failed) > 0 {
		end(buildv1.Release_STATE_FAILED, fmt.Sprintf("%s could not be published; every tag is pushed, and what was published stays: a release is published again once its tag and what was published of it are taken back", strings.Join(failed, " and ")))
		return
	}
	end(buildv1.Release_STATE_SUCCEEDED, "")
}

// publish runs what publishes each unit a release tagged, and notes on
// the unit what became of each. It returns which failed, each as the
// action and its unit; the release says why on the unit.
func (rs *releases) publish(ctx context.Context, m *made, planned *release.Planned, built *run, commit map[string]string, shipped map[string][]artifact) (failed []string, stopped bool) {
	if built == nil {
		return nil, false
	}
	with := built.published()
	if with == nil || len(with.actions) == 0 {
		return nil, false
	}
	runs := rs.graph.runs
	runner := publish.Runner{Grid: runs.answered, Tools: &tools.Provisioner{Executor: runs.executor, Platform: runs.platform}, Kept: runs.tools, Given: runs.given}
	units := map[string]*buildv1.ReleaseUnit{}
	for _, unit := range planned.Plan.GetUnits() {
		units[entity.FromProto(unit.GetEntity()).String()] = unit
	}
	began := false
	for at, became := range m.snapshot().GetUnits() {
		ref := entity.FromProto(became.GetEntity())
		unit := units[ref.String()]
		if became.GetAlreadyReleased() || len(became.GetTags()) == 0 || unit == nil {
			continue
		}
		repository := planned.Repositories[ref]
		of := publish.Release{
			Version:    pluginhost.Version{Version: unit.GetNextVersion(), Tag: unit.GetTag(), Commit: commit[repository], Unit: ref.String()},
			Notes:      unit.GetNotes(),
			Prerelease: unit.GetPrerelease(),
		}
		members := map[string]bool{}
		for _, member := range unit.GetMembers() {
			members[entity.FromProto(member).String()] = true
			for _, each := range shipped[entity.FromProto(member).String()] {
				of.Artifacts = append(of.Artifacts, publish.Artifact{Name: each.file.GetName(), Held: each.held})
			}
		}
		for _, action := range with.actions {
			if !publishes(action, with.entities, members, repository) {
				continue
			}
			if !began {
				began = true
				m.change(func(r *buildv1.Release) { r.State = buildv1.Release_STATE_PUBLISHING })
			}
			// A daemon that is ending publishes nothing more, and what
			// it did not run did not fail.
			if ctx.Err() != nil {
				return failed, true
			}
			said := &buildv1.PublishResult{Label: action.Label.String()}
			var err error
			if of.Remote, err = runs.workspace.Remote(ctx, repository); err == nil {
				err = runner.Run(ctx, with.snapshot, action, of)
			}
			if err != nil && ctx.Err() != nil {
				said.Failure = "the daemon ended while it ran: it may be half done"
				m.change(func(r *buildv1.Release) { r.Units[at].Publishes = append(r.Units[at].Publishes, said) })
				return failed, true
			}
			if err != nil {
				said.Failure = err.Error()
				failed = append(failed, fmt.Sprintf("%s of %s", action.Label, ref.Name))
			}
			m.change(func(r *buildv1.Release) { r.Units[at].Publishes = append(r.Units[at].Publishes, said) })
		}
	}
	return failed, false
}

// publishes is whether an action that publishes is a unit's: its
// owner's unit's, or, where no entity owns it, as at a repository's
// root, each unit's of its repository.
func publishes(action plan.Action, entities []record.Located, members map[string]bool, repository string) bool {
	owner, owned := graph.Owner(entities, action.Label.Path)
	return owned && members[owner.Ref.String()] || !owned && action.Repository == repository
}

// publishedBy is the first action that would publish a unit a plan
// tags, by its label: a unit found released already, or a prerelease
// that is not tagged, is published by nothing.
func publishedBy(actions []plan.Action, entities []record.Located, planned *release.Planned) (string, bool) {
	for _, unit := range planned.Plan.GetUnits() {
		if unit.GetNextVersion() == "" || unit.GetTag() == "" {
			continue
		}
		members := map[string]bool{}
		for _, member := range unit.GetMembers() {
			members[entity.FromProto(member).String()] = true
		}
		repository := planned.Repositories[entity.FromProto(unit.GetEntity())]
		for _, action := range actions {
			if action.IsPublish() && publishes(action, entities, members, repository) {
				return action.Label.String(), true
			}
		}
	}
	return "", false
}
