package cmd

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// programs is a daemon's run service as a test has it answer, and
// what it was asked.
type programs struct {
	buildv1alpha1connect.UnimplementedRunServiceHandler
	buildv1alpha1connect.UnimplementedBuildServiceHandler
	// ends is how the run ends, and prints what its program printed.
	ends   *buildv1.Run
	prints []string
	// holds, if set, keeps the program running until it is stopped.
	holds bool
	// waits, if set, is how waiting for the run fails.
	waits error

	mu      sync.Mutex
	created []*buildv1.CreateRunRequest
	stopped []string
	// halted are the builds that were cancelled.
	halted []string
	stop   chan struct{}
}

func (l *programs) CreateRun(_ context.Context, request *buildv1.CreateRunRequest) (*buildv1.CreateRunResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.created = append(l.created, request)
	// The run is made with its build's run: nothing more is asked of
	// it to learn which, and this daemon describes no run.
	return &buildv1.CreateRunResponse{Run: &buildv1.Run{Id: "run_1", BuildId: "build_1", State: buildv1.Run_STATE_BUILDING}}, nil
}

func (l *programs) StreamBuildEvents(_ context.Context, _ *buildv1.StreamBuildEventsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildEventsServerStream) error {
	return stream.Send(&buildv1.StreamBuildEventsResponse{Events: []*buildv1.BuildEvent{{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: &buildv1.Build{Id: "build_1", ExitCode: &buildv1.ExitCode{Name: "SUCCESS"}}}}}}})
}

func (l *programs) StreamRunLogs(ctx context.Context, _ *buildv1.StreamRunLogsRequest, stream buildv1alpha1connect.RunServiceStreamRunLogsServerStream) error {
	var lines []*typev1.LogLine
	for _, text := range l.prints {
		lines = append(lines, &typev1.LogLine{Text: text})
	}
	if err := stream.Send(&buildv1.StreamRunLogsResponse{Lines: lines}); err != nil {
		return err
	}
	if l.holds {
		select {
		case <-l.stop:
		case <-ctx.Done():
		}
	}
	return nil
}

func (l *programs) WaitRun(context.Context, *buildv1.WaitRunRequest) (*buildv1.WaitRunResponse, error) {
	if l.waits != nil {
		return nil, l.waits
	}
	return &buildv1.WaitRunResponse{Run: l.ends}, nil
}

func (l *programs) CancelRun(_ context.Context, request *buildv1.CancelRunRequest) (*buildv1.CancelRunResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = append(l.stopped, request.GetRunId())
	if l.stop != nil {
		select {
		case <-l.stop:
		default:
			close(l.stop)
		}
	}
	return &buildv1.CancelRunResponse{Run: &buildv1.Run{Id: request.GetRunId(), Target: "//x:go.run/x", State: buildv1.Run_STATE_CANCELLED}}, nil
}

func (l *programs) CancelBuild(_ context.Context, request *buildv1.CancelBuildRequest) (*buildv1.CancelBuildResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.halted = append(l.halted, request.GetBuildId())
	return &buildv1.CancelBuildResponse{Build: &buildv1.Build{Id: request.GetBuildId(), State: buildv1.Build_STATE_CANCELLED}}, nil
}

func (l *programs) ListRuns(context.Context, *buildv1.ListRunsRequest) (*buildv1.ListRunsResponse, error) {
	return &buildv1.ListRunsResponse{Runs: []*buildv1.Run{
		{Id: "run_2", Target: "//x:go.run/x", Arguments: []string{"--port", "1"}, State: buildv1.Run_STATE_RUNNING},
		{Id: "run_1", Target: "//x:go.run/y", State: buildv1.Run_STATE_EXITED, ExitCode: 3},
		{Id: "run_0", Target: "//x:go.compile", State: buildv1.Run_STATE_FAILED, Failure: "it is no program\x1b[2J"},
	}}, nil
}

