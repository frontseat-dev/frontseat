// Package run composes action scheduling, staging and tool provisioning.
// The build service owns run records; Events is how execution reports to it.
package run

import (
	"context"
	"errors"
	"fmt"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/download"
	"github.com/frontseat-dev/frontseat/server/internal/exec/fix"
	"github.com/frontseat-dev/frontseat/server/internal/exec/schedule"
	"github.com/frontseat-dev/frontseat/server/internal/exec/stage"
	"github.com/frontseat-dev/frontseat/server/internal/exec/tools"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"path"
	"slices"
	"sort"
	"sync"
	"time"
)

// owner is the entity that owns an action: none for a shared one, which
// belongs to no project.
func (e Events) owner(action plan.Action) *typev1.EntityRef {
	if action.IsShared() {
		return nil
	}
	return e.Entity(action.Label.Path)
}

// Events records execution without depending on the build service's storage.
// Every callback is required, and may be called concurrently by actions.
type Events struct {
	Planned   func()
	Restarted func(label, reason string)
	Started   func(plan.Action)
	Finished  func(*buildv1.ActionResult)
	Entity    func(string) *typev1.EntityRef
	Print     func([]byte) *buildv1.Digest
	Patched   func(repository string, paths []string)
	// Report asks a check's plugin what the check found, from how it
	// ended and what it printed. Nil, no check has findings.
	Report func(ctx context.Context, action plan.Action, exitCode int, stdout, stderr []byte) ([]*typev1.Diagnostic, error)
}

// Runner owns the execution services shared across a daemon's runs.
type Runner struct {
	Executor exec.Executor
	Answers  *exec.Remembering
	// Downloads brings what a shared fetch downloads to the grid, through
	// the asset service. Nil, a daemon given no asset service: a fetch
	// that downloads fails, saying so.
	Downloads download.Fetcher
	Platform  map[string]string
	// Fix says the run fixes: each check's fix runs where the checkout
	// is, and what it changes is written; the catalog is kept in step.
	Fix bool
	// Kept is the directory the daemon keeps tool trees in, for fixes,
	// for as long as it runs.
	Kept string
	// Run is told of the program a run's build ends with, once all
	// it reads has been built: the action, and what the actions it needs
	// left, by the path each sits at in the action's root. It brings
	// that to where the daemon is. Nil, a run that is a build: it plans
	// no program, and one it is handed all the same is not run.
	Deliver func(ctx context.Context, action plan.Action, left map[string]exec.Held) error
}

// Run executes the plan, retrying once if the grid has discarded inputs.
// It returns an error where the run could not begin: what its checks
// may change could not be read.
func (runner Runner) Run(ctx context.Context, snapshot *source.Snapshot, needed []plan.Action, events Events) error {
	for again := false; ; again = true {
		events.Planned()
		// Where its tools are is asked of the grid by this run, and held
		// for as long as it runs: the next run asks again, the grid being
		// free to let a tool go in between.
		held := &tools.Provisioner{Executor: runner.Executor, Platform: runner.Platform}
		// A grid is free to let go of what it kept while the run is under
		// way, too: a tool, or what one action left for another. The
		// action handed it was not run. The run stops there and starts
		// over, asking the grid again where everything is; the grid makes
		// again what it let go. It does so once.
		var let *gone
		if !again {
			let = &gone{}
		}
		// A run that fixes writes what its checks propose: two checks
		// that may change one file are refused before either runs.
		var conflicts map[string]string
		if runner.Fix {
			var err error
			if conflicts, err = conflicting(ctx, snapshot, needed); err != nil {
				return err
			}
		}
		attempt, stop := context.WithCancel(ctx)
		fixer := fix.Runner{Grid: runner.Answers, Tools: held, Kept: runner.Kept}
		schedule.All(attempt, needed, atOnce, running{runner: runner, events: events, source: snapshot, tools: held, gone: let, stop: stop, conflicts: conflicts, fixer: fixer})
		stop()
		label, reason, went := let.first()
		if !went || ctx.Err() != nil {
			break
		}
		// Nothing that was kept of what the grid answered is gone by
		// again: any of it may name what the grid let go, whatever it was
		// that found out.
		runner.Answers.Forget()
		if runner.Downloads != nil {
			runner.Downloads.Forget()
		}
		events.Restarted(label, reason)
	}
	return nil
}

