package build

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// logsOf is what one action of a run printed, every part of it, read
// from the stream as any client of the API reads it, to its end.
func logsOf(runs buildv1alpha1connect.BuildServiceClient, id, label string) ([]*typev1.LogLine, error) {
	logs, err := runs.StreamBuildLogs(context.Background(), &buildv1.StreamBuildLogsRequest{BuildId: id, Label: label})
	if err != nil {
		return nil, err
	}
	defer logs.Close()
	var lines []*typev1.LogLine
	for {
		part, err := logs.Receive()
		if errors.Is(err, io.EOF) {
			return lines, nil
		}
		if err != nil {
			return lines, err
		}
		lines = append(lines, part.GetLines()...)
	}
}

// told is all a daemon says of a run: the run, what its actions did,
// its events, and what one of its actions printed.
type told struct {
	run     *buildv1.Build
	actions *buildv1.ListBuildActionsResponse
	events  []*buildv1.BuildEvent
	printed []*typev1.LogLine
}

func tellOf(t *testing.T, c clients, id string) told {
	t.Helper()
	ctx := context.Background()
	described, err := c.runs.DescribeBuild(ctx, &buildv1.DescribeBuildRequest{BuildId: id})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := c.runs.ListBuildActions(ctx, &buildv1.ListBuildActionsRequest{BuildId: id})
	if err != nil {
		t.Fatal(err)
	}
	printed, err := logsOf(c.runs, id, "//hello:go.lint")
	if err != nil {
		t.Fatal(err)
	}
	return told{run: described.GetBuild(), actions: listed, events: events(t, c.runs, id, 0), printed: printed}
}

// same holds what one daemon says of a run to what another did.
func same(t *testing.T, got, want told) {
	t.Helper()
	if !proto.Equal(got.run, want.run) {
		t.Errorf("the run is\n%v\nwant\n%v", got.run, want.run)
	}
	if !proto.Equal(got.actions, want.actions) {
		t.Errorf("its actions are\n%v\nwant\n%v", got.actions, want.actions)
	}
	if !proto.Equal(&buildv1.StreamBuildLogsResponse{Lines: got.printed}, &buildv1.StreamBuildLogsResponse{Lines: want.printed}) {
		t.Errorf("its lint printed\n%v\nwant\n%v", got.printed, want.printed)
	}
	if len(got.events) != len(want.events) {
		t.Fatalf("it has %d events, want %d", len(got.events), len(want.events))
	}
	for i := range want.events {
		if !proto.Equal(got.events[i], want.events[i]) {
			t.Errorf("its event %d is\n%v\nwant\n%v", i+1, got.events[i], want.events[i])
		}
	}
}

// printing is a grid whose workers say how long they worked, and whose
// actions print.
func printing() exec.Executor {
	return answering{as: func(r *exec.Result) {
		r.Worked, r.Stdout, r.Stderr = 2*time.Second, []byte("checked\ntwo files\n"), []byte("a warning\n")
	}}
}

// A run belongs to the workspace, not to the daemon that ran it: the
// daemon that comes after says of it what the one that ran it said.
func TestARunOutlivesTheDaemonThatRanIt(t *testing.T) {
	config := configuration{Workspace: oneModule(t), Mounted: true, Executor: printing(), Builds: t.TempDir()}
	first := serveAs(t, config)
	run := wait(t, first.runs, create(t, first.runs).GetId())
	want := tellOf(t, first, run.GetId())
	if got := want.run.GetCost().GetWorkerTime().AsDuration(); got != 6*time.Second || len(want.printed) != 3 {
		t.Fatalf("the run cost %v of workers' time and its lint printed %d lines; want six seconds and three lines to find again", got, len(want.printed))
	}
	first.daemon.Close()

	second := serveAs(t, config)

	same(t, tellOf(t, second, run.GetId()), want)
	if listed := listedRuns(t, second); len(listed) != 1 || listed[0].GetId() != run.GetId() {
		t.Errorf("the daemon lists %v, want the one run", listed)
	}
}

