package cmd

import (
	"bytes"
	"context"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/frontseat-dev/frontseat/cli/internal/machine"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
)

// daemonOf is a daemon's release service as a test has it answer, and
// what it was asked.
type daemonOf struct {
	buildv1alpha1connect.UnimplementedReleaseServiceHandler
	buildv1alpha1connect.UnimplementedBuildServiceHandler
	state   buildv1.Release_State
	planned []*buildv1.PlanReleaseRequest
	created []*buildv1.CreateReleaseRequest
	// builds makes the release one that builds: it is made with its
	// run, and ends when it is waited for.
	builds bool
	// streams is how the build's events are answered: its events, or
	// this failure.
	streams error
	// waits, if set, is waited on before a release is said to have ended.
	waits  chan struct{}
	waited atomic.Int32
	// events, if set, are the build's events in place of the one above.
	events []*buildv1.BuildEvent
	// printed is what each action printed, by its label, and unread what
	// reading any of it is answered with instead; read is whose was asked
	// for.
	printed map[string][]string
	unread  error
	read    []string
}

func (d *daemonOf) PlanRelease(_ context.Context, request *buildv1.PlanReleaseRequest) (*buildv1.PlanReleaseResponse, error) {
	d.planned = append(d.planned, request)
	plan := &buildv1.ReleasePlan{Etag: "etag-of-the-plan"}
	for _, entity := range request.GetEntities() {
		plan.Units = append(plan.Units, &buildv1.ReleaseUnit{
			Entity: entity, Members: []*typev1.EntityRef{entity}, NextVersion: "0.77.0", Tag: entity.GetName() + "/v0.77.0", Branch: "release/0.77",
			Reasons: []*buildv1.ReleaseReason{{Kind: buildv1.ReasonKind_REASON_KIND_FIRST_RELEASE}},
		})
	}
	return &buildv1.PlanReleaseResponse{Plan: plan}, nil
}

func (d *daemonOf) StreamBuildEvents(_ context.Context, request *buildv1.StreamBuildEventsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildEventsServerStream) error {
	if d.streams != nil {
		return d.streams
	}
	if d.events != nil {
		return stream.Send(&buildv1.StreamBuildEventsResponse{Events: d.events})
	}
	return stream.Send(&buildv1.StreamBuildEventsResponse{Events: []*buildv1.BuildEvent{
		{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: &buildv1.ActionResult{Outcome: buildv1.Outcome_OUTCOME_PASSED, Label: "//greet:go.compile"}}}},
	}})
}

func (d *daemonOf) StreamBuildLogs(_ context.Context, request *buildv1.StreamBuildLogsRequest, stream buildv1alpha1connect.BuildServiceStreamBuildLogsServerStream) error {
	d.read = append(d.read, request.GetLabel())
	if d.unread != nil {
		return d.unread
	}
	// In two parts, as an action that printed much is sent.
	for _, text := range d.printed[request.GetLabel()] {
		if err := stream.Send(&buildv1.StreamBuildLogsResponse{Lines: []*typev1.LogLine{{Text: text}}}); err != nil {
			return err
		}
	}
	return nil
}

// ended is the events of a build of three actions, one of which failed
// and one of which was not run for it, and the build's end.
func ended() []*buildv1.BuildEvent {
	finished := func(result *buildv1.ActionResult) *buildv1.BuildEvent {
		return &buildv1.BuildEvent{Event: &buildv1.BuildEvent_ActionFinished_{ActionFinished: &buildv1.BuildEvent_ActionFinished{Result: result}}}
	}
	return []*buildv1.BuildEvent{
		finished(&buildv1.ActionResult{Outcome: buildv1.Outcome_OUTCOME_PASSED, Label: "//greet:go.compile"}),
		finished(&buildv1.ActionResult{Outcome: buildv1.Outcome_OUTCOME_FAILED, Label: "//greet:go.lint", ExitCode: 1}),
		finished(&buildv1.ActionResult{Outcome: buildv1.Outcome_OUTCOME_SKIPPED, Label: "//greet:go.test", Message: "not run: //greet:go.lint failed"}),
		{Event: &buildv1.BuildEvent_Finished_{Finished: &buildv1.BuildEvent_Finished{Build: &buildv1.Build{ExitCode: &buildv1.ExitCode{Name: "BUILD_FAILURE", Code: 1}}}}},
	}
}