// gone is what a grid let go of while a run was under way: the first
// action that was handed something the grid no longer held, and what
// the grid said. A nil one is a run that does not start over.
type gone struct {
	mu     sync.Mutex
	label  string
	reason string
}

// note records an action that was handed what the grid let go, and
// reports whether the run starts over for it.
func (g *gone) note(label string, err error) bool {
	if g == nil || !errors.Is(err, exec.ErrNotKept) {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.label == "" {
		g.label, g.reason = label, err.Error()
	}
	return true
}

// first returns the first action noted, with what the grid said.
func (g *gone) first() (label, reason string, went bool) {
	if g == nil {
		return "", "", false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.label, g.reason, g.label != ""
}

// atOnce is how many actions of a run are with the grid at once. The
// daemon does none of their work: it asks, and the grid decides how many
// it runs and has the rest wait their turn. So it is as many as Buck2
// has with a grid at once (400, its `execution_concurrency_limit`), and
// not as many as the daemon's machine has processors, which is what
// Bazel takes for a build it runs itself.
const atOnce = 400

// running is a run's actions being run: what the schedule asks to run
// each, and tells of each that is not run.
type running struct {
	runner Runner
	events Events
	source *source.Snapshot
	tools  *tools.Provisioner
	// gone is told of an action handed what the grid let go; stop then
	// ends what the run has under way, which starts over.
	gone *gone
	stop context.CancelFunc
	// conflicts says, by label, what is wrong with an action that is
	// refused before it runs: it may change a file another action may.
	conflicts map[string]string
	// fixer runs a check's fix where the checkout is.
	fixer fix.Runner
}

var _ schedule.Runner = running{}

// Run implements schedule.Runner.
func (ing running) Run(ctx context.Context, action plan.Action, handed []schedule.Handed) schedule.Ending {
	if refused, conflicting := ing.conflicts[action.Label.String()]; conflicting {
		ing.events.Started(action)
		ing.events.Finished(&buildv1.ActionResult{
			Label:   action.Label.String(),
			Entity:  ing.events.owner(action),
			Name:    action.Name(),
			Outcome: buildv1.Outcome_OUTCOME_ERROR,
			Message: refused,
		})
		return schedule.Ending{Outcome: schedule.Errored}
	}
	// An action with nothing to do is not run, and fails nothing: what
	// waits for it goes on.
	if missing, idle := nothingToDo(ing.source.Tree, action, handed); idle {
		ing.events.Finished(&buildv1.ActionResult{
			Label:   action.Label.String(),
			Entity:  ing.events.owner(action),
			Name:    action.Name(),
			Outcome: buildv1.Outcome_OUTCOME_IDLE,
			Message: "not run: " + missing + ", and it has nothing to do without it",
		})
		return schedule.Ending{Outcome: schedule.Passed}
	}
	outcome, left, err := ing.runner.run(ctx, ing.events, ing.source, ing.tools, ing.fixer, action, handed)
	if ing.gone.note(action.Label.String(), err) {
		ing.stop()
	}
	return schedule.Ending{Outcome: ended[outcome], Left: left, Err: err}
}

// nothingToDo reports whether an action that says it has nothing to do
// without what it reads (Action.idle_without_inputs) was handed less
// than its plugin named one by one (plan.Action.IdleWithout), and what
// was not left: one output of an action it names, which that action
// passed without leaving. What a catalog file added to what it reads
// is no part of this: a test is not skipped for a fixture that was not
// left, and is still skipped where its own program was not.
func nothingToDo(tree source.Tree, action plan.Action, handed []schedule.Handed) (missing string, idle bool) {
	if !action.Declared.GetIdleWithoutInputs() {
		return "", false
	}
	for _, read := range action.IdleWithout {
		// An action that passed and left nothing at all is not among
		// what is handed: what is read of it was not left either.
		left := false
		for _, each := range handed {
			if each.From == read.From {
				_, left = each.Left[stage.Place(tree.Paths[read.Repository], read.ProjectRoot, read.Output)]
				break
			}
		}
		if !left {
			return read.From.String() + " left no " + read.Output, true
		}
	}
	return "", false
}

// Skip implements schedule.Runner. An action that was not run is on
// record as skipped, and says which action stopped it.
func (ing running) Skip(action plan.Action, need model.Label, how schedule.Outcome) {
	ing.events.Finished(&buildv1.ActionResult{
		Label:     action.Label.String(),
		Entity:    ing.events.owner(action),
		Name:      action.Name(),
		Outcome:   buildv1.Outcome_OUTCOME_SKIPPED,
		BlockedBy: need.String(),
		Message:   "not run: " + need.String() + " " + stopped[how],
	})
}

// ended is how an action ended, as what needs it sees it. One that is
// out of step did what it does, and left what it leaves: what needs it
// runs, and the run fails all the same.
var ended = map[buildv1.Outcome]schedule.Outcome{
	buildv1.Outcome_OUTCOME_PASSED:  schedule.Passed,
	buildv1.Outcome_OUTCOME_FAILED:  schedule.Failed,
	buildv1.Outcome_OUTCOME_REWRITE: schedule.Passed,
	buildv1.Outcome_OUTCOME_ERROR:   schedule.Errored,
}

// stopped says, of an action that did not pass, what it did instead, to
// whatever needed it.
var stopped = map[schedule.Outcome]string{
	schedule.Failed:  "failed",
	schedule.Errored: "could not be run",
	schedule.Skipped: "was not run",
}

// selected is what an action reads of what the actions it needs left:
// all that an action it named left, and of one it named an output of,
// that output alone, where the other left it. An output that was not
// left hands nothing. What is handed by an action the reader named
// nothing of is left as it is: a generated-source binding selects it.
//
// It runs before every action is staged, on every build, so what is
// handed on as it was left is handed on as it is: nothing is made anew
// for an action that reads all of what each of its needs left, or the
// one thing it left.
func selected(tree source.Tree, action plan.Action, handed []schedule.Handed) []schedule.Handed {
	if len(action.Reads) == 0 || len(handed) == 0 {
		return handed
	}
	// A few reads are looked through for each thing handed. Many, as a
	// module's of every fetch of its lock, are looked up: whether an
	// action was named, and whole; and which outputs of it were.
	var whole map[model.Label]bool
	var parts map[model.Label][]int
	if len(action.Reads) > lookedThrough {
		whole = make(map[model.Label]bool, len(action.Reads))
		for i, read := range action.Reads {
			if read.All() {
				whole[read.From] = true
				continue
			}
			whole[read.From] = whole[read.From]
			if parts == nil {
				parts = map[model.Label][]int{}
			}
			parts[read.From] = append(parts[read.From], i)
		}
	}
	// out stays nil for as long as everything handed is passed on as it
	// is, and is made at the first thing that is not.
	var out []schedule.Handed
	for n, each := range handed {
		named, all := false, false
		if whole != nil {
			all, named = whole[each.From]
		} else {
			for _, read := range action.Reads {
				if read.From == each.From {
					named, all = true, all || read.All()
				}
			}
		}
		if named && !all {
			// How many of the outputs named are there. The reads of one
			// action name different places (plan.Resolve), so where as
			// many are there as it left, all it left was named.
			there := 0
			each := each
			forOutputs(action, parts, each.From, func(read plan.Read) {
				if _, left := each.Left[stage.Place(tree.Paths[read.Repository], read.ProjectRoot, read.Output)]; left {
					there++
				}
			})
			if there != len(each.Left) {
				if out == nil {
					out = append(make([]schedule.Handed, 0, len(handed)), handed[:n]...)
				}
				if there > 0 {
					kept := make(map[string]exec.Held, there)
					forOutputs(action, parts, each.From, func(read plan.Read) {
						at := stage.Place(tree.Paths[read.Repository], read.ProjectRoot, read.Output)
						if held, left := each.Left[at]; left {
							kept[at] = held
						}
					})
					out = append(out, schedule.Handed{From: each.From, Left: kept})
				}
				continue
			}
		}
		if out != nil {
			out = append(out, each)
		}
	}
	if out == nil {
		return handed
	}
	return out
}

// lookedThrough is how many reads of an action are looked through for
// each thing it is handed, before they are looked up instead.
const lookedThrough = 8

// forOutputs calls do with each read of one output of an action: found
// by looking it up, where the reads were indexed, and by looking
// through them otherwise.
func forOutputs(action plan.Action, parts map[model.Label][]int, from model.Label, do func(plan.Read)) {
	if parts != nil {
		for _, i := range parts[from] {
			do(action.Reads[i])
		}
		return
	}
	for _, read := range action.Reads {
		if read.From == from && !read.All() {
			do(read)
		}
	}
}

// together is what the actions an action needs left, each thing by where
// it sits. What several left in one place, as modules leave their
// programs in one directory, is put together by the grid. It says, as
// clash, what cannot be: which two actions left what, and where.
func (runner Runner) together(ctx context.Context, handed []schedule.Handed) (together map[string]exec.Held, clash string, err error) {
	together = map[string]exec.Held{}
	type sitting struct {
		all  []exec.Held
		from []model.Label
	}
	var several map[string]*sitting
	origin := map[string]model.Label{}
	for _, each := range handed {
		for at, held := range each.Left {
			there, both := together[at]
			if !both {
				together[at], origin[at] = held, each.From
				continue
			}
			if several == nil {
				several = map[string]*sitting{}
			}
			place := several[at]
			if place == nil {
				place = &sitting{all: []exec.Held{there}, from: []model.Label{origin[at]}}
				several[at] = place
			}
			if !slices.Contains(place.all, held) {
				place.all, place.from = append(place.all, held), append(place.from, each.From)
			}
		}
	}
	places := make([]string, 0, len(several))
	for at, place := range several {
		if len(place.all) > 1 {
			places = append(places, at)
		}
	}
	sort.Strings(places)
	for _, at := range places {
		place := several[at]
		merged, err := runner.Answers.Merge(ctx, place.all)
		var differ *exec.Clash
		if errors.As(err, &differ) {
			return nil, fmt.Sprintf("%s and %s left different things at %s, and one would replace the other", place.from[differ.First], place.from[differ.Second], path.Join(at, differ.Path)), nil
		}
		if err != nil {
			return nil, "", fmt.Errorf("what %s and %s left at %s: %w", place.from[0], place.from[1], at, err)
		}
		together[at] = merged
	}
	return together, "", nil
}

// run executes one action of a run, with what the actions it needs left,
// and records what it did. It returns how it ended, what it left, by
// where each thing sits in the workspace, and why it could not be run
// where it could not.
func (runner Runner) run(ctx context.Context, events Events, snapshot *source.Snapshot, held *tools.Provisioner, fixer fix.Runner, action plan.Action, handed []schedule.Handed) (buildv1.Outcome, map[string]exec.Held, error) {
	label := action.Label.String()
	events.Started(action)
	span := trace.SpanFromContext(ctx)
	if action.InStep != nil {
		return runner.inStep(events, snapshot, action), nil, nil
	}

	started := time.Now()
	// A program is no action of the grid: what it reads is brought to
	// where the daemon is, for whoever run it to start there.
	if action.IsProgram() {
		return runner.deliver(ctx, events, snapshot, action, handed, started)
	}
	// A run that fixes runs the check's fix first, where the checkout
	// is, and writes what it changed: the check then runs over that.
	var proposals []proposal
	if runner.Fix && action.IsCheck() && action.Declared.GetFix() != nil {
		changes, err := fixer.Run(ctx, snapshot, action)
		if err == nil {
			proposals = record(events, action, changes)
			var wrote []string
			wrote, err = apply(events, snapshot, action, proposals)
			if len(wrote) > 0 {
				events.Patched(action.Repository, wrote)
			}
		}
		if err != nil {
			result := &buildv1.ActionResult{
				Label: label, Entity: events.owner(action), Name: action.Name(),
				Provenance: buildv1.Provenance_PROVENANCE_DAEMON,
				Outcome:    buildv1.Outcome_OUTCOME_ERROR, Message: err.Error(),
				Duration: durationpb.New(time.Since(started)),
			}
			for _, each := range proposals {
				result.Rewrites = append(result.Rewrites, each.recorded)
			}
			events.Finished(result)
			return result.GetOutcome(), nil, nil
		}
	}
	var done exec.Result
	// answered is whether the grid answered: with what a worker did for
	// this run, or with what it kept. An action that could not be staged,
	// or that the grid did not answer, has no result from anywhere.
	answered := false
	allHanded := handed
	if action.Beside != nil {
		handed = forNeeds(handed, action.Beside.TestNeeds)
	}
	handed, err := runner.generated(ctx, snapshot.Tree, action, handed)
	var needs map[string]exec.Held
	var clash string
	if err == nil {
		needs, clash, err = runner.together(ctx, handed)
	}
	if err == nil && clash == "" {
		var staged exec.Action
		if staged, err = stage.Of(ctx, snapshot, action, held, runner.Downloads, needs); err == nil {
			if staged, err = runner.serviceInputs(ctx, snapshot, held, action, allHanded, staged); err == nil {
				done, err = runner.Answers.Execute(ctx, staged)
				answered = err == nil
			}
		}
	}
	// An action that passed left what it says it writes, or it did not
	// pass: whatever needs it would otherwise run without it.
	var left map[string]exec.Held
	missing := ""
	if err == nil && clash == "" && done.ExitCode == 0 {
		left, missing, err = stage.Left(snapshot.Tree, action, done.Outputs)
	}
	// What a producer generated is what the checkout's copies of it
	// should say: a copy out of step is a rewrite, which a run asked to
	// fix writes, and which otherwise fails the run and says so.
	var drifted []proposal
	if err == nil && missing == "" && len(action.GeneratedSources) > 0 {
		var changes []source.Change
		if changes, err = runner.drift(ctx, snapshot, action, left); err == nil && len(changes) > 0 {
			drifted = record(events, action, changes)
			if runner.Fix {
				var wrote []string
				wrote, err = apply(events, snapshot, action, drifted)
				if len(wrote) > 0 {
					events.Patched(action.Repository, wrote)
				}
			}
		}
	}
	// What a check found is its plugin's to say, from what it printed.
	var diagnostics []*typev1.Diagnostic
	if err == nil && clash == "" && answered && action.IsCheck() && events.Report != nil {
		diagnostics, err = events.Report(ctx, action, done.ExitCode, done.Stdout, done.Stderr)
	}
	provenance := buildv1.Provenance_PROVENANCE_UNSPECIFIED
	switch {
	case !answered:
	case done.Cached:
		provenance = buildv1.Provenance_PROVENANCE_GRID_CACHE
	default:
		provenance = buildv1.Provenance_PROVENANCE_EXECUTED
	}

	result := &buildv1.ActionResult{
		Label:        label,
		Entity:       events.owner(action),
		Name:         action.Name(),
		Provenance:   provenance,
		ExitCode:     int32(done.ExitCode),
		Duration:     durationpb.New(time.Since(started)),
		Stdout:       events.Print(done.Stdout),
		ActionDigest: &buildv1.Digest{Hash: done.Action.Hash, SizeBytes: done.Action.Size},
		Stderr:       events.Print(done.Stderr),
		Diagnostics:  diagnostics,
	}
	for _, each := range proposals {
		result.Rewrites = append(result.Rewrites, each.recorded)
	}
	for _, each := range drifted {
		result.Rewrites = append(result.Rewrites, each.recorded)
	}
	// How long it worked is the worker's to say, too.
	if provenance == buildv1.Provenance_PROVENANCE_EXECUTED {
		result.WorkerTime = durationpb.New(done.Worked)
	}
	// When it started is the worker's to say; one answered from a cache
	// started nowhere.
	if !done.Cached && !done.Started.IsZero() {
		result.StartTime = timestamppb.New(done.Started)
	}
	switch {
	case err != nil:
		result.Outcome, result.Message = buildv1.Outcome_OUTCOME_ERROR, err.Error()
	case clash != "":
		result.Outcome, result.Message = buildv1.Outcome_OUTCOME_FAILED, clash
	case done.ExitCode != 0:
		result.Outcome = buildv1.Outcome_OUTCOME_FAILED
	case len(diagnostics) > 0:
		// A check that found something did not pass, whatever it exited
		// with: gofmt -l exits 0 and lists what it would change.
		result.Outcome, result.Message = buildv1.Outcome_OUTCOME_FAILED, "it found what it reports"
		if action.Declared.GetFix() != nil && !runner.Fix {
			result.Message += ": frontseat build --fix puts it right"
		}
	case missing != "":
		result.Outcome, result.Message = buildv1.Outcome_OUTCOME_FAILED, "it left no "+missing+", which it says it writes"
	case len(drifted) > 0 && !runner.Fix:
		result.Outcome, result.Message = buildv1.Outcome_OUTCOME_REWRITE, changed(action, drifted)+": not what it generated, which the checkout's copy should say: frontseat build --fix writes it"
	case len(drifted) > 0:
		result.Outcome, result.Message = buildv1.Outcome_OUTCOME_PASSED, "wrote "+changed(action, drifted)
	case len(proposals) > 0:
		result.Outcome, result.Message = buildv1.Outcome_OUTCOME_PASSED, "wrote "+changed(action, proposals)
	default:
		result.Outcome = buildv1.Outcome_OUTCOME_PASSED
	}
	span.SetAttributes(attribute.Bool("frontseat.action.cached", done.Cached))
	telemetry.Output(ctx, "stdout", done.Stdout, done.Cached)
	telemetry.Output(ctx, "stderr", done.Stderr, done.Cached)

	events.Finished(result)
	return result.GetOutcome(), left, err
}

// run ends a run's build: what the program reads of what other
// actions left is put together and handed to whoever runs it. The
// program itself is not run here, and nothing of it is the grid's.
func (runner Runner) deliver(ctx context.Context, events Events, snapshot *source.Snapshot, action plan.Action, handed []schedule.Handed, started time.Time) (buildv1.Outcome, map[string]exec.Held, error) {
	result := &buildv1.ActionResult{
		Label: action.Label.String(), Entity: events.owner(action), Name: action.Name(),
		Provenance: buildv1.Provenance_PROVENANCE_DAEMON,
	}
	var err error
	var clash string
	if runner.Deliver == nil {
		err = fmt.Errorf("%s is a program, which no build runs", action.Label)
	}
	var needs map[string]exec.Held
	if err == nil {
		needs, clash, err = runner.together(ctx, selected(snapshot.Tree, action, handed))
	}
	if err == nil && clash == "" {
		err = runner.Deliver(ctx, action, needs)
	}
	switch {
	case err != nil:
		result.Outcome, result.Message = buildv1.Outcome_OUTCOME_ERROR, err.Error()
	case clash != "":
		result.Outcome, result.Message = buildv1.Outcome_OUTCOME_FAILED, clash
	default:
		result.Outcome, result.Message = buildv1.Outcome_OUTCOME_PASSED, "brought to where the daemon is"
	}
	result.Duration = durationpb.New(time.Since(started))
	events.Finished(result)
	return result.GetOutcome(), nil, err
}