// A plugin failure remains structured in the finished event, in queries, and
// after reopening the durable run record. A later valid run has no failure.
func TestAPluginDiagnosticOutlivesItsDaemon(t *testing.T) {
	workspace := oneModule(t)
	write(t, workspace, "hello/go.mod", "go 1.27\n")
	config := configuration{Workspace: workspace, Mounted: true, Executor: printing(), Builds: t.TempDir()}
	first := serveAs(t, config)
	run := wait(t, first.runs, create(t, first.runs).GetId())
	// The failure is said, and what the plugin found is kept as it said
	// it: its rule, its file and what to do, read without the message.
	if run.GetFailure() == "" || len(run.GetDiagnostics()) != 1 {
		t.Fatalf("the run lost its failure or its diagnostic: %v", run)
	}
	finding := run.GetDiagnostics()[0]
	if finding.GetRuleId() != "go/invalid-manifest" {
		t.Fatalf("wrong rule: %v", finding)
	}
	if finding.GetSourceLocation().GetPath() != "hello/go.mod" || finding.GetHelp() == "" {
		t.Fatalf("finding lost its source or help: %v", finding)
	}
	for _, event := range events(t, first.runs, run.GetId(), 0) {
		if finished := event.GetFinished(); finished != nil && !proto.Equal(finished.GetBuild(), run) {
			t.Fatal("finished event lost run details")
		}
	}
	first.daemon.Close()
	second := serveAs(t, config)
	got, err := second.runs.DescribeBuild(t.Context(), &buildv1.DescribeBuildRequest{BuildId: run.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got.GetBuild(), run) {
		t.Fatalf("reopening changed the run: %v; want %v", got.GetBuild(), run)
	}
	write(t, workspace, "hello/go.mod", "module example.com/hello\n\ngo 1.27\n")
	control := wait(t, second.runs, create(t, second.runs).GetId())
	if control.GetExitCode().GetCode() != 0 || control.GetFailure() != "" || len(control.GetDiagnostics()) != 0 {
		t.Fatalf("valid control failed: %v", control)
	}
}

// halting is a grid that runs a module's lint and never answers for
// anything else.
type halting struct{ scripted }

func (h halting) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if installs(action) || strings.HasSuffix(action.Label, "go.lint") {
		return h.scripted.Execute(ctx, action)
	}
	<-ctx.Done()
	return exec.Result{}, ctx.Err()
}

// underWay starts a run that gets as far as its lint and no further,
// and returns its id and a copy of the daemon's runs as they are then:
// what a daemon that died there would have left.
func underWay(t *testing.T) (id, left string, workspace string) {
	t.Helper()
	workspace = oneModule(t)
	kept := t.TempDir()
	c := serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: halting{}, Builds: kept})
	id = create(t, c.runs).GetId()
	onePassed(t, c, id)
	// An event is in the record a moment after it happened: the daemon
	// dies after that moment, and not in it.
	time.Sleep(time.Second)
	left = t.TempDir()
	if err := os.CopyFS(left, os.DirFS(kept)); err != nil {
		t.Fatal(err)
	}
	return id, left, workspace
}

// A daemon that dies leaves its runs where they were. The one that comes
// after ends each: it was not finished, nobody cancelled it, and it says
// so, with what its actions had done by then.
func TestARunItsDaemonDiedUnderIsEndedByTheNext(t *testing.T) {
	id, left, workspace := underWay(t)

	next := serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: scripted{}, Builds: left})

	got := tellOf(t, next, id)
	if got.run.GetState() != buildv1.Build_STATE_FINISHED || got.run.GetExitCode().GetName() != "INTERNAL_ERROR" || !strings.Contains(got.run.GetFailure(), "daemon") {
		t.Errorf("the run is %s, %s (%s); want it finished, in error, and saying its daemon ended under it", got.run.GetState(), got.run.GetExitCode().GetName(), got.run.GetFailure())
	}
	if got.run.GetCounts().GetPassed() != 1 || got.run.GetEndTime() == nil {
		t.Errorf("the run ended %v at %v; want its lint passed and an end", got.run.GetCounts(), got.run.GetEndTime())
	}
	last := got.events[len(got.events)-1]
	if last.GetFinished() == nil || last.GetSequence() != int64(len(got.events)) || !proto.Equal(last.GetFinished().GetBuild(), got.run) {
		t.Errorf("its last event is %v; want the run as it ended", last)
	}
	// It is ended once: the daemon after that one finds it ended.
	next.daemon.Close()
	after := serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: scripted{}, Builds: left})
	same(t, tellOf(t, after, id), got)
}

// What a dying daemon was writing when it died is not a whole event. It
// is let go, and the run is the events before it.
func TestAnEventCutShortIsNotPartOfARun(t *testing.T) {
	config := configuration{Workspace: oneModule(t), Mounted: true, Executor: printing(), Builds: t.TempDir()}
	first := serveAs(t, config)
	run := wait(t, first.runs, create(t, first.runs).GetId())
	want := tellOf(t, first, run.GetId())
	first.daemon.Close()
	record := filepath.Join(config.Builds, run.GetId(), "record")
	whole, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}

	// Its last event, the run's end, less its last bytes.
	if err := os.WriteFile(record, whole[:len(whole)-3], 0o644); err != nil {
		t.Fatal(err)
	}
	next := serveAs(t, config)

	got := tellOf(t, next, run.GetId())
	if got.run.GetExitCode().GetName() != "INTERNAL_ERROR" || len(got.events) != len(want.events) {
		t.Fatalf("the run is %s with %d events; want it ended by this daemon, with its %d events less the one cut short and one for its end", got.run.GetExitCode().GetName(), len(got.events), len(want.events))
	}
	for i := range len(want.events) - 1 {
		if !proto.Equal(got.events[i], want.events[i]) {
			t.Errorf("its event %d is\n%v\nwant\n%v", i+1, got.events[i], want.events[i])
		}
	}
	if !proto.Equal(got.actions, want.actions) {
		t.Errorf("its actions are\n%v\nwant\n%v", got.actions, want.actions)
	}
}

