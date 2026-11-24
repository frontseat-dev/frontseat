package build

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/frontseat-dev/frontseat/build/daemon/directory"
	"github.com/frontseat-dev/frontseat/build/engine/engine/evaluate"
	"github.com/frontseat-dev/frontseat/build/engine/engine/graph"
	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/download"
	"github.com/frontseat-dev/frontseat/build/engine/exec/program"
	runexec "github.com/frontseat-dev/frontseat/build/engine/exec/run"
	"github.com/frontseat-dev/frontseat/build/engine/history"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/core/telemetry"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/sdk/go/enum"
)

// How a run ends, in Bazel's names.
const (
	exitSuccess      = buildv1.ExitCode_EXIT_CODE_SUCCESS
	exitBuildFailure = buildv1.ExitCode_EXIT_CODE_BUILD_FAILURE
	exitTestsFailed  = buildv1.ExitCode_EXIT_CODE_TESTS_FAILED
	// exitNoTests is Bazel's for a test of what holds no tests.
	exitNoTests     = buildv1.ExitCode_EXIT_CODE_NO_TESTS_FOUND
	exitInterrupted = buildv1.ExitCode_EXIT_CODE_INTERRUPTED
	exitInternal    = buildv1.ExitCode_EXIT_CODE_INTERNAL_ERROR
)

// bazelName is how a run ended, by Bazel's name for it: SUCCESS.
func bazelName(code buildv1.ExitCode) string {
	return enum.Name(code)
}

// errNotFound is a run that does not exist.
var errNotFound = errors.New("no build has that id")

// builds is a daemon's builds. A build belongs to the daemon, not to the
// call that made it: it goes on when its caller goes away.
type builds struct {
	workspace *directory.Directory
	plugins   []*pluginhost.Plugin
	// tools is where a fix's work is kept: tool trees by digest, and copies.
	tools string
	// given are the variables of where the daemon runs that what
	// publishes a release may be given: those the daemon was told.
	given []string
	// said is where the daemon says what a person who reads its log
	// should know of it; none, it is said to telemetry alone.
	said     io.Writer
	executor exec.Executor
	// downloads brings what a shared fetch downloads to the grid; nil
	// where the daemon was given no asset service.
	downloads download.Fetcher
	// answered is the executor with what it answered kept: an action it
	// was asked about before is not asked about again.
	answered *exec.Remembering
	// platform is what an action asks of the workers that run it.
	platform map[string]string
	// telemetry connects each run to its originating command.
	telemetry *telemetry.Signals
	// history is where the runs outlive the daemon.
	history *history.Store
	// now is the time; time.Now unless a test says otherwise.
	now func() time.Time
	// files is what the daemon keeps of the workspace's files from one
	// run to the next, and answers what its plugins answered: a run reads
	// only what looks different, and asks a plugin only where what it
	// read has changed.
	files      *source.Keeper
	answers    *evaluate.Answers
	evaluation *evaluate.Session

	mu    sync.Mutex
	byID  map[string]*run
	order []*run
	// stopped says the daemon has ended: no run is made after it.
	stopped bool
	// going are the runs that have not ended.
	going sync.WaitGroup
}

// busy says whether a run has not ended.
func (rs *builds) busy() bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, r := range rs.order {
		select {
		case <-r.done:
		default:
			return true
		}
	}
	return false
}

// errStopped is a run asked of a daemon that has ended.
var errStopped = errors.New("the daemon is ending, and makes no run")

// stop ends the daemon's runs: each is cancelled, and stop returns once
// all have ended.
func (rs *builds) stop() {
	rs.mu.Lock()
	rs.stopped = true
	made := append([]*run(nil), rs.order...)
	rs.mu.Unlock()
	for _, r := range made {
		r.cancel()
	}
	rs.going.Wait()
	// What it knew of the directory's files is kept for the daemon after
	// it, once no run reads them, and while the state is still this
	// daemon's: whoever ends it lets go of the state only after this
	// (life.Take). Lost, the files are only read again, and what was
	// saved before stays, which is gone by no further than each file
	// still looks as it did.
	_ = rs.files.Save()
	// A fix's work is the daemon's for as long as it runs, and nothing's
	// after. What cannot be removed now is cleared by the daemon that
	// keeps its runs here next. It is removed while the runs' directory
	// is still this daemon's: let go of first, the daemon after this one
	// could have its own work there removed.
	_ = os.RemoveAll(rs.tools)
	rs.history.Release()
}