// running runs the command line against a programs.
func running(t *testing.T, ctx context.Context, l *programs, args ...string) (string, int, error) {
	t.Helper()
	return runAt(t, ctx, serving(t, func(api *connect.Server) {
		buildv1alpha1connect.RegisterRunServiceHandler(api, l)
		buildv1alpha1connect.RegisterBuildServiceHandler(api, l)
	}), args...)
}

// stoppedTheRunItMade fails the test unless the command stopped the
// one run it made, and no other.
func stoppedTheRunItMade(t *testing.T, l *programs) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.stopped) != 1 || l.stopped[0] != "run_1" {
		t.Errorf("it stopped %v, want the run it made", l.stopped)
	}
}

// run asks for one program with what follows the dashes, prints what
// the program prints as text, and exits as the program did.
func TestRunGivesTheProgramWhatFollowsAndExitsAsItDid(t *testing.T) {
	l := &programs{
		ends:   &buildv1.Run{Id: "run_1", State: buildv1.Run_STATE_EXITED, ExitCode: 3},
		prints: []string{"hello", "moves \x1b[2J the screen"},
	}
	printed, code, err := running(t, context.Background(), l, "run", "--profile", "production", "//x:go.run/x", "--", "--name", "two words", "--endpoint", "its own")
	if err != nil || code != 3 {
		t.Fatalf("exit %d, %v\n%s", code, err, printed)
	}
	if len(l.created) != 1 {
		t.Fatalf("it asked for %d runs", len(l.created))
	}
	asked := l.created[0]
	if asked.GetTarget() != "//x:go.run/x" || asked.GetProfile() != "production" || strings.Join(asked.GetArguments(), "|") != "--name|two words|--endpoint|its own" {
		t.Errorf("it asked for %v", asked)
	}
	if !strings.Contains(printed, "hello\n") || !strings.Contains(printed, `moves \x1b[2J the screen`) || strings.Contains(printed, "\x1b") {
		t.Errorf("it printed %q", printed)
	}
	if len(l.stopped) != 0 {
		t.Errorf("it stopped %v, which ended by itself", l.stopped)
	}
}

// What was not run says why, and the command fails.
func TestARunThatFailedExitsOneAndSaysWhy(t *testing.T) {
	l := &programs{ends: &buildv1.Run{Id: "run_1", State: buildv1.Run_STATE_FAILED, Failure: "//x:go.compile is a step of a build, and no program"}}
	printed, code, err := running(t, context.Background(), l, "run", "//x:go.compile")
	if err != nil || code != 1 || !strings.Contains(printed, "not run: //x:go.compile is a step of a build, and no program") {
		t.Errorf("exit %d, %v\n%s", code, err, printed)
	}
	// A program is named: nothing is asked of a daemon without one.
	none := &programs{}
	if _, _, err := running(t, context.Background(), none, "run"); err == nil || len(none.created) != 0 {
		t.Errorf("run with no program: %v, asked %d", err, len(none.created))
	}
}

// A program is the daemon's, and would go on without the command: one
// that is interrupted stops what it run before it ends.
func TestAnInterruptedRunStopsItsProgram(t *testing.T) {
	l := &programs{holds: true, stop: make(chan struct{}), prints: []string{"up"}, ends: &buildv1.Run{Id: "run_1", State: buildv1.Run_STATE_CANCELLED}}
	ctx, interrupt := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		interrupt()
	}()
	_, _, err := running(t, ctx, l, "run", "//x:go.run/x")
	if err == nil {
		t.Error("an interrupted run ended as if it had run")
	}
	stoppedTheRunItMade(t, l)
}

// runs lists the programs the daemon has run.
func TestRunsListsThePrograms(t *testing.T) {
	l := &programs{}
	printed, code, err := running(t, context.Background(), l, "runs")
	if err != nil || code != 0 {
		t.Fatalf("exit %d, %v", code, err)
	}
	want := "run_2  running  //x:go.run/x -- --port 1\nrun_1  exited 3  //x:go.run/y\nrun_0  failed: it is no program\\x1b[2J  //x:go.compile\n"
	if printed != want {
		t.Errorf("it printed\n%q\nwant\n%q", printed, want)
	}
}