func (d *daemonOf) ListBuilds(context.Context, *buildv1.ListBuildsRequest) (*buildv1.ListBuildsResponse, error) {
	return &buildv1.ListBuildsResponse{}, nil
}

func (d *daemonOf) release() *buildv1.Release {
	out := &buildv1.Release{Id: "rel_1", State: d.state, EndTime: timestamppb.Now()}
	if d.builds {
		out.BuildId = "run_9"
	}
	if d.state == buildv1.Release_STATE_FAILED {
		out.Failure = "the production build run_9 ended BUILD_FAILURE; nothing is tagged"
	}
	return out
}

func (d *daemonOf) CreateRelease(_ context.Context, request *buildv1.CreateReleaseRequest) (*buildv1.CreateReleaseResponse, error) {
	d.created = append(d.created, request)
	if request.GetEtag() == "stale" {
		return nil, connect.NewError(connect.CodeAborted, "the plan is no longer what releasing would do; plan again")
	}
	if d.builds {
		return &buildv1.CreateReleaseResponse{Release: &buildv1.Release{Id: "rel_1", State: buildv1.Release_STATE_BUILDING, BuildId: "run_9"}}, nil
	}
	return &buildv1.CreateReleaseResponse{Release: d.release()}, nil
}

func (d *daemonOf) WaitRelease(ctx context.Context, _ *buildv1.WaitReleaseRequest) (*buildv1.WaitReleaseResponse, error) {
	d.waited.Add(1)
	if d.waits != nil {
		select {
		case <-d.waits:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &buildv1.WaitReleaseResponse{Release: d.release()}, nil
}

// run runs the command line against a daemon, and returns what it
// printed, the exit code and why it could not run. Each run begins with
// every flag as it is by default, as each process does.
func run(t *testing.T, d *daemonOf, args ...string) (string, int, error) {
	t.Helper()
	return runWith(t, context.Background(), d, args...)
}

func runWith(t *testing.T, ctx context.Context, d *daemonOf, args ...string) (string, int, error) {
	t.Helper()
	return runAt(t, ctx, serving(t, func(api *connect.Server) {
		buildv1alpha1connect.RegisterReleaseServiceHandler(api, d)
		buildv1alpha1connect.RegisterBuildServiceHandler(api, d)
	}), args...)
}

// serving serves, for as long as the test runs, the services register
// puts on a Connect server, and returns the address they are served at.
func serving(t *testing.T, register func(api *connect.Server)) string {
	t.Helper()
	api := connect.NewServer()
	register(api)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, api)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// runAt runs the command line inside a workspace's sandbox whose daemon
// is at endpoint, or outside any where endpoint is empty, and returns
// what it printed, the exit code and why it could not run.
func runAt(t *testing.T, ctx context.Context, endpoint string, args ...string) (string, int, error) {
	t.Helper()
	inside, here = func() (bool, error) { return endpoint != "", nil }, endpoint
	t.Cleanup(func() { inside, here = outside, machine.Daemon })
	var printed bytes.Buffer
	out = &printed
	t.Cleanup(func() { out = os.Stdout })
	code, err := Execute(ctx, args)
	return printed.String(), code, err
}

// serverAt has the test's commands ask the control plane at server, as
// frontseat/local/server names it, beneath a root of the test's own.
func serverAt(t *testing.T, server string) string {
	t.Helper()
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	kept := filepath.Join(config, "frontseat", "local")
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, "server"), []byte(server+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return kept
}

// lastOpened has the test's commands find the workspace named as the
// one last opened, kept by the control plane at server.
func lastOpened(t *testing.T, server, workspaceName string) {
	t.Helper()
	if err := machine.WriteContext(machine.Context{Server: server, Workspace: workspaceName}); err != nil {
		t.Fatal(err)
	}
}

func TestShipPlanSaysWhatReleasingWouldDoAndReleasesNothing(t *testing.T) {
	d := &daemonOf{}
	printed, code, err := run(t, d, "ship", "plan", "greet", "system:kiosk")
	if err != nil || code != 0 {
		t.Fatalf("ship plan ended %d, %v", code, err)
	}
	want := "greet  unreleased -> 0.77.0  tag greet/v0.77.0  on release/0.77\n  its first release\nsystem:kiosk  unreleased -> 0.77.0  tag kiosk/v0.77.0  on release/0.77\n  its first release\nplan etag-of-the-plan\n"
	if printed != want {
		t.Errorf("ship plan printed\n%s\nwant\n%s", printed, want)
	}
	if len(d.planned) != 1 || len(d.created) != 0 {
		t.Fatalf("it planned %d times and released %d", len(d.planned), len(d.created))
	}
	asked := d.planned[0].GetEntities()
	// A kind left out is a component; a namespace left out is left for
	// the daemon to find.
	if len(asked) != 2 || asked[0].GetKind() != "component" || asked[0].GetNamespace() != "" || asked[0].GetName() != "greet" || asked[1].GetKind() != "system" || asked[1].GetNamespace() != "" || asked[1].GetName() != "kiosk" {
		t.Errorf("it asked of %v", asked)
	}
}

func TestShipPlansThenReleasesThatPlan(t *testing.T) {
	d := &daemonOf{state: buildv1.Release_STATE_SUCCEEDED}
	printed, code, err := run(t, d, "ship", "greet")
	if err != nil || code != 0 {
		t.Fatalf("ship ended %d, %v", code, err)
	}
	if len(d.planned) != 1 || len(d.created) != 1 || d.created[0].GetEtag() != "etag-of-the-plan" || d.created[0].GetEntities()[0].GetName() != "greet" {
		t.Errorf("it planned %v and released %v", d.planned, d.created)
	}
	if !strings.HasPrefix(printed, "greet  unreleased -> 0.77.0") || !strings.Contains(printed, "\nreleasing rel_1\n") || !strings.HasSuffix(printed, "rel_1  succeeded\n") {
		t.Errorf("ship printed\n%s", printed)
	}
}

// A release that failed is the command's failure: it exits 1, having
// said why. So is one that tagged and could not push every tag, whose
// tags never left the workspace. One that did what was asked exits 0.
func TestAShipThatFailsExitsOne(t *testing.T) {
	printed, code, err := run(t, &daemonOf{state: buildv1.Release_STATE_FAILED}, "ship", "greet")
	if err != nil || code != 1 || !strings.Contains(printed, "rel_1  FAILED: the production build run_9 ended BUILD_FAILURE") {
		t.Errorf("a failed ship ended %d, %v, printing\n%s", code, err, printed)
	}
	unpublished, code, err := run(t, &daemonOf{state: buildv1.Release_STATE_UNPUBLISHED}, "ship", "greet")
	if err != nil || code != 1 || !strings.Contains(unpublished, "rel_1  unpublished: its tags are in the workspace and are not pushed") {
		t.Errorf("a ship that ended unpublished ended %d, %v, printing\n%s", code, err, unpublished)
	}
	if _, code, err := run(t, &daemonOf{state: buildv1.Release_STATE_SUCCEEDED}, "ship", "greet"); err != nil || code != 0 {
		t.Errorf("the control ended %d, %v", code, err)
	}
}

// With --plan a release is of the plan that was seen, and no other: the
// command plans nothing of its own, and what the daemon refuses is said.
func TestShipWithAPlanReleasesThatPlanAndNoOther(t *testing.T) {
	d := &daemonOf{state: buildv1.Release_STATE_SUCCEEDED}
	// A flag is read wherever it is written.
	for _, args := range [][]string{{"ship", "--plan", "seen", "greet"}, {"ship", "greet", "--plan", "seen"}, {"ship", "greet", "--plan=seen"}} {
		d.planned, d.created = nil, nil
		if _, code, err := run(t, d, args...); err != nil || code != 0 {
			t.Fatalf("%v ended %d, %v", args, code, err)
		}
		if len(d.planned) != 0 || len(d.created) != 1 || d.created[0].GetEtag() != "seen" {
			t.Errorf("%v planned %d times and released %v", args, len(d.planned), d.created)
		}
	}
	_, _, err := run(t, d, "ship", "greet", "--plan", "stale")
	if err == nil || !strings.Contains(err.Error(), "plan again") {
		t.Errorf("a plan the workspace has moved from: %v", err)
	}
}

func TestWhatIsWrittenWronglyIsRefusedBeforeADaemonIsAsked(t *testing.T) {
	d := &daemonOf{}
	for name, args := range map[string][]string{
		"ship with nothing named":          {"ship"},
		"ship plan with nothing named":     {"ship", "plan"},
		"an entity that is none":           {"ship", "plan", "a b"},
		"a flag ship does not take":        {"ship", "greet", "--only", "test"},
		"a command there is not":           {"release", "greet"},
		"no command at all":                {},
		"workspace by itself":              {"workspace"},
		"a workspace command there is not": {"workspace", "delet"},
		"a flag written with one dash":     {"build", "-only", "test"},
		"builds with an argument":          {"builds", "extra"},
		"cancel with no id":                {"cancel"},
		"cancel with two ids":              {"cancel", "build_1", "run_1"},
		"runs cancel, which is cancel":     {"runs", "cancel", "run_1"},
		"events, which is builds events":   {"events", "build_1"},
		"open, which is workspace open":    {"open"},
		"test keeps no other actions":      {"test", "--only", "lint"},
		"query of two targets":             {"query", "a", "b"},
		"a server named by a flag":         {"workspace", "list", "--server", "http://127.0.0.1:1"},
	} {
		if _, _, err := run(t, d, args...); err == nil {
			t.Errorf("%s was run", name)
		}
	}
	if len(d.planned)+len(d.created) != 0 {
		t.Errorf("a daemon was asked: %v %v", d.planned, d.created)
	}
}

// A command is named in a trace by its own name and nothing it was
// given.
func TestACommandIsNamedWithoutWhatItWasGiven(t *testing.T) {
	if got := Name([]string{"help", "ship"}); got != "help" {
		t.Errorf("help of a command is named %q", got)
	}
	for want, args := range map[string][]string{
		"build":          {"build", "gateway", "--only", "test"},
		"ship":           {"ship", "greet"},
		"ship plan":      {"ship", "plan", "secret-entity"},
		"workspace ssh":  {"workspace", "ssh", "cat", "/etc/passwd"},
		"workspace open": {"workspace", "open", "https://secret.example/private.git"},
		"builds events":  {"builds", "events", "build_1", "7"},
		"test":           {"test", "secret-entity"},
		"query":          {"query", "secret-entity", "--why", "another"},
		"unknown":        {"not-a-command", "secret"},
		"help":           {},
	} {
		if got := Name(args); got != want {
			t.Errorf("Name(%v) = %q, want %q", args, got, want)
		}
	}
	if got := Name([]string{"--endpoint", "https://secret.example", "build", "x"}); got != "build" {
		t.Errorf("a flag before the command names it %q", got)
	}
	if got := Name([]string{"--help"}); got != "help" {
		t.Errorf("--help is named %q", got)
	}
}

// What follows the command to run over SSH is that command's, flags and
// all: it is not read as this command's.
func TestWhatFollowsACommandOverSSHIsItsOwn(t *testing.T) {
	if err := sshCmd.ParseFlags([]string{"--config"}); err != nil || !sshConfig {
		t.Fatalf("--config before the command is this command's: %v", err)
	}
	sshConfig = false
	if err := sshCmd.ParseFlags([]string{"ls", "--config", "-la"}); err != nil {
		t.Fatal(err)
	}
	if sshConfig || strings.Join(sshCmd.Flags().Args(), " ") != "ls --config -la" {
		t.Errorf("after the command, its flags were read as ours: config %v, args %v", sshConfig, sshCmd.Flags().Args())
	}
}

// A release that builds is followed as a build is: the command says
// which release it is at once, prints the events of the build it was
// made with, asking nothing more of the release to learn which, and
// waits for the release to end. The daemon here describes no release:
// a command that asked it would fail.
func TestShipFollowsItsBuildAndWaitsForTheRelease(t *testing.T) {
	d := &daemonOf{state: buildv1.Release_STATE_SUCCEEDED, builds: true}
	printed, code, err := run(t, d, "ship", "greet")
	if err != nil || code != 0 {
		t.Fatalf("ship ended %d, %v", code, err)
	}
	began, built, ended := strings.Index(printed, "releasing rel_1\n"), strings.Index(printed, "passed  //greet:go.compile"), strings.Index(printed, "rel_1  succeeded")
	if began < 0 || built < began || ended < built || d.waited.Load() != 1 {
		t.Errorf("ship printed, having waited %d times:\n%s", d.waited.Load(), printed)
	}
}

// A build that cannot be followed is still a release that goes on: the
// command waits for it and says how it ended, and exits by that.
func TestABuildThatCannotBeFollowedIsStillWaitedFor(t *testing.T) {
	for state, want := range map[buildv1.Release_State]int{buildv1.Release_STATE_SUCCEEDED: 0, buildv1.Release_STATE_UNPUBLISHED: 1, buildv1.Release_STATE_FAILED: 1} {
		d := &daemonOf{state: state, builds: true, streams: connect.NewError(connect.CodeUnavailable, "the stream broke")}
		printed, code, err := run(t, d, "ship", "greet")
		if err != nil || code != want || d.waited.Load() != 1 || !strings.Contains(printed, "rel_1  ") {
			t.Errorf("%v: ship ended %d, %v, having waited %d, printing\n%s", state, code, err, d.waited.Load(), printed)
		}
	}
}

// Interrupted, the command ends, having said which release goes on
// without it.
func TestAnInterruptedShipHasSaidWhichReleaseGoesOn(t *testing.T) {
	d := &daemonOf{state: buildv1.Release_STATE_SUCCEEDED, waits: make(chan struct{})}
	d.builds = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var printed string
	var err error
	go func() {
		defer close(done)
		printed, _, err = runWith(t, ctx, d, "ship", "greet")
	}()
	// It is waiting for the release: interrupt it there.
	for i := 0; i < 500 && d.waited.Load() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("an interrupted ship did not end")
	}
	if err == nil || !strings.Contains(printed, "releasing rel_1\n") {
		t.Errorf("an interrupted ship ended %v, having printed\n%s", err, printed)
	}
	close(d.waits)
}

// Each command line begins as a process does: what one was given is not
// there for the next.
func TestOneCommandLineLeavesNothingForTheNext(t *testing.T) {
	d := &daemonOf{state: buildv1.Release_STATE_SUCCEEDED}
	if _, _, err := run(t, d, "ship", "--help"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, d, "ship", "greet", "--plan", "seen"); err != nil {
		t.Fatal(err)
	}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := runWith(t, gone, d, "ship", "greet"); err == nil {
		t.Fatal("a command line under a context that has ended ran, so this test proves nothing")
	}
	d.planned, d.created = nil, nil
	printed, code, err := run(t, d, "ship", "greet")
	if err != nil || code != 0 || len(d.planned) != 1 || len(d.created) != 1 || d.created[0].GetEtag() != "etag-of-the-plan" {
		t.Errorf("after --help, --plan and a cancelled run, ship ended %d, %v, planned %d and released %v, printing\n%s", code, err, len(d.planned), d.created, printed)
	}
}

// A command that asks a workspace is told which by its name, and asks
// its daemon where the control plane that keeps it says: the workspace
// named, and not the one the environment's daemon manages. No command
// takes a daemon's address, and those that do not ask a daemon take no
// workspace this way. The control is the same command with no name,
// which asks the environment's.
func TestAWorkspaceIsNamedAndNotAddressed(t *testing.T) {
	d := &daemonOf{}
	api := connect.NewServer()
	buildv1alpha1connect.RegisterReleaseServiceHandler(api, d)
	daemon := http.NewServeMux()
	connecthttp.Mount(daemon, api)
	// The control plane, which keeps the workspace "named" and leads to
	// its daemon by its name.
	plane := http.NewServeMux()
	var at string
	keeps := connect.NewServer()
	workspacev1alpha1connect.RegisterWorkspaceServiceHandler(keeps, keeping{daemon: func() string { return at }})
	connecthttp.Mount(plane, keeps)
	plane.Handle("/workspaces/named/build/", http.StripPrefix("/workspaces/named/build", daemon))
	srv := httptest.NewServer(plane)
	t.Cleanup(srv.Close)
	at = srv.URL + "/workspaces/named/build"
	serverAt(t, srv.URL)
	lastOpened(t, srv.URL, "another")
	if _, _, err := runAt(t, context.Background(), "", "ship", "plan", "greet", "--workspace", "named"); err != nil {
		t.Fatal(err)
	}
	if len(d.planned) != 1 {
		t.Errorf("the workspace named was asked %d times", len(d.planned))
	}
	if _, _, err := runAt(t, context.Background(), "", "ship", "plan", "greet", "--workspace", "not-there"); err == nil || !strings.Contains(err.Error(), "not-there") {
		t.Errorf("a workspace that is not there: %v, want it said", err)
	}
	// Inside a workspace's sandbox, its own daemon is asked, and no
	// other: a name is given outside.
	other := &daemonOf{}
	if _, _, err := run(t, other, "ship", "plan", "greet"); err != nil || len(other.planned) != 1 {
		t.Errorf("inside, with no name, the daemon there was asked %d times (%v)", len(other.planned), err)
	}
	if _, _, err := run(t, other, "ship", "plan", "greet", "--workspace", "named"); err == nil || !strings.Contains(err.Error(), "--workspace is given outside") || len(d.planned) != 1 {
		t.Errorf("inside, another workspace named: %v, and it was asked %d times; want it refused by name, and asked once", err, len(d.planned))
	}
	for _, args := range [][]string{{"build", "--endpoint", "x"}, {"workspace", "open", "--workspace", "x"}, {"workspace", "ssh", "--endpoint", "x"}} {
		if _, _, err := run(t, d, args...); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%v took a flag it has none of: %v", args, err)
		}
	}
	for _, path := range [][]string{{"workspace", "describe"}, {"workspace", "add"}, {"build"}, {"test"}, {"query"}, {"builds"}, {"builds", "events"}, {"run"}, {"runs"}, {"cancel"}, {"ship"}, {"ship", "plan"}} {
		sub, _, err := rootCmd.Find(path)
		if err != nil || sub.Flags().Lookup("workspace") == nil {
			t.Errorf("%v asks a workspace and takes no --workspace", path)
		}
	}
}

