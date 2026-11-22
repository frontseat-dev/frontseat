package build

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/protobuf/types/known/timestamppb"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec/program"
)

// runsAtOnce is how many runs a daemon has that have not ended:
// each is a program of its sandbox, and what it printed.
const runsAtOnce = 32

// errTooManyRuns is one run more than that.
var errTooManyRuns = fmt.Errorf("the daemon has %d runs that have not ended: cancel one first (frontseat run cancel)", runsAtOnce)

// linesKept is how many lines of what a program printed a daemon keeps,
// the newest, and bytesKept how much they may come to: a program may
// print for as long as it runs, in lines of any length.
const (
	linesKept = 10000
	bytesKept = 8 << 20
)

// errRunNotFound is what asking for a run no daemon made, or one
// it remembers no more, fails with.
var errRunNotFound = errors.New("no run has that id")

// delivery is where a run's build brings its program: the build makes
// what the program is made of, and leaves the program here for the run
// to start.
type delivery struct {
	mu      sync.Mutex
	brought *program.Brought
}

// bring keeps a program that was brought. A run that started over
// brings it again: what was brought before is removed.
func (d *delivery) bring(brought *program.Brought) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.brought != nil {
		d.brought.Remove()
	}
	d.brought = brought
}

// take returns the program that was brought, which is then whoever
// took it's to start or remove; nil where none was.
func (d *delivery) take() *program.Brought {
	d.mu.Lock()
	defer d.mu.Unlock()
	brought := d.brought
	d.brought = nil
	return brought
}

// runs are the runs a daemon has made, of the run service. A run
// builds what a program is made of, by a build, and starts the program
// where the daemon is. What a daemon remembers of them it remembers
// while it runs: a program ends with the daemon that started it.
type runs struct {
	// runs are the daemon's builds, one of which each run is made with.
	builds *builds

	mu       sync.Mutex
	made     []*programRun
	stopping bool
	// joined waits for every run to have ended, when the daemon does.
	joined sync.WaitGroup
}

// programRun is one run of a program, as it is now.
type programRun struct {
	lasting[*buildv1.Run]
	// lines are the newest lines its program printed, the first of them
	// being line first; changed is closed, and replaced, each time one
	// is added and when the run ends.
	lines   []*typev1.LogLine
	first   int64
	bytes   int
	changed chan struct{}
	// stop is closed to end the run.
	stop    chan struct{}
	stopped sync.Once
}

// end asks the run to end. It returns at once; ended says when.
func (r *programRun) end() { r.stopped.Do(func() { close(r.stop) }) }

// printed keeps a line the program printed. What of it is not UTF-8,
// which a message's text has to be, is given as U+FFFD.
func (r *programRun) printed(stderr bool, line []byte, at *timestamppb.Timestamp) {
	stream := typev1.LogStream_LOG_STREAM_STDOUT
	if stderr {
		stream = typev1.LogStream_LOG_STREAM_STDERR
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := &typev1.LogLine{Time: at, Stream: stream, Text: strings.ToValidUTF8(string(line), "\uFFFD")}
	r.lines, r.bytes = append(r.lines, kept), r.bytes+sent(kept)
	// The oldest are let go, one at a time, from the front of what is
	// kept: what is behind it is let go with the next growth.
	for len(r.lines) > 1 && (len(r.lines) > linesKept || r.bytes > bytesKept) {
		r.bytes -= sent(r.lines[0])
		r.lines[0] = nil
		r.lines = r.lines[1:]
		r.first++
	}
	close(r.changed)
	r.changed = make(chan struct{})
}

// since returns the lines kept from a line on, the line after the last
// of them, whether the run has ended, and what is closed when there
// is more to say. A line the daemon keeps no more is answered from the
// first it keeps.
func (r *programRun) since(line int64) (lines []*typev1.LogLine, next int64, ended bool, changed <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if line < r.first {
		line = r.first
	}
	if at := line - r.first; at < int64(len(r.lines)) {
		lines = append(lines, r.lines[at:]...)
	}
	return lines, line + int64(len(lines)), r.over(), r.changed
}

func newRuns(builds *builds) *runs { return &runs{builds: builds} }

// stop ends every run, and waits for each to have ended: a program
// ends with its daemon. It is called before the builds are stopped: a
// run waits on its build, and its program is in the builds' directory.
func (rs *runs) stop() {
	rs.mu.Lock()
	rs.stopping = true
	made := append([]*programRun(nil), rs.made...)
	rs.mu.Unlock()
	for _, each := range made {
		each.end()
	}
	rs.joined.Wait()
}

func (rs *runs) get(id string) (*programRun, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, each := range rs.made {
		if each.state.GetId() == id {
			return each, nil
		}
	}
	return nil, errRunNotFound
}

// list returns the runs, newest first.
func (rs *runs) list() []*programRun {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]*programRun, 0, len(rs.made))
	for i := len(rs.made) - 1; i >= 0; i-- {
		out = append(out, rs.made[i])
	}
	return out
}