// One cancel stops a build or a run of a program, by its id, as builds
// and runs list them; an id that is neither is refused before the
// daemon is asked. The verb is the API's: there is no other spelling of
// it, and no runs cancel.
func TestCancelStopsABuildOrARunByItsID(t *testing.T) {
	l := &programs{}
	if _, code, err := running(t, context.Background(), l, "cancel", "run_2"); err != nil || code != 0 || len(l.stopped) != 1 || l.stopped[0] != "run_2" || len(l.halted) != 0 {
		t.Errorf("cancel of a run: %v, cancelled runs %v and builds %v", err, l.stopped, l.halted)
	}
	if _, code, err := running(t, context.Background(), l, "cancel", "build_7"); err != nil || code != 0 || len(l.halted) != 1 || l.halted[0] != "build_7" || len(l.stopped) != 1 {
		t.Errorf("cancel of a build: %v, cancelled runs %v and builds %v", err, l.stopped, l.halted)
	}
	for _, args := range [][]string{{"cancel", "rel_1"}, {"cancel", "7"}, {"runs", "cancel", "run_2"}, {"runs", "stop", "run_2"}, {"cancel"}} {
		if _, _, err := running(t, context.Background(), l, args...); err == nil {
			t.Errorf("%v was taken", args)
		}
	}
	if len(l.stopped) != 1 || len(l.halted) != 1 {
		t.Errorf("what was refused cancelled runs %v and builds %v", l.stopped, l.halted)
	}
}

// What follows the program is the program's, with or without the
// dashes: a flag there is none of this command's, its own names among
// them.
func TestWhatFollowsAProgramIsItsOwn(t *testing.T) {
	for _, after := range [][]string{
		{"serve", "-h"},
		{"--help"},
		{"--name", "world"},
		{"serve", "--endpoint", "http://127.0.0.1:1"},
		{"--profile", "production"},
		{"--", "--profile", "production"},
		{"--", "--", "x"},
	} {
		l := &programs{ends: &buildv1.Run{Id: "run_1", State: buildv1.Run_STATE_EXITED}}
		printed, code, err := running(t, context.Background(), l, append([]string{"run", "//x:go.run/x"}, after...)...)
		if err != nil || code != 0 || len(l.created) != 1 {
			t.Errorf("%v: exit %d, %v, asked %d runs\n%s", after, code, err, len(l.created), printed)
			continue
		}
		want := after
		if want[0] == "--" {
			want = want[1:]
		}
		if got := l.created[0]; strings.Join(got.GetArguments(), "|") != strings.Join(want, "|") || got.GetProfile() != "" {
			t.Errorf("%v: the program was given %q, built with %q", after, got.GetArguments(), got.GetProfile())
		}
	}
}

// A run the command can follow no longer is stopped: the program is
// the daemon's, and would go on unseen.
func TestARunThatCannotBeFollowedIsStopped(t *testing.T) {
	l := &programs{prints: []string{"up"}}
	l.waits = connect.NewError(connect.CodeInternal, "the daemon lost it")
	_, _, err := running(t, context.Background(), l, "run", "//x:go.run/x")
	if err == nil || !strings.Contains(err.Error(), "the daemon lost it") {
		t.Errorf("a run that could not be waited for: %v", err)
	}
	stoppedTheRunItMade(t, l)
}

// A run someone else stopped did not run to its end: the command
// says so and fails, whatever the program exited with.
func TestARunStoppedByAnotherFails(t *testing.T) {
	for want, ended := range map[int]*buildv1.Run{
		1:   {Id: "run_1", State: buildv1.Run_STATE_CANCELLED},
		143: {Id: "run_1", State: buildv1.Run_STATE_CANCELLED, ExitCode: 143, Failure: "a process could not be signalled"},
	} {
		printed, code, err := running(t, context.Background(), &programs{ends: ended}, "run", "//x:go.run/x")
		if err != nil || code != want || !strings.Contains(printed, "cancelled") || !strings.Contains(printed, ended.GetFailure()) {
			t.Errorf("exit %d, want %d, %v\n%s", code, want, err, printed)
		}
	}
}