// newBuilds returns a daemon's builds: those kept in a directory by the
// daemons before it, and those it will make. A run a daemon died under
// is ended here, and says so. The runs that ended long ago are let go by
// the clock given, which is the machine's own where none is.
func newBuilds(held *directory.Directory, plugins []*pluginhost.Plugin, executor exec.Executor, downloads download.Fetcher, platform map[string]string, signals *telemetry.Signals, kept string, now func() time.Time) (*builds, error) {
	keptHistory, err := history.Open(kept)
	if err != nil {
		return nil, err
	}
	// A fix's work, the tool trees it runs with and its copies, is kept
	// here for as long as the daemon runs: the grid is what keeps the
	// trees. It is beside the runs, a directory that is one daemon's at
	// a time, so whatever a daemon died holding is cleared by the next.
	tools := filepath.Join(kept, "work")
	if err := os.RemoveAll(tools); err != nil {
		keptHistory.Release()
		return nil, err
	}
	rs := &builds{workspace: held, plugins: plugins, executor: executor, downloads: downloads, answered: &exec.Remembering{Executor: executor}, platform: platform, telemetry: signals, history: keptHistory, files: &source.Keeper{}, answers: &evaluate.Answers{}, byID: map[string]*run{}, tools: tools, now: now}
	before, err := keptHistory.All()
	if err != nil {
		keptHistory.Release()
		return nil, err
	}
	for _, each := range before {
		// A run is known by the name its record is kept under, which is
		// what its record was read by.
		id := each.Made.GetId()
		r := recorded(each, rs.clock)
		rs.byID[id] = r
		rs.order = append(rs.order, r)
	}
	rs.prune()
	return rs, nil
}

// recorded returns a run as its record holds it: the run as it was
// made, and then what each of its events says. One with no last event
// is one its daemon died under: nobody finished it and nobody cancelled
// it, and it ends in error, at the last moment it is known to have been
// under way.
func recorded(kept history.Read, clock func() time.Time) *run {
	r := &run{
		clock:   clock,
		cancel:  func() {},
		changed: make(chan struct{}),
		done:    make(chan struct{}),
		state:   kept.Made,
		results: map[string]*buildv1.ActionResult{},
		tests:   map[string]bool{},
		record:  kept.Record,
	}
	last := kept.Made.GetCreateTime().AsTime()
	for _, event := range kept.Events {
		r.apply(event)
		last = event.GetTime().AsTime()
	}
	if r.state.GetState() == buildv1.Build_STATE_RUNNING {
		r.finish(last, exitInternal, failureOf(errors.New("the daemon ended while the run was under way"), codes.Internal), false)
	} else {
		// A record may say the run ended without the event that ends
		// it: whoever waits on it is told it has ended all the same.
		r.over()
		r.lost = r.record.Close()
	}
	return r
}

// lingers is how long a run that has ended is kept whatever has ended
// since: whoever made it asks how it ended a moment after it has, and
// is answered, however many runs a daemon ends in that moment.
const lingers = 5 * time.Minute

// clock is the time, as the daemon is told it.
func (rs *builds) clock() time.Time {
	if rs.now != nil {
		return rs.now()
	}
	return time.Now()
}

// prune lets go of the oldest runs that have ended, beyond the newest
// the daemon keeps and those that have only just ended. A run that has
// not ended is never let go.
func (rs *builds) prune() {
	now := rs.clock
	rs.mu.Lock()
	defer rs.mu.Unlock()
	ended := 0
	for _, r := range rs.order {
		if r.ended() {
			ended++
		}
	}
	kept := rs.order[:0]
	for _, r := range rs.order {
		state := r.snapshot()
		// Finished state and end time must be from the same observation.
		// A run may finish after this snapshot; a second read of endedness
		// would then treat the snapshot's absent end time as ancient.
		if ended > endedKept && state.GetState() != buildv1.Build_STATE_RUNNING && now().Sub(state.GetEndTime().AsTime()) > lingers {
			ended--
			delete(rs.byID, state.GetId())
			// What cannot be removed now is removed by the daemon that
			// finds it next.
			_ = rs.history.Drop(state.GetId())
			continue
		}
		kept = append(kept, r)
	}
	rs.order = kept
}