// keeping is a control plane that keeps one workspace, "named".
type keeping struct {
	workspacev1alpha1connect.UnimplementedWorkspaceServiceHandler
	daemon func() string
}

func (k keeping) DescribeWorkspace(_ context.Context, req *workspacev1.DescribeWorkspaceRequest) (*workspacev1.DescribeWorkspaceResponse, error) {
	if req.GetName() != "named" {
		return nil, connect.NewError(connect.CodeNotFound, "there is no workspace "+strconv.Quote(req.GetName()))
	}
	return &workspacev1.DescribeWorkspaceResponse{Workspace: &workspacev1.Workspace{Name: "named", Status: &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_READY, BuildUri: k.daemon()}}}, nil
}

// A workspace's source is in its sandbox: the command line has no way to
// give a sandbox a directory of this machine.
func TestNoDirectoryOfThisMachineIsGivenToASandbox(t *testing.T) {
	serverAt(t, "http://127.0.0.1:1")
	_, _, err := run(t, &daemonOf{}, "workspace", "open", "--image", "box", "--mount", t.TempDir(), "https://example.com/a.git")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --mount") {
		t.Fatalf("a mount was not refused as a flag there is none of: %v", err)
	}
	// The same command without it is taken, and gets as far as asking the server.
	_, _, err = run(t, &daemonOf{}, "workspace", "open", "--image", "box", "https://example.com/a.git")
	if err == nil || strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("the control was not taken as far as the server: %v", err)
	}
}

