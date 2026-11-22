package build

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec/program"
)

// runsKept is how many runs that have ended a daemon remembers,
// the newest; one that has not ended is always remembered.
const runsKept = 12

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

// running is where a run's build brings its program: the run is told
// to build what the program is made of, and leaves the program here for
// the run to start.
type delivery struct {
	mu      sync.Mutex
	brought *program.Brought
}

// bring keeps a program that was brought. A run that started over
// brings it again: what was brought before is removed.
func (l *delivery) bring(brought *program.Brought) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.brought != nil {
		l.brought.Remove()
	}
	l.brought = brought
}

// take returns the program that was brought, which is then whoever
// took it's to start or remove; nil where none was.
func (l *delivery) take() *program.Brought {
	l.mu.Lock()
	defer l.mu.Unlock()
	brought := l.brought
	l.brought = nil
	return brought
}

// runs are the runs a daemon has made. A run builds what a
// program is made of, by a run, and starts the program where the daemon
// is. What a daemon remembers of them it remembers while it runs: a
// program ends with the daemon that started it.
type programRuns struct {
	runs *runs

	mu       sync.Mutex
	made     []*programRun
	stopping bool
	// joined waits for every run to have ended, when the daemon does.
	joined sync.WaitGroup
}

// run is one run, as it is now.
type programRun struct {
	mu    sync.Mutex
	state *buildv1.Run
	// lines are the newest lines its program printed, the first of them
	// being line first; changed is closed, and replaced, each time one
	// is added and when the run ends.
	lines   []*typev1.LogLine
	first   int64
	bytes   int
	changed chan struct{}
	// stop is closed to end the run, and ended when it has ended.
	stop    chan struct{}
	stopped sync.Once
	ended   chan struct{}
}

func (l *programRun) snapshot() *buildv1.Run {
	l.mu.Lock()
	defer l.mu.Unlock()
	return proto.Clone(l.state).(*buildv1.Run)
}

func (l *programRun) change(change func(*buildv1.Run)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	change(l.state)
}

func (l *programRun) over() bool {
	select {
	case <-l.ended:
		return true
	default:
		return false
	}
}

// end asks the run to end. It returns at once; ended says when.
func (l *programRun) end() { l.stopped.Do(func() { close(l.stop) }) }

// printed keeps a line the program printed. What of it is not UTF-8,
// which a message's text has to be, is given as U+FFFD.
func (l *programRun) printed(stderr bool, line []byte, at *timestamppb.Timestamp) {
	stream := typev1.LogStream_LOG_STREAM_STDOUT
	if stderr {
		stream = typev1.LogStream_LOG_STREAM_STDERR
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := &typev1.LogLine{Time: at, Stream: stream, Text: strings.ToValidUTF8(string(line), "\uFFFD")}
	l.lines, l.bytes = append(l.lines, kept), l.bytes+sent(kept)
	// The oldest are let go, one at a time, from the front of what is
	// kept: what is behind it is let go with the next growth.
	for len(l.lines) > 1 && (len(l.lines) > linesKept || l.bytes > bytesKept) {
		l.bytes -= sent(l.lines[0])
		l.lines[0] = nil
		l.lines = l.lines[1:]
		l.first++
	}
	close(l.changed)
	l.changed = make(chan struct{})
}

// since returns the lines kept from a line on, the line after the last
// of them, whether the run has ended, and what is closed when there
// is more to say. A line the daemon keeps no more is answered from the
// first it keeps.
func (l *programRun) since(line int64) (lines []*typev1.LogLine, next int64, ended bool, changed <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if line < l.first {
		line = l.first
	}
	if at := line - l.first; at < int64(len(l.lines)) {
		lines = append(lines, l.lines[at:]...)
	}
	return lines, line + int64(len(lines)), l.over(), l.changed
}

func newProgramRuns(runs *runs) *programRuns { return &programRuns{runs: runs} }

// stop ends every run, and waits for each to have ended: a program
// ends with its daemon. It is called before the runs are stopped: a
// run waits on a run, and its program is in the runs' directory.
func (ls *programRuns) stop() {
	ls.mu.Lock()
	ls.stopping = true
	made := append([]*programRun(nil), ls.made...)
	ls.mu.Unlock()
	for _, each := range made {
		each.end()
	}
	ls.joined.Wait()
}

func (ls *programRuns) get(id string) (*programRun, error) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	for _, each := range ls.made {
		if each.state.GetId() == id {
			return each, nil
		}
	}
	return nil, errRunNotFound
}

// list returns the runs, newest first.
func (ls *programRuns) list() []*programRun {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	out := make([]*programRun, 0, len(ls.made))
	for i := len(ls.made) - 1; i >= 0; i-- {
		out = append(out, ls.made[i])
	}
	return out
}