// errNoProfile is a number that is no profile: proto3 enums are open,
// and a client may send any.
var errNoProfile = errors.New("the number asked for is no profile: the profiles are PROFILE_DEVELOPMENT and PROFILE_PRODUCTION")

// profileOf returns the profile a run builds with: the one asked for,
// or development where none is.
func profileOf(asked typev1.Profile) (typev1.Profile, error) {
	switch asked {
	case typev1.Profile_PROFILE_UNSPECIFIED:
		return typev1.Profile_PROFILE_DEVELOPMENT, nil
	case typev1.Profile_PROFILE_DEVELOPMENT, typev1.Profile_PROFILE_PRODUCTION:
		return asked, nil
	}
	return 0, errNoProfile
}

// versions is what a release tells the build it makes: the version
// each entity it releases is released at, by the entity, and the commit
// each repository is planned at, by the repository.
type versions struct {
	units   map[string]pluginhost.Version
	commits map[string]string
}

// versioned are a run's actions, each versioned one given its version
// (Action.versioned): what the release plans for the entity that owns
// it, at the commit its repository is planned at, where the run is a
// release's and the release moves that entity; and what every build
// that is no release gives, anywhere else. The actions as evaluated are
// left as they were: an evaluation is kept, and is of no release.
func versioned(actions []plan.Action, entities []record.Located, of *versions) []plan.Action {
	out := actions
	for i, action := range actions {
		if !action.Declared.GetVersioned() {
			continue
		}
		version := pluginhost.Development
		if owner, owned := graph.Owner(entities, action.Label.Path); owned && of != nil {
			if planned, moves := of.units[owner.Ref.String()]; moves {
				version = planned
				version.Commit = of.commits[action.Repository]
			}
		}
		if &out[0] == &actions[0] {
			out = slices.Clone(actions)
		}
		out[i].Declared = pluginhost.FillVersion(action.Declared, version)
	}
	return out
}

// create makes a run, starts it and returns it at once. A run given
// where to bring a program is made for a run of that program: it builds
// what the one program its target names is made of, and brings that to
// where the daemon is; every other run is given nil. A run given
// versions is made for a release.
func (rs *builds) create(parent context.Context, profile typev1.Profile, targets, only []string, fix, testsOnly bool, running *delivery, released *versions) (*run, error) {
	id, err := newID(history.IDPrefix, history.IDBytes)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(rs.telemetry.Context(context.WithoutCancel(parent)))
	r := &run{
		clock:     rs.clock,
		base:      profile,
		running:   running,
		released:  released,
		only:      only,
		testsOnly: testsOnly,
		fix:       fix,
		tests:     map[string]bool{},
		cancel:    cancel,
		changed:   make(chan struct{}),
		done:      make(chan struct{}),
		state: &buildv1.Build{
			Id:         id,
			Profile:    profile,
			Targets:    targets,
			State:      buildv1.Build_STATE_RUNNING,
			CreateTime: timestamppb.New(rs.clock()),
			Counts:     &buildv1.Counts{},
			Cost:       &buildv1.Cost{},
		},
		results: map[string]*buildv1.ActionResult{},
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.stopped {
		cancel()
		return nil, errStopped
	}
	record, err := rs.history.Begin(r.state)
	if err != nil {
		cancel()
		return nil, err
	}
	r.record = record
	rs.byID[r.state.GetId()] = r
	rs.order = append(rs.order, r)
	rs.going.Go(func() { rs.execute(ctx, r) })
	return r, nil
}

func (rs *builds) get(id string) (*run, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r, ok := rs.byID[id]
	if !ok {
		return nil, errNotFound
	}
	return r, nil
}

// list returns the runs, newest first.
func (rs *builds) list() []*run {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]*run, 0, len(rs.order))
	for i := len(rs.order) - 1; i >= 0; i-- {
		out = append(out, rs.order[i])
	}
	return out
}

