package build

// What a daemon holds of its runs, from inside: each refusal has a
// control beside it that passes.

import (
	"context"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/history"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protodelim"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
)

// begun is a run under way, as create makes one, with its record in a
// directory.
func begun(t *testing.T, kept *history.Store, id string) *run {
	t.Helper()
	made := &buildv1.Build{Id: id, State: buildv1.Build_STATE_RUNNING, CreateTime: timestamppb.Now(), Counts: &buildv1.Counts{}, Cost: &buildv1.Cost{}}
	record, err := kept.Begin(made)
	if err != nil {
		t.Fatal(err)
	}
	return &run{cancel: func() {}, changed: make(chan struct{}), done: make(chan struct{}), state: made, results: map[string]*buildv1.ActionResult{}, tests: map[string]bool{}, record: record}
}

// opened is the history kept in a directory, held until the test ends.
func opened(t *testing.T, dir string) *history.Store {
	t.Helper()
	kept, err := history.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kept.Release)
	return kept
}

// readRuns is what a daemon that starts over a directory of runs, and
// is given nothing else, makes of them.
func readRuns(t *testing.T, dir string) *runs {
	t.Helper()
	rs, err := newRuns(nil, nil, nil, nil, nil, nil, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// endedRuns writes the record of each run numbered from first to last,
// whole, the run numbered i made at minute i.
func endedRuns(t *testing.T, dir string, first, last int) {
	t.Helper()
	for i := first; i <= last; i++ {
		id := fmt.Sprintf("build_%012x", i)
		crafted(t, dir, id, whole(id, i, id)...)
	}
}

// plannedOf is the event that says a run has so many actions, each
// needing three others, named as a repository of some depth names them.
func plannedOf(actions int) *buildv1.BuildEvent {
	planned := &buildv1.BuildEvent_Planned{}
	for i := range actions {
		label := fmt.Sprintf("//platform/services/billing/internal/module%05d:go.compile", i)
		planned.Actions = append(planned.Actions, &buildv1.BuildEvent_PlannedAction{
			Label: label, Name: "go.compile",
			Needs: []string{label + "-a", label + "-b", label + "-c"},
		})
	}
	return &buildv1.BuildEvent{Event: &buildv1.BuildEvent_Planned_{Planned: planned}}
}

// reloaded is what the daemon that comes after says of the one run kept.
func reloaded(t *testing.T, kept *history.Store) *run {
	t.Helper()
	all, err := kept.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("the next daemon finds %d runs, want 1", len(all))
	}
	return recorded(all[0], nil)
}

// A run of a large workspace cannot be read back: its
// "planned" event is one message, protodelim refuses to read a message
// over 4 MiB, the reader takes the refusal for a record cut short,
// truncates the record there and ends the run as one its daemon died
// under. The run had finished well.
func TestARunOfManyActionsOutlivesItsDaemon(t *testing.T) {
	for name, actions := range map[string]int{"control: 100 actions": 100, "20000 actions": 20000} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			kept := opened(t, dir)
			r := begun(t, kept, "build_0123456789ab")
			event := plannedOf(actions)
			r.append(event)
			t.Logf("the planned event is %d bytes", proto.Size(event))
			r.finish(time.Now(), exitSuccess, nil, false)
			if r.lost != nil {
				t.Fatalf("the record was not kept: %v", r.lost)
			}
			want := r.snapshot()
			before, _ := os.Stat(filepath.Join(dir, "build_0123456789ab", recordFile))

			got := reloaded(t, kept).snapshot()

			if !proto.Equal(got, want) {
				t.Errorf("the next daemon says the run is %s %s (%s), %d actions; the daemon that ran it said %s %s, %d actions", got.GetState(), got.GetExitCode().GetName(), got.GetFailure(), got.GetCounts().GetTotal(), want.GetState(), want.GetExitCode().GetName(), want.GetCounts().GetTotal())
			}
			after, _ := os.Stat(filepath.Join(dir, "build_0123456789ab", recordFile))
			if after.Size() < before.Size() {
				t.Errorf("reading the record cut it from %d bytes to %d", before.Size(), after.Size())
			}
		})
	}
}

