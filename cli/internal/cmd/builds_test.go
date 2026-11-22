package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// recorded is a daemon that keeps one build that failed, and one run.
type recorded struct {
	programs
	from int64
}

func (r *recorded) DescribeBuild(_ context.Context, req *buildv1.DescribeBuildRequest) (*buildv1.DescribeBuildResponse, error) {
	return &buildv1.DescribeBuildResponse{Build: &buildv1.Build{Id: req.GetBuildId(), State: buildv1.Build_STATE_FINISHED, ExitCode: &buildv1.ExitCode{Name: "BUILD_FAILURE", Code: 1}, Failure: "1 action failed"}}, nil
}

func (r *recorded) ListBuildActions(context.Context, *buildv1.ListBuildActionsRequest) (*buildv1.ListBuildActionsResponse, error) {
	return &buildv1.ListBuildActionsResponse{Actions: []*buildv1.ActionResult{
		{Label: "//greeter:go.compile", Outcome: buildv1.Outcome_OUTCOME_FAILED, ExitCode: 1},
		{Label: "//greeter:go.test", Outcome: buildv1.Outcome_OUTCOME_SKIPPED, Message: "//greeter:go.compile failed"},
	}}, nil
}

func (r *recorded) StreamBuildLogs(_ context.Context, req *buildv1.StreamBuildLogsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildLogsServerStream) error {
	if req.GetLabel() != "//greeter:go.compile" {
		return connect.NewError(connect.CodeNotFound, "the build has planned no such action")
	}
	return stream.Send(&buildv1.StreamBuildLogsResponse{Lines: []*typev1.LogLine{{Text: "main.go:14:9: undefined: greet"}}})
}

func (r *recorded) StreamBuildEvents(_ context.Context, req *buildv1.StreamBuildEventsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildEventsServerStream) error {
	r.from = req.GetFromSequence()
	return r.programs.StreamBuildEvents(context.Background(), req, stream)
}

func (r *recorded) DescribeRun(_ context.Context, req *buildv1.DescribeRunRequest) (*buildv1.DescribeRunResponse, error) {
	return &buildv1.DescribeRunResponse{Run: &buildv1.Run{Id: req.GetRunId(), Target: "//x:go.run/x", State: buildv1.Run_STATE_EXITED, ExitCode: 2}}, nil
}

// A build is read by the commands beneath build, each by its id: where
// it is, what each action did, what one printed, and its events from
// the one --from numbers; a run by those beneath run.
func TestABuildAndARunAreReadByTheCommandsBeneathThem(t *testing.T) {
	r := &recorded{programs: programs{prints: []string{"hello"}}}
	at := serving(t, func(api *connect.Server) {
		buildv1alpha1connect.RegisterRunServiceHandler(api, r)
		buildv1alpha1connect.RegisterBuildServiceHandler(api, r)
	})
	for want, args := range map[string][]string{
		"build_7  BUILD_FAILURE\n  1 action failed\n":                                                        {"build", "describe", "build_7"},
		"FAILED  //greeter:go.compile  exit 1  0s\nskipped //greeter:go.test  //greeter:go.compile failed\n": {"build", "actions", "build_7"},
		"main.go:14:9: undefined: greet\n":                                                                   {"build", "logs", "build_7", "//greeter:go.compile"},
		"run_3  exited 2  //x:go.run/x\n":                                                                    {"run", "describe", "run_3"},
		"hello\n":                                                                                            {"run", "logs", "run_3"},
	} {
		printed, code, err := runAt(t, context.Background(), at, args...)
		if err != nil || code != 0 || printed != want {
			t.Errorf("%v printed %q, exit %d, %v; want %q", args, printed, code, err, want)
		}
	}
	if _, _, err := runAt(t, context.Background(), at, "build", "logs", "build_7", "//greeter:go.test"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("the logs of an action the build did not plan: %v", err)
	}
	if _, _, err := runAt(t, context.Background(), at, "build", "events", "build_7", "--from", "12"); err != nil || r.from != 12 {
		t.Errorf("build events --from 12 asked from %d: %v", r.from, err)
	}
}

// failedAction is a daemon whose build has one action that failed, which
// printed more than is shown of it.
type failedAction struct {
	recorded
	lines int
	logs  error
}

func (f *failedAction) StreamBuildEvents(_ context.Context, _ *buildv1.StreamBuildEventsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildEventsServerStream) error {
	return stream.Send(&buildv1.StreamBuildEventsResponse{Events: []*buildv1.BuildEvent{
		{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{Label: "//greeter:go.compile", Outcome: buildv1.Outcome_OUTCOME_FAILED, ExitCode: 1}}}},
		{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{Label: "//greeter:go.vet", Outcome: buildv1.Outcome_OUTCOME_PASSED}}}},
		{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: &buildv1.Build{Id: "build_7", ExitCode: &buildv1.ExitCode{Name: "BUILD_FAILURE", Code: 1}}}}},
	}})
}

func (f *failedAction) StreamBuildLogs(_ context.Context, req *buildv1.StreamBuildLogsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildLogsServerStream) error {
	if f.logs != nil {
		return f.logs
	}
	var lines []*typev1.LogLine
	for i := range f.lines {
		lines = append(lines, &typev1.LogLine{Text: fmt.Sprintf("%s line %d", req.GetLabel(), i)})
	}
	return stream.Send(&buildv1.StreamBuildLogsResponse{Lines: lines})
}

// What an action that failed printed is shown beneath it, the last 200
// lines of it, indented, with how to read all of it; what passed shows
// nothing of what it printed. Where its log cannot be read, the build is
// followed to its end all the same, and that is said apart.
func TestWhatAFailedActionPrintedIsShownBeneathIt(t *testing.T) {
	f := &failedAction{lines: 250}
	at := serving(t, func(api *connect.Server) { buildv1alpha1connect.RegisterBuildServiceHandler(api, f) })
	printed, code, err := runAt(t, context.Background(), at, "build", "events", "build_7")
	if err != nil || code != 1 {
		t.Fatalf("exit %d, %v", code, err)
	}
	lines := strings.Split(strings.TrimSuffix(printed, "\n"), "\n")
	if lines[0] != "FAILED  //greeter:go.compile  exit 1  0s" || lines[1] != "        //greeter:go.compile line 50" || lines[200] != "        //greeter:go.compile line 249" {
		t.Errorf("beneath the failure it printed %q … %q", lines[:2], lines[200])
	}
	if lines[201] != "        (frontseat build logs build_7 //greeter:go.compile  for all of it)" || !strings.HasPrefix(lines[202], "passed  //greeter:go.vet") || strings.Contains(printed, "go.vet line") {
		t.Errorf("after the failure's lines it printed %q", lines[201:])
	}
	f.logs = connect.NewError(connect.CodeInternal, "the log is gone")
	printed, code, err = runAt(t, context.Background(), at, "build", "events", "build_7")
	if err != nil || code != 1 || !strings.Contains(printed, "BUILD_FAILURE") || !strings.Contains(complaints.String(), "could not be read") {
		t.Errorf("a log that cannot be read: exit %d, %v, printing %q and saying %q", code, err, printed, complaints.String())
	}
}