// execute is a run, from evaluation to its last event. Its span has
// ended before its last event is out, so that whoever saw the run end
// finds the whole trace.
func (rs *builds) execute(ctx context.Context, r *run) {
	ctx, span := telemetry.Pipeline(ctx, "build", r.state.GetId())
	started := time.Now()
	code, err, cancelled := rs.work(ctx, r)
	fallback := codes.Internal
	if code == exitBuildFailure || code == exitNoTests {
		fallback = codes.FailedPrecondition
	}
	failure := failureOf(err, fallback)
	result := semconv.CICDPipelineResultSuccess
	errorType := ""
	severity := log.SeverityInfo
	switch {
	case cancelled:
		result = semconv.CICDPipelineResultCancellation
	case code == exitInternal:
		result = semconv.CICDPipelineResultError
		errorType = bazelName(code)
	case code != exitSuccess:
		result = semconv.CICDPipelineResultFailure
		errorType = bazelName(code)
	}
	if errorType != "" {
		severity = log.SeverityError
	}
	telemetry.Result(span, result, errorType, failure.GetMessage())
	telemetry.PipelineDuration(ctx, "build", time.Since(started), result, errorType)
	telemetry.Message(ctx, severity, "run finished", result)
	span.End()
	r.finish(rs.clock(), code, failure, cancelled)
	rs.prune()
}