// finish decides how the run ended before the events held in
// the buffer are written. A record that could not be written is found
// out only when the buffer is written out, which for a run of less than
// 64 KiB of events and a quarter of a second is at its end: the run ends
// SUCCESS though nothing of it was kept, against finish's own comment.
// The control is the same run with enough events to fill the buffer, so
// that the write fails while the run is under way.
func TestARunWhoseRecordCouldNotBeKeptDoesNotEndWell(t *testing.T) {
	for name, actions := range map[string]int{"control: events that fill the buffer": 2000, "events that stay in the buffer": 10} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			kept := opened(t, dir)
			r := begun(t, kept, "build_0123456789ab")
			// Storage reports a failed write, either when appending or when
			// flushing buffered events. History tests prove both disk failures.
			r.record = failedRecord{runRecord: r.record, duringAppend: actions == 2000}
			r.append(plannedOf(actions))
			r.stage(buildv1.Build_STAGE_EXECUTING)

			r.finish(time.Now(), exitSuccess, nil, false)

			ended := r.snapshot()
			if r.lost == nil {
				t.Fatal("the run lost nothing; the test's disk did not refuse")
			}
			if ended.GetExitCode().GetName() != "INTERNAL_ERROR" {
				t.Errorf("the run ended %s (%q) though its record could not be kept (%v); want INTERNAL_ERROR", ended.GetExitCode().GetName(), ended.GetFailure(), r.lost)
			}
		})
	}
}

// crafted writes a record as a file on the disk holds it.
func crafted(t *testing.T, dir, id string, messages ...proto.Message) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, id, printedDir), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, id, recordFile))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, message := range messages {
		if _, err := protodelim.MarshalTo(file, message); err != nil {
			t.Fatal(err)
		}
	}
}

// whole is a run that ended, as its record holds it: made at a minute,
// and finished as the run its last event names.
func whole(id string, minute int, endedAs string) []proto.Message {
	made := &buildv1.Build{Id: id, State: buildv1.Build_STATE_RUNNING, CreateTime: timestamppb.New(time.Date(2026, 11, 6, 10, minute, 0, 0, time.UTC)), Counts: &buildv1.Counts{}, Cost: &buildv1.Cost{}}
	final := proto.Clone(made).(*buildv1.Build)
	final.Id, final.State, final.LastSequence = endedAs, buildv1.Build_STATE_FINISHED, 1
	return []proto.Message{made, &buildv1.BuildEvent{Sequence: 1, Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: final}}}}
}

// The id a run is known by, and the directory that is removed
// when it is let go, is the id its *last event* names, which nothing
// holds to the directory the record was read from. A record whose last
// event names another place has that place removed when the run is
// pruned: a directory beside the daemon's own, or, with no id at all,
// everything the daemon was told to keep its runs in, other people's
// files included. AGENTS.md: "Something from outside trusted ... any id
// deleted".
func TestLettingARunGoRemovesOnlyThatRun(t *testing.T) {
	for name, endedAs := range map[string]string{
		"control: the run's own id":       "",
		"an id that leaves the directory": "../beside",
		"no id":                           "-",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "runs")
			beside := filepath.Join(root, "beside", "precious.txt")
			notes := filepath.Join(dir, "notes", "keep.txt")
			for _, file := range []string{beside, notes} {
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("someone's\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// Thirteen runs that ended: the oldest is let go.
			oldest := "build_000000000000"
			named := oldest
			switch endedAs {
			case "-":
				named = ""
			case "":
			default:
				named = endedAs
			}
			crafted(t, dir, oldest, whole(oldest, 0, named)...)
			endedRuns(t, dir, 1, endedKept)

			rs := readRuns(t, dir)

			for _, file := range []string{beside, notes} {
				if _, err := os.Stat(file); err != nil {
					t.Errorf("%s is gone: %v", file, err)
				}
			}
			on, _ := os.ReadDir(dir)
			directories := 0
			for _, entry := range on {
				if entry.IsDir() {
					directories++
				}
			}
			if directories != endedKept+1 {
				t.Errorf("the directory holds %d directories; want the %d newest runs and someone's notes", directories, endedKept)
			}
			if _, err := os.Stat(filepath.Join(dir, oldest)); err == nil {
				t.Errorf("the oldest run's record is still there, though the daemon lists %d runs", len(rs.list()))
			}
		})
	}
}

// A record whose run says nothing of its counts or its cost
// (a file damaged, or written by hand) stops the daemon from starting
// at all: it panics reading it.
func TestARecordThatSaysLittleDoesNotStopTheDaemon(t *testing.T) {
	for name, made := range map[string]*buildv1.Build{
		"control: a run with counts and cost": {Id: "build_0123456789ab", State: buildv1.Build_STATE_RUNNING, Counts: &buildv1.Counts{}, Cost: &buildv1.Cost{}},
		"a run with neither":                  {Id: "build_0123456789ab", State: buildv1.Build_STATE_RUNNING},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			crafted(t, dir, made.GetId(), made, plannedOf(3))
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("the daemon panicked reading its runs: %v", r)
				}
			}()
			if _, err := newRuns(nil, nil, nil, nil, nil, nil, dir, nil); err != nil {
				t.Logf("the daemon refused, which is an answer: %v", err)
			}
		})
	}
}