func (k keeping) ListWorkspaces(context.Context, *workspacev1.ListWorkspacesRequest) (*workspacev1.ListWorkspacesResponse, error) {
	return &workspacev1.ListWorkspacesResponse{Workspaces: []*workspacev1.Workspace{{Name: "named", Status: &workspacev1.WorkspaceStatus{State: workspacev1.WorkspaceStatus_STATE_READY}}}}, nil
}

// Every command asks one control plane, which one rule names: the one
// frontseat/local/server names, or else the local stack's. Listing,
// opening and ending workspaces ask it, and no command takes a server by
// a flag. The workspace last opened is asked only where it was opened of
// that control plane. The control is the plane the rule names, which is
// asked; that the local stack's is where nothing names another is the
// machine package's to show, so that no test here asks this machine's.
func TestEveryCommandAsksTheOneControlPlaneTheRuleNames(t *testing.T) {
	plane := serving(t, func(api *connect.Server) {
		workspacev1alpha1connect.RegisterWorkspaceServiceHandler(api, keeping{daemon: func() string { return "" }})
	})
	serverAt(t, plane)
	got, _, err := runAt(t, context.Background(), "", "workspace", "list")
	if err != nil || !strings.HasPrefix(got, "named  ready") {
		t.Errorf("listing asked elsewhere than the control plane the rule names: %q, %v", got, err)
	}
	for _, args := range [][]string{{"workspace", "list", "--server", plane}, {"workspace", "open", "--server", plane}, {"workspace", "delete", "--server", plane}, {"workspace", "credential", "h", "--server", plane}} {
		if _, _, err := runAt(t, context.Background(), "", args...); err == nil || !strings.Contains(err.Error(), "unknown flag: --server") {
			t.Errorf("%v took a server by a flag: %v", args, err)
		}
	}
	// The workspace last opened of another control plane is none of this
	// one's.
	lastOpened(t, "http://127.0.0.1:1", "named")
	if _, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err == nil || !strings.Contains(err.Error(), "is of the control plane at http://127.0.0.1:1") {
		t.Errorf("the workspace last opened of another control plane was asked: %v", err)
	}
	lastOpened(t, plane, "named")
	if got, _, err := runAt(t, context.Background(), "", "workspace", "describe"); err != nil || got != "named  ready\n" {
		t.Errorf("the workspace last opened of this control plane was not asked: %q, %v", got, err)
	}
}