// work does what a run does and returns how it ended, why where it
// ended before anything ran, and whether it was cancelled.
func (rs *builds) work(ctx context.Context, r *run) (code buildv1.ExitCode, failure error, cancelled bool) {
	r.stage(buildv1.Build_STAGE_EVALUATING)
	rs.answered.Begin()
	tree, entities, err := sourceOf(rs.workspace)
	if err != nil {
		return exitInternal, err, false
	}
	// The run is of the files as they are now. Everything it does reads
	// them through this, and a file that is not what it was fails
	// whatever reads it.
	_, taking := telemetry.Start(ctx, "snapshot")
	snapshot, err := rs.files.Take(ctx, tree)
	telemetry.End(taking, err)
	// Said once of each repository, in the daemon's log and to
	// telemetry: a person who finds every file read after a restart
	// finds why.
	for _, unkept := range rs.files.Unkept() {
		telemetry.Message(ctx, log.SeverityWarn, unkept.String())
		if rs.said != nil {
			fmt.Fprintln(rs.said, "frontseat:", unkept)
		}
	}
	if err != nil {
		return exitInternal, err, ctx.Err() != nil
	}
	// What a repository or an entity says of how it is built, what its
	// actions need and what the run was pointed at are their authors' to
	// get right: a mistake in any fails the build before anything runs,
	// as a mistake in the source does.
	var needed, asked []plan.Action
	if r.running == nil {
		var selected evaluate.BuildPlan
		selected, err = rs.evaluation.Build(ctx, snapshot, r.base, entities, r.state.GetTargets(), r.only)
		if err == nil {
			needed, asked, entities = selected.Needed, selected.Asked, selected.Entities
			r.owned(entities)
			if r.released != nil {
				r.mu.Lock()
				r.publishes = &publishing{snapshot: snapshot, actions: selected.Publishes, entities: entities}
				r.mu.Unlock()
			}
		}
		// A build of tests keeps what its plugins say runs tests, and
		// is refused where there is none, as Bazel refuses one.
		if err == nil && r.testsOnly {
			needed, asked, err = plan.Tests(needed, asked, r.state.GetTargets())
		}
	} else {
		evaluating, evaluation := telemetry.Start(ctx, "evaluate")
		var evaluated evaluate.Evaluation
		evaluated, err = evaluate.Evaluate(evaluating, snapshot, rs.plugins, rs.answers, r.base, entities)
		evaluation.SetAttributes(attribute.Int("asked", evaluated.Asked), attribute.Int("again", evaluated.Again), attribute.Int("read", snapshot.Hashed()))
		telemetry.End(evaluation, err)
		if err == nil {
			entities = evaluated.Entities
			r.owned(entities)
			_, planning := telemetry.Start(ctx, "plan")
			var run plan.Action
			needed, run, err = plan.Program(evaluated.Actions, r.state.GetTargets()[0])
			asked = []plan.Action{run}
			telemetry.End(planning, err)
		}
	}
	read := snapshot.Hashed()
	var mistake *model.Mistake
	var none *plan.NoTests
	switch {
	case errors.As(err, &none):
		return exitNoTests, err, false
	case errors.As(err, &mistake):
		return exitBuildFailure, err, false
	case err != nil:
		return exitInternal, err, ctx.Err() != nil
	}
	needed = versioned(needed, entities, r.released)
	r.stage(buildv1.Build_STAGE_EXECUTING)
	executing, execution := telemetry.Start(ctx, "execute", attribute.Int("actions", len(needed)))
	defer func() {
		execution.SetAttributes(attribute.Int("read", snapshot.Hashed()-read))
		var failed error
		if code != buildv1.ExitCode_EXIT_CODE_UNSPECIFIED && code != exitSuccess {
			failed = fmt.Errorf("%s", bazelName(code))
		}
		telemetry.End(execution, failed)
	}()
	runner := runexec.Runner{Executor: rs.executor, Answers: rs.answered, Downloads: rs.downloads, Platform: rs.platform, Fix: r.fix, Kept: rs.tools}
	// What a release's build packages is what the release ships.
	if r.released != nil {
		runner.Packaged = func(action plan.Action, left map[string]exec.Held) { r.packaged(action, left) }
	}
	if r.running != nil {
		runner.Deliver = func(ctx context.Context, action plan.Action, left map[string]exec.Held) error {
			brought, err := program.Bringer{Grid: rs.answered, Kept: rs.tools, Platform: rs.platform}.Bring(ctx, snapshot.Paths[action.Repository], action, left)
			if err != nil {
				return err
			}
			r.running.bring(brought)
			return nil
		}
	}
	err = runner.Run(executing, snapshot, needed, runexec.Events{
		Planned:   func() { r.planned(needed, asked) },
		Restarted: r.startOver,
		Started: func(action plan.Action) {
			r.append(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionStarted_{ActionStarted: &buildv1.BuildEvent_ActionStarted{Label: action.Label.String()}}})
		},
		Finished: r.finished,
		Entity:   r.owner,
		Print:    r.print,
		Patched: func(repository string, paths []string) {
			r.append(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_Patched_{Patched: &buildv1.BuildEvent_Patched{Repository: repository, Paths: paths}}})
		},
		Report: func(ctx context.Context, action plan.Action, exitCode int, stdout, stderr []byte) ([]*typev1.Diagnostic, error) {
			return evaluate.Report(ctx, snapshot, rs.plugins, rs.answers, action, exitCode, stdout, stderr)
		},
	})
	if err != nil {
		return exitInternal, err, ctx.Err() != nil
	}
	return r.exitCode(), nil, ctx.Err() != nil
}

// run is one run: its state, its events and what its actions did.
type run struct {
	cancel context.CancelFunc
	// clock is the daemon's clock, which stamps the run's events; nil,
	// they are stamped by the system's.
	clock func() time.Time
	// base is the way the source is built: which of the two the run's
	// profile is.
	base typev1.Profile
	// fix is whether what is out of step is written, and not failed.
	fix bool
	// released is what a release tells the build it makes; nil, the run
	// is of no release.
	released *versions
	// artifacts are what the packages of a release's build left, each
	// with the entity that owns the action that left it.
	artifacts []artifact
	// running is what a run's build brings its program to; nil, the
	// run is a build.
	running *delivery
	// entities are the entities that own what the run builds.
	entities []record.Located

	mu      sync.Mutex
	state   *buildv1.Build
	events  []*buildv1.BuildEvent
	labels  []string
	results map[string]*buildv1.ActionResult
	// only are the names of the actions the run keeps; none is all.
	only []string
	// testsOnly keeps, of what the run was asked, the actions that run
	// tests and what they need.
	testsOnly bool
	// tests are the labels of the actions that are tests, whose failure
	// is a test that failed and not a build that broke.
	tests map[string]bool
	// publishes is what a release's build leaves it to publish with.
	publishes *publishing
	// changed is closed, and replaced, each time an event is appended;
	// done is closed when the run has ended.
	changed chan struct{}
	done    chan struct{}
	// record is the run's place in the daemon's history: each event is
	// added to it as it happens. lost is the first thing that could not
	// be kept there; a run that lost something does not end well.
	record runRecord
	lost   error
}