// A record cut at every byte: the next daemon starts, the run is the
// whole events before the cut, and it is ended.
func TestARecordCutAnywhereIsARunOrNone(t *testing.T) {
	sourceDir := t.TempDir()
	source := opened(t, sourceDir)
	r := begun(t, source, "build_0123456789ab")
	r.stage(buildv1.Build_STAGE_EVALUATING)
	r.append(plannedOf(3))
	r.finished(&buildv1.ActionResult{Label: "//a:b", Outcome: buildv1.Outcome_OUTCOME_PASSED})
	r.finish(time.Now(), exitSuccess, nil, false)
	want := len(r.events)
	bytes, err := os.ReadFile(filepath.Join(sourceDir, "build_0123456789ab", recordFile))
	if err != nil {
		t.Fatal(err)
	}
	for cut := 0; cut <= len(bytes); cut++ {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "build_0123456789ab"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "build_0123456789ab", recordFile), bytes[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			rs, err := newRuns(nil, nil, nil, nil, nil, nil, dir, nil)
			if err != nil {
				t.Fatalf("cut at %d: %v", cut, err)
			}
			for _, got := range rs.list() {
				if !got.ended() {
					t.Errorf("cut at %d: the run has not ended", cut)
				}
				if n := len(got.events); n > want {
					t.Errorf("cut at %d: %d events, more than the %d there were", cut, n, want)
				}
				for i, event := range got.events {
					if event.GetSequence() != int64(i+1) {
						t.Errorf("cut at %d: event %d has sequence %d", cut, i+1, event.GetSequence())
					}
				}
				if cut == len(bytes) && got.snapshot().GetExitCode().GetName() != "SUCCESS" {
					t.Errorf("the whole record reads as %s", got.snapshot().GetExitCode().GetName())
				}
				if cut < len(bytes) && got.snapshot().GetExitCode().GetName() != "INTERNAL_ERROR" {
					t.Errorf("cut at %d: the run ended %s", cut, got.snapshot().GetExitCode().GetName())
				}
			}
			rs.stop()
		}
	}
}

// A record that names what an action printed by a path, or by a digest
// the run was never given: StreamBuildLogs returns nothing of any file.
// (Found sound: the digest is held to being one. No test in the
// repository proved it; removing the check fails none of them.)
func TestWhatARecordNamesAsPrintedIsADigestOrNothing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "runs")
	id := "build_0123456789ab"
	secret := filepath.Join(root, "secret")
	if err := os.WriteFile(secret, []byte("not yours\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	messages := whole(id, 0, id)
	made, last := messages[0], messages[1].(*buildv1.BuildEvent)
	last.Sequence = 4
	last.GetFinished().GetBuild().LastSequence = 4
	never := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	result := func(sequence int64, label, hash string) *buildv1.BuildEvent {
		return &buildv1.BuildEvent{Sequence: sequence, Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{Label: label, Stdout: &buildv1.Digest{Hash: hash, SizeBytes: 10}}}}}
	}
	planned := &buildv1.BuildEvent{Sequence: 1, Event: &buildv1.BuildEvent_Planned_{Planned: &buildv1.BuildEvent_Planned{Actions: []*buildv1.BuildEvent_PlannedAction{{Label: "//a:path"}, {Label: "//a:never"}}}}}
	crafted(t, dir, id, made, planned, result(2, "//a:path", "../../../secret"), result(3, "//a:never", never), last)
	rs := readRuns(t, dir)
	service := &buildService{runs: rs}
	for _, label := range []string{"//a:path", "//a:never"} {
		var got []*buildv1.StreamBuildLogsResponse
		err := service.logs(t.Context(), &buildv1.StreamBuildLogsRequest{BuildId: id, Label: label}, func(part *buildv1.StreamBuildLogsResponse) error {
			got = append(got, part)
			return nil
		})
		if err == nil || len(got) > 0 {
			t.Errorf("%s: the daemon answers %v, %v", label, got, err)
		}
		t.Logf("%s: %v", label, err)
	}
}

