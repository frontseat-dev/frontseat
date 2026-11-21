package build

import runexec "github.com/frontseat-dev/frontseat/server/internal/exec/run"

import "github.com/frontseat-dev/frontseat/server/internal/source/record"

import "github.com/frontseat-dev/frontseat/sdk/go/entity"

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/history"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/evaluate"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/download"
	"github.com/frontseat-dev/frontseat/server/internal/exec/program"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	sourced "github.com/frontseat-dev/frontseat/server/internal/source"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// How a run ends, in Bazel's names and numbers.
var (
	exitSuccess      = &buildv1.ExitCode{Name: "SUCCESS", Code: 0}
	exitBuildFailure = &buildv1.ExitCode{Name: "BUILD_FAILURE", Code: 1}
	exitTestsFailed  = &buildv1.ExitCode{Name: "TESTS_FAILED", Code: 3}
	// exitNoTests is Bazel's for a test of what holds no tests.
	exitNoTests     = &buildv1.ExitCode{Name: "NO_TESTS_FOUND", Code: 4}
	exitInterrupted = &buildv1.ExitCode{Name: "INTERRUPTED", Code: 8}
	exitInternal    = &buildv1.ExitCode{Name: "INTERNAL_ERROR", Code: 37}
)

// errNotFound is a run that does not exist.
var errNotFound = errors.New("no build has that id")