// create makes a run, and returns at once. What it does is of the
// trace of whoever asked, and goes on after they have been answered.
func (rs *runs) create(ctx context.Context, profile typev1.Profile, request *buildv1.CreateRunRequest) (*programRun, error) {
	id, err := newID("run_", 8)
	if err != nil {
		return nil, err
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.stopping {
		return nil, errStopped
	}
	going := 0
	for _, each := range rs.made {
		if !each.over() {
			going++
		}
	}
	if going >= runsAtOnce {
		return nil, errTooManyRuns
	}
	// Its build is made with it, so that whoever made it follows the
	// build by the run it is answered with.
	bringing := &delivery{}
	built, err := rs.builds.create(context.WithoutCancel(ctx), profile, []string{request.GetTarget()}, nil, false, false, bringing, nil)
	if err != nil {
		return nil, err
	}
	r := &programRun{
		changed: make(chan struct{}), stop: make(chan struct{}),
		lasting: lasting[*buildv1.Run]{ended: make(chan struct{}), state: &buildv1.Run{
			Id: id, Target: request.GetTarget(), Arguments: request.GetArguments(), Profile: profile,
			State: buildv1.Run_STATE_BUILDING, CreateTime: timestamppb.New(rs.builds.clock()),
			BuildId: built.snapshot().GetId(),
		}},
	}
	rs.made = remembered(append(rs.made, r))
	rs.joined.Add(1)
	go func() {
		defer rs.joined.Done()
		rs.carryOut(context.WithoutCancel(ctx), r, built, bringing)
	}()
	return r, nil
}

// carryOut builds what a run's program is made of, starts it, and
// stays with it until it ends or the run is stopped.
func (rs *runs) carryOut(ctx context.Context, r *programRun, built *run, bringing *delivery) {
	end := func(state buildv1.Run_State, failure string) {
		now := timestamppb.New(rs.builds.clock())
		r.mu.Lock()
		r.state.State, r.state.Failure, r.state.EndTime = state, failure, now
		close(r.ended)
		close(r.changed)
		r.changed = make(chan struct{})
		r.mu.Unlock()
	}
	fail := func(format string, a ...any) {
		end(buildv1.Run_STATE_FAILED, fmt.Sprintf(format, a...))
	}
	// Whatever was brought and never started is removed, however the
	// run ends.
	defer func() {
		if brought := bringing.take(); brought != nil {
			brought.Remove()
		}
	}()
	if !built.wait(r.stop) {
		// The build is cancelled and waited for: nothing of it goes on,
		// and nothing it brings is left, after the run ended.
		built.cancel()
		built.wait(nil)
		end(buildv1.Run_STATE_CANCELLED, "")
		return
	}
	ran := built.snapshot()
	switch {
	case ran.GetExitCode() != exitSuccess && ran.GetFailure() != "":
		fail("%s", ran.GetFailure())
		return
	case ran.GetExitCode() != exitSuccess:
		fail("the build %s of what it is made of ended %s%s", ran.GetId(), bazelName(ran.GetExitCode()), stoppedBy(built.actions()))
		return
	}
	brought := bringing.take()
	if brought == nil {
		fail("the build %s ended without its program", ran.GetId())
		return
	}
	select {
	case <-r.stop:
		brought.Remove()
		end(buildv1.Run_STATE_CANCELLED, "")
		return
	default:
	}
	r.change(func(s *buildv1.Run) { s.Label = brought.Label })
	running, err := brought.Start(r.snapshot().GetArguments(), func(stderr bool, line []byte) {
		r.printed(stderr, line, timestamppb.New(rs.builds.clock()))
	})
	if err != nil {
		fail("%v", err)
		return
	}
	started := timestamppb.New(rs.builds.clock())
	r.change(func(s *buildv1.Run) {
		s.State, s.StartTime = buildv1.Run_STATE_RUNNING, started
	})
	state, failure := buildv1.Run_STATE_SUCCEEDED, ""
	select {
	case <-running.Ended():
	case <-r.stop:
		// A program that is stopped has ended; what could not be done
		// of stopping it is said.
		if err := running.Stop(); err != nil {
			failure = err.Error()
		}
		state = buildv1.Run_STATE_CANCELLED
	}
	code := int32(running.ExitCode())
	r.change(func(s *buildv1.Run) { s.ExitCode = code })
	end(state, failure)
}

// stoppedBy says which action stopped a run's build, and what it
// said: the first that did not pass. A run says why it failed, and
// not only that its build did.
func stoppedBy(actions []*buildv1.ActionResult) string {
	for _, action := range actions {
		switch action.GetOutcome() {
		case buildv1.Outcome_OUTCOME_FAILED, buildv1.Outcome_OUTCOME_ERRORED, buildv1.Outcome_OUTCOME_REWRITTEN:
			if action.GetMessage() == "" {
				return fmt.Sprintf(": %s failed", action.GetLabel())
			}
			return fmt.Sprintf(": %s: %s", action.GetLabel(), action.GetMessage())
		}
	}
	return ""
}