// create makes a run, and returns at once. What it does is of the
// trace of whoever asked, and goes on after they have been answered.
func (ls *programRuns) create(ctx context.Context, profile string, base pluginv1.Profile, request *buildv1.CreateRunRequest) (*programRun, error) {
	id, err := newRunID()
	if err != nil {
		return nil, err
	}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.stopping {
		return nil, errStopped
	}
	going := 0
	for _, each := range ls.made {
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
	built, err := ls.runs.create(context.WithoutCancel(ctx), profile, base, []string{request.GetTarget()}, nil, false, false, bringing, nil)
	if err != nil {
		return nil, err
	}
	l := &programRun{
		changed: make(chan struct{}), stop: make(chan struct{}), ended: make(chan struct{}),
		state: &buildv1.Run{
			Id: id, Target: request.GetTarget(), Arguments: request.GetArguments(), Profile: profile,
			State: buildv1.Run_STATE_BUILDING, CreateTime: timestamppb.New(ls.runs.clock()),
			BuildId: built.snapshot().GetId(),
		},
	}
	ls.made = append(ls.made, l)
	ls.forget()
	ls.joined.Add(1)
	go func() {
		defer ls.joined.Done()
		ls.carryOut(context.WithoutCancel(ctx), l, built, bringing)
	}()
	return l, nil
}

// forget lets go of runs that have ended beyond the newest kept.
// It is called with the lock held.
func (ls *programRuns) forget() {
	over := 0
	for _, each := range ls.made {
		if each.over() {
			over++
		}
	}
	kept := ls.made[:0]
	for _, each := range ls.made {
		if each.over() && over > runsKept {
			over--
			continue
		}
		kept = append(kept, each)
	}
	ls.made = kept
}

// carryOut builds what a run's program is made of, starts it, and
// stays with it until it ends or the run is stopped.
func (ls *programRuns) carryOut(ctx context.Context, l *programRun, built *run, bringing *delivery) {
	end := func(state buildv1.Run_State, failure string) {
		now := timestamppb.New(ls.runs.clock())
		l.mu.Lock()
		l.state.State, l.state.Failure, l.state.EndTime = state, failure, now
		close(l.ended)
		close(l.changed)
		l.changed = make(chan struct{})
		l.mu.Unlock()
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
	for {
		_, ended, changed := built.since(1 << 62)
		if ended {
			break
		}
		select {
		case <-changed:
		case <-l.stop:
			// The build is cancelled and waited for: nothing of it goes
			// on, and nothing it brings is left, after the run ended.
			built.cancel()
			for {
				_, ended, changed := built.since(1 << 62)
				if ended {
					break
				}
				<-changed
			}
			end(buildv1.Run_STATE_CANCELLED, "")
			return
		}
	}
	ran := built.snapshot()
	switch {
	case ran.GetExitCode().GetCode() != 0 && ran.GetFailure() != "":
		fail("%s", ran.GetFailure())
		return
	case ran.GetExitCode().GetCode() != 0:
		fail("the build %s of what it is made of ended %s%s", ran.GetId(), ran.GetExitCode().GetName(), stoppedBy(built.actions()))
		return
	}
	brought := bringing.take()
	if brought == nil {
		fail("the build %s ended without its program", ran.GetId())
		return
	}
	select {
	case <-l.stop:
		brought.Remove()
		end(buildv1.Run_STATE_CANCELLED, "")
		return
	default:
	}
	l.change(func(s *buildv1.Run) { s.Label = brought.Label })
	running, err := brought.Start(l.snapshot().GetArguments(), func(stderr bool, line []byte) {
		l.printed(stderr, line, timestamppb.New(ls.runs.clock()))
	})
	if err != nil {
		fail("%v", err)
		return
	}
	started := timestamppb.New(ls.runs.clock())
	l.change(func(s *buildv1.Run) {
		s.State, s.StartTime = buildv1.Run_STATE_RUNNING, started
	})
	state, failure := buildv1.Run_STATE_EXITED, ""
	select {
	case <-running.Ended():
	case <-l.stop:
		// A program that is stopped has ended; what could not be done
		// of stopping it is said.
		if err := running.Stop(); err != nil {
			failure = err.Error()
		}
		state = buildv1.Run_STATE_CANCELLED
	}
	code := int32(running.ExitCode())
	l.change(func(s *buildv1.Run) { s.ExitCode = code })
	end(state, failure)
}

// stoppedBy says which action stopped a run's build, and what it
// said: the first that did not pass. A run says why it failed, and
// not only that its build did.
func stoppedBy(actions []*buildv1.ActionResult) string {
	for _, action := range actions {
		switch action.GetOutcome() {
		case buildv1.Outcome_OUTCOME_FAILED, buildv1.Outcome_OUTCOME_ERROR, buildv1.Outcome_OUTCOME_REWRITE:
			if action.GetMessage() == "" {
				return fmt.Sprintf(": %s failed", action.GetLabel())
			}
			return fmt.Sprintf(": %s: %s", action.GetLabel(), action.GetMessage())
		}
	}
	return ""
}

func newRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "run_" + hex.EncodeToString(b[:]), nil
}