// Events out of sequence, and a record whose first message is not a
// run. (Found sound: the run is the events before the gap; the record
// that begins with no run is let go.)
func TestARecordOutOfOrderIsTheEventsBeforeTheGap(t *testing.T) {
	id := "build_0123456789ab"
	made := whole(id, 0, id)[0]
	stage := func(sequence int64) *buildv1.BuildEvent {
		return &buildv1.BuildEvent{Sequence: sequence, Time: timestamppb.Now(), Event: &buildv1.BuildEvent_StageChanged_{StageChanged: &buildv1.BuildEvent_StageChanged{Stage: buildv1.Build_STAGE_EXECUTING}}}
	}
	for name, c := range map[string]struct {
		messages []proto.Message
		runs     int
		events   int
	}{
		"1, 2, 4":                 {[]proto.Message{made, stage(1), stage(2), stage(4)}, 1, 3},
		"2 first":                 {[]proto.Message{made, stage(2)}, 1, 1},
		"1 twice":                 {[]proto.Message{made, stage(1), stage(1)}, 1, 2},
		"an event and then a run": {[]proto.Message{stage(1), made}, 0, 0},
		"a run of another name":   {[]proto.Message{whole("build_ffffffffffff", 0, "build_ffffffffffff")[0]}, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			crafted(t, dir, id, c.messages...)
			for range 2 {
				rs := readRuns(t, dir)
				if len(rs.list()) != c.runs {
					t.Fatalf("%d runs, want %d", len(rs.list()), c.runs)
				}
				for _, r := range rs.list() {
					// The events before the gap, and the one that ends it.
					if len(r.events) != c.events || !r.ended() {
						t.Errorf("%d events, ended %v; want %d and ended", len(r.events), r.ended(), c.events)
					}
				}
				rs.stop()
			}
		})
	}
}

const (
	recordFile = "record"
	printedDir = "printed"
	writing    = ".writing-"
)

type failedRecord struct {
	runRecord
	duringAppend bool
}

func (r failedRecord) Event(e *buildv1.BuildEvent) error {
	if r.duringAppend {
		return syscall.ENOSPC
	}
	return r.runRecord.Event(e)
}
func (r failedRecord) Written() error { return syscall.ENOSPC }

// A record that says its run ended, with no event that ends it, is a run
// whoever waits on is answered at once, as one ended by its last event
// is (the control); neither waits until the caller gives up.
func TestWaitingOnARunARecordSaysEndedReturns(t *testing.T) {
	id := "build_0123456789ab"
	ended := whole(id, 0, id)
	said := proto.Clone(ended[0]).(*buildv1.Build)
	said.State = buildv1.Build_STATE_FINISHED
	for name, messages := range map[string][]proto.Message{
		"control: ended by its last event": ended,
		"said to have ended, by no event":  {said, plannedOf(1)},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			crafted(t, dir, id, messages...)
			service := &buildService{runs: readRuns(t, dir)}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			got, err := service.WaitBuild(ctx, &buildv1.WaitBuildRequest{BuildId: id})
			if err != nil || got.GetBuild().GetState() != buildv1.Build_STATE_FINISHED {
				t.Fatalf("waiting on the run: %v, %v", got, err)
			}
		})
	}
}

// A run's events are stamped by the daemon's clock, as its making and
// its end are, and so is the end of a run read back that its daemon died
// under; a run with no clock is stamped by the system's (the control).
func TestARunsEventsAreStampedByTheDaemonsClock(t *testing.T) {
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	dir := t.TempDir()
	r := begun(t, opened(t, dir), "build_0123456789ab")
	r.stage(buildv1.Build_STAGE_EVALUATING)
	if got := r.events[0].GetTime().AsTime(); got.Year() == at.Year() {
		t.Fatalf("a run with no clock was stamped %v", got)
	}
	r.clock = func() time.Time { return at }
	r.stage(buildv1.Build_STAGE_EXECUTING)
	if got := r.events[1].GetTime().AsTime(); !got.Equal(at) {
		t.Errorf("the event is stamped %v, want the daemon's %v", got, at)
	}
	bytes, err := os.ReadFile(filepath.Join(dir, "build_0123456789ab", recordFile))
	if err != nil {
		t.Fatal(err)
	}
	again := t.TempDir()
	write(t, again, filepath.Join("build_0123456789ab", recordFile), string(bytes))
	rs, err := newRuns(nil, nil, nil, nil, nil, nil, again, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	read := rs.list()[0]
	if last := read.events[len(read.events)-1]; !last.GetTime().AsTime().Equal(at) {
		t.Errorf("the end of a run its daemon died under is stamped %v, want the daemon's %v", last.GetTime().AsTime(), at)
	}
}