// owned tells the run which entities own what it builds.
func (r *run) owned(entities []record.Located) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entities = entities
}

// artifact is what a package left, where it sits in the workspace, and
// the entity that owns the action that left it; the entity is empty
// where none owns it. A package leaves files: a directory is none, and
// is kept to be said.
type artifact struct {
	owner, at string
	directory bool
	held      exec.Held
	file      *buildv1.ReleaseArtifact
}

// publishing is what a release's build leaves for the release to
// publish with: the files as the build read them, and the actions of
// the workspace that publish.
type publishing struct {
	snapshot *source.Snapshot
	actions  []plan.Action
	entities []record.Located
}

// packaged notes what a package left: each file, by its own name, with
// what the grid holds of it.
func (r *run) packaged(action plan.Action, left map[string]exec.Held) {
	r.mu.Lock()
	defer r.mu.Unlock()
	owner := ""
	if owned, is := graph.Owner(r.entities, action.Label.Path); is {
		owner = owned.Ref.String()
	}
	for at, held := range left {
		r.artifacts = append(r.artifacts, artifact{owner: owner, at: at, directory: held.Directory, held: held, file: &buildv1.ReleaseArtifact{
			Name: path.Base(at), Digest: &buildv1.Digest{Hash: held.Digest.Hash, SizeBytes: held.Digest.Size}, Label: action.Label.String(),
		}})
	}
}

// published is what the run leaves a release to publish with; nil
// where it planned nothing.
func (r *run) published() *publishing {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.publishes
}

// shipped are what the run's packages left, by the entity that owns
// each.
func (r *run) shipped() map[string][]artifact {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string][]artifact{}
	for _, each := range r.artifacts {
		out[each.owner] = append(out[each.owner], each)
	}
	return out
}

// owner returns the entity that owns what is at a path of the workspace;
// nil where none does.
func (r *run) owner(at string) *typev1.EntityRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, owned := graph.Owner(r.entities, at)
	if !owned {
		return nil
	}
	return entity.Proto(owner.Ref)
}

// snapshot returns the run as it is now.
func (r *run) snapshot() *buildv1.Build {
	r.mu.Lock()
	defer r.mu.Unlock()
	return proto.Clone(r.state).(*buildv1.Build)
}

// ended reports whether the run has ended.
func (r *run) ended() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state.GetState() != buildv1.Build_STATE_RUNNING
}

// since returns the events from a sequence on, whether the run has ended,
// and a channel that is closed when there is more.
func (r *run) since(sequence int64) ([]*buildv1.BuildEvent, bool, <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sequence < 1 {
		sequence = 1
	}
	var out []*buildv1.BuildEvent
	if int(sequence) <= len(r.events) {
		out = append(out, r.events[sequence-1:]...)
	}
	return out, r.state.GetState() != buildv1.Build_STATE_RUNNING, r.changed
}

// wait returns true once the run has ended, and false once stop is
// closed before it has. A run that has ended is one that ended, whatever
// stop says; a nil stop waits for the end alone.
func (r *run) wait(stop <-chan struct{}) bool {
	select {
	case <-r.done:
		return true
	default:
	}
	select {
	case <-r.done:
		return true
	case <-stop:
		return false
	}
}

// actions returns what the run's actions did, in the order it planned
// them.
func (r *run) actions() []*buildv1.ActionResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*buildv1.ActionResult, 0, len(r.labels))
	for _, label := range r.labels {
		if result, ok := r.results[label]; ok {
			out = append(out, result)
		} else {
			out = append(out, &buildv1.ActionResult{Label: label})
		}
	}
	return out
}

// append records an event under the next sequence number and wakes
// whoever follows the run. The caller holds no lock.
func (r *run) append(e *buildv1.BuildEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appendLocked(e)
}