// frontseat --help says which control plane every command asks, and how
// a command is told which workspace; the control is a command's own
// help, which says neither.
func TestTheHelpSaysWhichServerAndWorkspaceAreAsked(t *testing.T) {
	printed := &strings.Builder{}
	rootCmd.SetOut(printed)
	t.Cleanup(func() { rootCmd.SetOut(os.Stdout) })
	if _, err := Execute(context.Background(), []string{"--help"}); err != nil {
		t.Fatal(err)
	}
	for _, said := range []string{"~/.config/frontseat/local/server", "http://127.0.0.1:7700", "local/token", "--workspace NAME", "last opened"} {
		if !strings.Contains(printed.String(), said) {
			t.Errorf("frontseat --help does not say %q:\n%s", said, printed)
		}
	}
	printed.Reset()
	if _, err := Execute(context.Background(), []string{"cancel", "--help"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(printed.String(), "local/server") {
		t.Errorf("a command's own help says the rule again:\n%s", printed)
	}
}

// An action that failed is followed by what it printed, all of it and
// as it printed it, beneath the line that says it failed and before the
// next action's: its exit code says nothing of why. What a terminal
// would act on is written as the bytes it is, so nothing an action
// prints is taken for a line of the command's own. Control: an action
// that passed or was not run is one line, and what it printed is not
// asked for.
func TestAnActionThatFailedIsFollowedByWhatItPrinted(t *testing.T) {
	d := &daemonOf{events: ended(), printed: map[string][]string{
		"//greet:go.lint":    {"# example.com/greet", "vet: greet.go:24:63: undefined: syscall.SO_REUSEPORT", "", "passed  //forged:line  1ms\rFAILED", "\x1b[2Jlast"},
		"//greet:go.compile": {"never shown"},
		"//greet:go.test":    {"never shown"},
	}}
	printed, code, err := run(t, d, "builds", "events", "bld_1")
	if err != nil || code != 1 {
		t.Fatalf("ended %d, %v:\n%s", code, err, printed)
	}
	want := "passed  //greet:go.compile  0s\n" +
		"FAILED  //greet:go.lint  exit 1  0s\n" +
		"        # example.com/greet\n" +
		"        vet: greet.go:24:63: undefined: syscall.SO_REUSEPORT\n" +
		"        \n" +
		"        passed  //forged:line  1ms\\x0dFAILED\n" +
		"        \\x1b[2Jlast\n" +
		"skipped //greet:go.test  not run: //greet:go.lint failed\n"
	if !strings.HasPrefix(printed, want) {
		t.Errorf("printed\n%s\nwant, first,\n%s", printed, want)
	}
	if !slices.Equal(d.read, []string{"//greet:go.lint"}) {
		t.Errorf("what was printed was asked of %v, want of the action that failed alone", d.read)
	}
}

// What a failed action printed may not be there to read, or the daemon
// may not answer: the command says so in the action's place and goes on
// to the build's end, which it exits by. A build is not lost for a log.
func TestWhatAFailedActionPrintedMayNotBeRead(t *testing.T) {
	d := &daemonOf{events: ended(), unread: connect.NewError(connect.CodeInternal, "what the action printed is not there to be read")}
	printed, code, err := run(t, d, "builds", "events", "bld_1")
	if err != nil || code != 1 {
		t.Fatalf("ended %d, %v:\n%s", code, err, printed)
	}
	failed, said, next := strings.Index(printed, "FAILED  //greet:go.lint  exit 1"), strings.Index(printed, "        what it printed could not be read: "), strings.Index(printed, "skipped //greet:go.test")
	if failed < 0 || said < failed || next < said || !strings.Contains(printed, "BUILD_FAILURE") {
		t.Errorf("printed\n%s", printed)
	}
	// An action that printed nothing is its one line.
	d = &daemonOf{events: ended()}
	printed, _, _ = run(t, d, "builds", "events", "bld_1")
	if !strings.Contains(printed, "FAILED  //greet:go.lint  exit 1  0s\nskipped //greet:go.test") {
		t.Errorf("an action that printed nothing printed\n%s", printed)
	}
}