// The daemon keeps its newest runs and lets the rest go, so that what it
// keeps does not grow with every build. A run that has not ended is
// never let go.
func TestOnlyTheNewestRunsAreKept(t *testing.T) {
	// A run that has only just ended is kept whatever has ended since:
	// fourteen runs that have just ended are all kept, then the daemon's
	// clock is put on an hour and the run that ends after them lets go of
	// the oldest.
	var ahead atomic.Int64
	clock := func() time.Time { return time.Now().Add(time.Duration(ahead.Load())) }
	config := configuration{Workspace: oneModule(t), Mounted: true, Executor: scripted{}, Builds: t.TempDir(), Now: clock}
	c := serveAs(t, config)
	var ids []string
	for range 14 {
		ids = append(ids, wait(t, c.runs, create(t, c.runs).GetId()).GetId())
	}
	// Ended within the moment a run lingers, all fourteen are kept,
	// though they are more than the twelve the daemon keeps.
	if listed, on := listedRuns(t, c), runsIn(t, config.Builds); len(listed) != 14 || on != 14 {
		t.Fatalf("of fourteen runs just ended the daemon lists %d and holds %d, want every one", len(listed), on)
	}
	ahead.Store(int64(time.Hour))
	ids = append(ids, wait(t, c.runs, create(t, c.runs).GetId()).GetId())

	kept := func(c clients) {
		t.Helper()
		listed := listedRuns(t, c)
		if len(listed) != 12 {
			t.Fatalf("the daemon keeps %d runs, want its 12 newest", len(listed))
		}
		for i, run := range listed {
			if want := ids[len(ids)-1-i]; run.GetId() != want {
				t.Errorf("run %d of the list is %s, want %s: the newest first", i, run.GetId(), want)
			}
		}
		for _, gone := range ids[:len(ids)-12] {
			if _, err := c.runs.DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: gone}); connect.CodeOf(err) != connect.CodeNotFound {
				t.Errorf("asking after %s, a run let go: %v; want it not found", gone, err)
			}
		}
		if on := runsIn(t, config.Builds); on != 12 {
			t.Errorf("the daemon's directory holds %d runs, want 12", on)
		}
	}
	kept(c)

	c.daemon.Close()
	kept(serveAs(t, config))
}

// The directory a daemon keeps its runs in is someone's directory. What
// is in it that is not a run is not the daemon's to touch; a run that
// was never made, its daemon having died making it, is let go.
func TestWhatIsNotARunIsLeftWhereItIs(t *testing.T) {
	config := configuration{Workspace: oneModule(t), Mounted: true, Executor: scripted{}, Builds: t.TempDir()}
	write(t, config.Builds, "notes/keep.txt", "someone's\n")
	write(t, config.Builds, "run_0123456789ab/record", "")
	write(t, config.Builds, "README", "someone's\n")

	c := serveAs(t, config)

	listed, err := c.runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
	if err != nil || len(listed.GetBuilds()) != 0 {
		t.Errorf("the daemon lists %v, %v; want no run", listed.GetBuilds(), err)
	}
	for file, want := range map[string]bool{"notes/keep.txt": true, "README": true, "build_0123456789ab": false} {
		if _, err := os.Stat(filepath.Join(config.Builds, file)); (err == nil) != want {
			t.Errorf("%s is there: %v, want %v", file, err == nil, want)
		}
	}
}

// runsIn is how many runs a directory holds: its directories, whatever
// else a daemon keeps there.
func runsIn(t *testing.T, dir string) int {
	t.Helper()
	on, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	for _, entry := range on {
		if entry.IsDir() {
			runs++
		}
	}
	return runs
}

// Whoever made a run asks how it ended a moment after it has: a run
// that has only just ended is kept, however many have ended since.
func TestARunThatHasJustEndedIsKeptHoweverManyHaveEndedSince(t *testing.T) {
	config := configuration{Workspace: oneModule(t), Mounted: true, Executor: scripted{}, Builds: t.TempDir()}
	c := serveAs(t, config)
	first := wait(t, c.runs, create(t, c.runs).GetId()).GetId()
	for range 14 {
		wait(t, c.runs, create(t, c.runs).GetId())
	}

	if _, err := c.runs.DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: first}); err != nil {
		t.Errorf("asking after a run that ended a moment ago, fourteen runs later: %v; want it kept", err)
	}
}