// appendLocked gives an event its place among the run's events, makes
// the run what it says, adds it to the run's record and wakes whoever
// follows the run.
func (r *run) appendLocked(e *buildv1.BuildEvent) {
	e.Sequence = int64(len(r.events) + 1)
	at := time.Now
	if r.clock != nil {
		at = r.clock
	}
	e.Time = timestamppb.New(at())
	r.apply(e)
	if r.lost == nil {
		r.lost = r.record.Event(e)
	}
	if e.GetFinished() != nil {
		if err := r.record.Close(); r.lost == nil {
			r.lost = err
		}
	}
	close(r.changed)
	r.changed = make(chan struct{})
}

// apply makes the run what an event says. A run is its events: what one
// says as it happens is what the daemon after this one reads from the
// run's record, and nothing is said of a run that no event says.
func (r *run) apply(e *buildv1.BuildEvent) {
	r.events = append(r.events, e)
	switch event := e.GetEvent().(type) {
	case *buildv1.BuildEvent_StageChanged_:
		r.state.Stage = event.StageChanged.GetStage()
	case *buildv1.BuildEvent_Planned_:
		r.labels = nil
		for _, action := range event.Planned.GetActions() {
			r.labels = append(r.labels, action.GetLabel())
		}
		r.state.Counts.Total = int32(len(r.labels))
	case *buildv1.BuildEvent_StartedOver_:
		// What the run cost its workers so far it has cost them, and
		// stays counted.
		r.labels = nil
		r.results = map[string]*buildv1.ActionResult{}
		// What the packages of the run before left is not this run's.
		r.artifacts = nil
		r.state.Counts = &buildv1.Counts{}
	case *buildv1.BuildEvent_ActionFinished_:
		result := event.ActionFinished.GetResult()
		r.results[result.GetLabel()] = result
		switch result.GetOutcome() {
		case buildv1.Outcome_OUTCOME_PASSED:
			r.state.Counts.Passed++
		case buildv1.Outcome_OUTCOME_FAILED:
			r.state.Counts.Failed++
		case buildv1.Outcome_OUTCOME_REWRITTEN:
			r.state.Counts.Rewritten++
		case buildv1.Outcome_OUTCOME_ERRORED:
			r.state.Counts.Errored++
		case buildv1.Outcome_OUTCOME_SKIPPED:
			r.state.Counts.Skipped++
		case buildv1.Outcome_OUTCOME_IDLE:
			r.state.Counts.Idle++
		}
		if result.GetProvenance() == buildv1.Provenance_PROVENANCE_EXECUTED {
			r.state.Cost.ActionsExecuted++
			r.state.Cost.WorkerTime = durationpb.New(r.state.Cost.GetWorkerTime().AsDuration() + result.GetWorkerTime().AsDuration())
		}
	case *buildv1.BuildEvent_Finished_:
		r.state = proto.Clone(event.Finished.GetBuild()).(*buildv1.Build)
		if r.state.GetState() != buildv1.Build_STATE_RUNNING {
			r.over()
		}
		return
	}
	r.state.LastSequence = e.GetSequence()
}

// over tells whoever waits on the run that it has ended, once: a record
// read back may hold more than one event that ends its run.
func (r *run) over() {
	select {
	case <-r.done:
	default:
		close(r.done)
	}
}

// print keeps what an action printed in the run's record and returns
// its digest.
func (r *run) print(content []byte) *buildv1.Digest {
	digest, err := r.record.Print(content)
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.lost == nil {
			r.lost = err
		}
	}
	return digest
}

// printed opens what an action of the run printed, by its digest.
func (r *run) printed(digest *buildv1.Digest) (io.ReadCloser, error) {
	return r.record.Printed(digest)
}

func (r *run) stage(stage buildv1.Build_Stage) {
	r.append(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_StageChanged_{StageChanged: &buildv1.BuildEvent_StageChanged{Stage: stage}}})
}