// runs is a daemon's runs. A run belongs to the daemon, not to the call
// that made it: it goes on when its caller goes away.
type runs struct {
	workspace *sourced.Workspace
	plugins   []*pluginhost.Plugin
	// tools is where a fix's work is kept: tool trees by digest, and copies.
	tools    string
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
	history history.Store
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

// errStopped is a run asked of a daemon that has ended.
var errStopped = errors.New("the daemon is ending, and makes no run")

// stop ends the daemon's runs: each is cancelled, and stop returns once
// all have ended.
func (rs *runs) stop() {
	rs.mu.Lock()
	rs.stopped = true
	made := append([]*run(nil), rs.order...)
	rs.mu.Unlock()
	for _, r := range made {
		r.cancel()
	}
	rs.going.Wait()
	rs.history.Release()
	os.RemoveAll(rs.tools)
}

// newRuns returns a daemon's runs: those kept in a directory by the
// daemons before it, and those it will make. A run a daemon died under
// is ended here, and says so.
func newRuns(held *sourced.Workspace, plugins []*pluginhost.Plugin, executor exec.Executor, downloads download.Fetcher, platform map[string]string, signals *telemetry.Signals, kept string) (*runs, error) {
	keptHistory, err := history.Open(kept)
	if err != nil {
		return nil, err
	}
	if err := keptHistory.Hold(); err != nil {
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
	rs := &runs{workspace: held, plugins: plugins, executor: executor, downloads: downloads, answered: &exec.Remembering{Executor: executor}, platform: platform, telemetry: signals, history: keptHistory, files: &source.Keeper{}, answers: &evaluate.Answers{}, byID: map[string]*run{}, tools: tools}
	before, err := keptHistory.All()
	if err != nil {
		keptHistory.Release()
		return nil, err
	}
	for _, each := range before {
		// A run is known by the name its record is kept under, which is
		// what its record was read by.
		id := each.Made.GetId()
		r := recorded(each)
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
func recorded(kept history.Read) *run {
	r := &run{
		cancel:  func() {},
		changed: make(chan struct{}),
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
		r.lost = r.record.Close()
	}
	return r
}

// lingers is how long a run that has ended is kept whatever has ended
// since: whoever made it asks how it ended a moment after it has, and
// is answered, however many runs a daemon ends in that moment.
const lingers = 5 * time.Minute

// clock is the time, as the daemon is told it.
func (rs *runs) clock() time.Time {
	if rs.now != nil {
		return rs.now()
	}
	return time.Now()
}

// prune lets go of the oldest runs that have ended, beyond the newest
// the daemon keeps and those that have only just ended. A run that has
// not ended is never let go.
func (rs *runs) prune() {
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
		if ended > newest && state.GetState() != buildv1.Build_STATE_RUNNING && now().Sub(state.GetEndTime().AsTime()) > lingers {
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

// create starts a run and returns it at once.
// profiles are the profiles there are, by name, each with the way of
// building it is at its base. Frontseat defines these two; a profile a
// repository defines for itself is found here once there are any.
var profiles = map[string]pluginv1.Profile{
	"development": pluginv1.Profile_PROFILE_DEVELOPMENT,
	"production":  pluginv1.Profile_PROFILE_PRODUCTION,
}

// errNoProfile is a profile nobody defined.
var errNoProfile = errors.New("the profiles are development and production")

// profileOf returns the profile a run builds with, by name, and what it
// is at its base: the one asked for, or development where none is.
func profileOf(asked string) (string, pluginv1.Profile, error) {
	if asked == "" {
		asked = "development"
	}
	base, defined := profiles[asked]
	if !defined {
		return "", 0, fmt.Errorf("there is no profile %s: %w", asked, errNoProfile)
	}
	return asked, base, nil
}

// create makes a run and starts it. A run given something to run is
// a run's: it builds what the one program its target names is made
// of, and brings that to where the daemon is; every other run is a
// build, and is given nil.
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

func (rs *runs) create(parent context.Context, profile string, base pluginv1.Profile, targets, only []string, fix, testsOnly bool, running *delivery, released *versions) (*run, error) {
	ctx, cancel := context.WithCancel(rs.telemetry.Context(context.WithoutCancel(parent)))
	r := &run{
		base:      base,
		running:   running,
		released:  released,
		only:      only,
		testsOnly: testsOnly,
		fix:       fix,
		tests:     map[string]bool{},
		cancel:    cancel,
		changed:   make(chan struct{}),
		state: &buildv1.Build{
			Id:         newID(),
			Profile:    profile,
			Targets:    targets,
			State:      buildv1.Build_STATE_RUNNING,
			CreateTime: timestamppb.Now(),
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

func (rs *runs) get(id string) (*run, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	r, ok := rs.byID[id]
	if !ok {
		return nil, errNotFound
	}
	return r, nil
}

// list returns the runs, newest first.
func (rs *runs) list() []*run {
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
func (rs *runs) execute(ctx context.Context, r *run) {
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
		errorType = code.GetName()
	case code.GetCode() != 0:
		result = semconv.CICDPipelineResultFailure
		errorType = code.GetName()
	}
	if errorType != "" {
		severity = log.SeverityError
	}
	telemetry.Result(span, result, errorType, failure.GetMessage())
	telemetry.PipelineDuration(ctx, "build", time.Since(started), result, errorType)
	telemetry.Message(ctx, severity, "run finished", result)
	span.End()
	r.finish(time.Now(), code, failure, cancelled)
	rs.prune()
}

// work does what a run does and returns how it ended, why where it
// ended before anything ran, and whether it was cancelled.
func (rs *runs) work(ctx context.Context, r *run) (code *buildv1.ExitCode, failure error, cancelled bool) {
	r.stage(buildv1.Build_STAGE_EVALUATING)
	rs.answered.Begin()
	tree, err := sourceOf(rs.workspace)
	if err != nil {
		return exitInternal, err, false
	}
	// The run is of the files as they are now. Everything it does reads
	// them through this, and a file that is not what it was fails
	// whatever reads it.
	_, taking := telemetry.Start(ctx, "snapshot")
	snapshot, err := rs.files.Take(ctx, tree)
	telemetry.End(taking, err)
	if err != nil {
		return exitInternal, err, ctx.Err() != nil
	}
	// What a repository or an entity says of how it is built, what its
	// actions need and what the run was pointed at are their authors' to
	// get right: a mistake in any fails the build before anything runs,
	// as a mistake in the source does.
	entities := rs.workspace.Entities()
	var needed, asked []plan.Action
	if r.running == nil {
		var selected evaluate.BuildPlan
		selected, err = rs.evaluation.Build(ctx, snapshot, r.base, entities, r.state.GetTargets(), r.only)
		if err == nil {
			needed, asked, entities = selected.Needed, selected.Asked, selected.Entities
			r.owned(entities)
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
		if code != nil && code.GetCode() != 0 {
			failed = fmt.Errorf("%s", code.GetName())
		}
		telemetry.End(execution, failed)
	}()
	runner := runexec.Runner{Executor: rs.executor, Answers: rs.answered, Downloads: rs.downloads, Platform: rs.platform, Fix: r.fix, Kept: rs.tools}
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
	// base is the way the source is built: which of the two the run's
	// profile is.
	base pluginv1.Profile
	// fix is whether what is out of step is written, and not failed.
	fix bool
	// released is what a release tells the build it makes; nil, the run
	// is of no release.
	released *versions
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
	// changed is closed, and replaced, each time an event is appended.
	changed chan struct{}
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
	e.Time = timestamppb.Now()
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
		r.state.Counts = &buildv1.Counts{}
	case *buildv1.BuildEvent_ActionFinished_:
		result := event.ActionFinished.GetResult()
		r.results[result.GetLabel()] = result
		switch result.GetOutcome() {
		case buildv1.Outcome_OUTCOME_PASSED:
			r.state.Counts.Passed++
		case buildv1.Outcome_OUTCOME_FAILED, buildv1.Outcome_OUTCOME_REWRITE:
			r.state.Counts.Failed++
		case buildv1.Outcome_OUTCOME_ERROR:
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
		return
	}
	r.state.LastSequence = e.GetSequence()
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
func (r *run) exitCode() *buildv1.ExitCode {
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
func (r *run) finish(at time.Time, code *buildv1.ExitCode, failure *statuspb.Status, cancelled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Every event before the last is in the record by now, or the run
	// is told it is not.
	if r.lost == nil && r.record != nil {
		r.lost = r.record.Written()
	}
	final := proto.Clone(r.state).(*buildv1.Build)
	final.State = buildv1.Build_STATE_FINISHED
	switch {
	case cancelled:
		final.State, code, failure = buildv1.Build_STATE_CANCELLED, exitInterrupted, nil
	case r.lost != nil:
		code, failure = exitInternal, failureOf(fmt.Errorf("the run's record could not be kept: %w", r.lost), codes.Internal)
	}
	final.Stage = buildv1.Build_STAGE_UNSPECIFIED
	final.ExitCode = code
	final.Failure, final.Diagnostics = explained(failure)
	final.EndTime = timestamppb.New(at)
	final.Cost.WallTime = durationpb.New(at.Sub(final.GetCreateTime().AsTime()))
	// What a cancelled run never reached did not run either.
	final.Counts.Skipped += final.Counts.Total - int32(len(r.results))
	// The last event carries the run as it ended, and is counted in it.
	final.LastSequence = int64(len(r.events) + 1)
	r.appendLocked(&buildv1.BuildEvent{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: final}}})
}

func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("server: no randomness: %v", err))
	}
	return "build_" + hex.EncodeToString(b[:])
}

// newest is the number of completed runs retained after the grace period.
const newest = 12

// runRecord is the durable record a live run appends to. Its storage details
// belong to history; the run decides the meaning and ordering of events.
type runRecord interface {
	Event(*buildv1.BuildEvent) error
	Written() error
	Close() error
	Print([]byte) (*buildv1.Digest, error)
	Printed(*buildv1.Digest) (io.ReadCloser, error)
}