// planned records what a run will do: its actions, each after what it
// needs, and which of them were asked for and not brought by another.
func (r *run) planned(actions, asked []plan.Action) {
	r.mu.Lock()
	defer r.mu.Unlock()
	planned := &buildv1.BuildEvent_Planned{}
	for _, a := range actions {
		label := a.Label.String()
		action := &buildv1.BuildEvent_PlannedAction{Label: label, Name: a.Name()}
		if owner, owned := graph.Owner(r.entities, a.Label.Path); owned && !a.IsShared() {
			action.Entity = entity.Proto(owner.Ref)
		}
		for _, need := range a.Needs {
			action.Needs = append(action.Needs, need.String())
		}
		planned.Actions = append(planned.Actions, action)
		if a.IsTest() {
			r.tests[label] = true
		}
	}
	for _, a := range asked {
		planned.Roots = append(planned.Roots, a.Label.String())
	}
	r.appendLocked(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_Planned_{Planned: planned}})
}

// startOver forgets what the run's actions did and says the run starts
// over: for the action that was handed what the grid no longer held,
// and with what the grid said.
func (r *run) startOver(label, reason string) {
	r.append(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_StartedOver_{StartedOver: &buildv1.BuildEvent_StartedOver{Label: label, Reason: reason}}})
}

func (r *run) finished(result *buildv1.ActionResult) {
	r.append(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: result}}})
}

// exitCode is how a run that ran to its end ended.
func (r *run) exitCode() buildv1.ExitCode {
	r.mu.Lock()
	defer r.mu.Unlock()
	code := exitSuccess
	for _, result := range r.results {
		switch {
		case result.GetOutcome() == buildv1.Outcome_OUTCOME_PASSED:
		// An action with nothing to do is nothing that went wrong.
		case result.GetOutcome() == buildv1.Outcome_OUTCOME_IDLE:
		// An action that was not run is not what went wrong: what
		// stopped it is.
		case result.GetOutcome() == buildv1.Outcome_OUTCOME_SKIPPED:
		// A test that was never handed to a grid is no test that failed.
		case result.GetOutcome() == buildv1.Outcome_OUTCOME_FAILED && r.tests[result.GetLabel()] && result.GetProvenance() != buildv1.Provenance_PROVENANCE_UNSPECIFIED:
			if code == exitSuccess {
				code = exitTestsFailed
			}
		default:
			code = exitBuildFailure
		}
	}
	return code
}

// finish ends the run: at a time, with how it ended and why. A run
// whose record could not be kept does not end well, whatever its
// actions did: what is asked of it afterwards could not be answered.
func (r *run) finish(at time.Time, code buildv1.ExitCode, failure *statuspb.Status, cancelled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Every event before the last is in the record by now, or the run
	// is told it is not.
	if r.lost == nil && r.record != nil {
		r.lost = r.record.Written()
	}
	final := proto.Clone(r.state).(*buildv1.Build)
	switch {
	case cancelled:
		code, failure = exitInterrupted, nil
	case r.lost != nil:
		code, failure = exitInternal, failureOf(fmt.Errorf("the run's record could not be kept: %w", r.lost), codes.Internal)
	}
	final.State = endedAs(code)
	final.Stage = buildv1.Build_STAGE_UNSPECIFIED
	final.ExitCode = code
	final.Failure, final.Diagnostics = explained(failure)
	final.EndTime = timestamppb.New(at)
	final.Cost.WallTime = durationpb.New(at.Sub(final.GetCreateTime().AsTime()))
	// What a cancelled run never reached is counted as such.
	final.Counts.Unreached = final.Counts.Total - int32(len(r.results))
	// The last event carries the run as it ended, and is counted in it.
	final.LastSequence = int64(len(r.events) + 1)
	r.appendLocked(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: final}}})
}

// endedAs is the state of a build that ended with a code.
func endedAs(code buildv1.ExitCode) buildv1.Build_State {
	switch code {
	case exitSuccess:
		return buildv1.Build_STATE_SUCCEEDED
	case exitInterrupted:
		return buildv1.Build_STATE_CANCELLED
	}
	return buildv1.Build_STATE_FAILED
}

// newID returns a new id: a prefix, and so many random bytes in
// lowercase hex.
func newID(prefix string, size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

// runRecord is the durable record a live run appends to. Its storage details
// belong to history; the run decides the meaning and ordering of events.
type runRecord interface {
	Event(*buildv1.BuildEvent) error
	Written() error
	Close() error
	Print([]byte) (*buildv1.Digest, error)
	Printed(*buildv1.Digest) (io.ReadCloser, error)
}
