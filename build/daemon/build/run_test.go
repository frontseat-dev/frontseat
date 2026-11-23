package build

import (
	"context"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/build/daemon/directory"
	"github.com/frontseat-dev/frontseat/common/vcs/chosen"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// module is the Go plugin, and catalog the Backstage one: what every
// pinned fixture's frontseat.yaml names (testpins.Go).
var module, catalog []byte

// strategy is the release plugin, which a daemon that releases has.
var strategy, forge []byte

func TestMain(m *testing.M) {
	module = testmodule.PluginOrExit("go")
	catalog = testmodule.PluginOrExit("backstage")
	strategy = testmodule.PluginOrExit("release")
	forge = testmodule.PluginOrExit("github")
	os.Exit(m.Run())
}

// loaded are the Go, Backstage, release and GitHub plugins, compiled once for
// every daemon the tests of this package start: a daemon closes nothing
// of a plugin, and a plugin is instantiated anew for each call. Compiled
// for each daemon, as they were, they cost about 3.6 s a test under the
// race detector (`go test -race -run '^TestARunThatDoesNotExistIsNotFound$'
// ./build/`, 2026-11-12), nearly all of what the package took.
var loaded = sync.OnceValues(func() ([]*pluginhost.Plugin, error) {
	ctx := context.Background()
	host, err := pluginhost.New(ctx)
	if err != nil {
		return nil, err
	}
	var plugins []*pluginhost.Plugin
	for _, each := range [][]byte{module, catalog, strategy, forge} {
		plugin, err := host.Load(ctx, each)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, plugin)
	}
	return plugins, nil
})

// daemon serves the build API over a workspace that holds one Go module,
// mounted, and returns a client of its runs.
func daemon(t *testing.T, executor exec.Executor) buildv1alpha1connect.BuildServiceClient {
	t.Helper()
	return serve(t, executor, oneModule(t), true).runs
}

// oneModule is a directory that holds one Go module, which has no
// tests: its actions are its lint, its format and its compile.
func oneModule(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	write(t, workspace, "hello/go.mod", "module example.com/hello\n\ngo 1.27\n")
	write(t, workspace, "hello/main.go", "package main\n\nfunc main() {}\n")
	testpins.Go(t, workspace)
	return workspace
}

// clients are one daemon and the clients of it.
type clients struct {
	daemon *Daemon
	runs   buildv1alpha1connect.BuildServiceClient
	graph  buildv1alpha1connect.GraphServiceClient
	// programs is the run service's: the programs a workspace runs.
	programs buildv1alpha1connect.RunServiceClient
	// releases is the release service's.
	releases buildv1alpha1connect.ReleaseServiceClient
}

// serve serves the build API over a workspace's directory.
func serve(t *testing.T, executor exec.Executor, workspace string, mounted bool) clients {
	t.Helper()
	return serveAs(t, configuration{Workspace: workspace, Mounted: mounted, Executor: executor})
}

// serveAs serves the build API as a configuration says, with the Go,
// Backstage and release plugins and the test grid's platform.
func serveAs(t testing.TB, config configuration) clients {
	t.Helper()
	return serveFirst(t, config, 3)
}

// serveFirst serves the build API as a configuration says, with so many
// of the plugins loaded, the first of them, and the test grid's platform.
func serveFirst(t testing.TB, config configuration, plugins int) clients {
	t.Helper()
	all, err := loaded()
	if err != nil {
		t.Fatal(err)
	}
	config.Plugins, config.Platform = all[:plugins], testgrid.Platform()
	if config.Builds == "" {
		config.Builds = t.TempDir()
	}
	config.System = readWith()
	served, err := newDaemon(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(served.Close)
	srv := httptest.NewServer(served.Handler)
	t.Cleanup(srv.Close)
	api := connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL))
	return clients{daemon: served, runs: buildv1alpha1connect.NewBuildServiceClient(api), graph: buildv1alpha1connect.NewGraphServiceClient(api), programs: buildv1alpha1connect.NewRunServiceClient(api), releases: buildv1alpha1connect.NewReleaseServiceClient(api)}
}

// write writes a file of a workspace, named by its path beneath root
// with slashes.
func write(t testing.TB, root, name, content string) {
	t.Helper()
	testfiles.Tree(t, root, map[string]string{name: content})
}

// scripted is an executor a test decides the behaviour of.
type scripted struct {
	// fails are words; an action whose label holds one exits 1.
	fails []string
	// gate, if set, is waited on before every action.
	gate chan struct{}
	// hang makes every action run until it is cancelled.
	hang bool
}

// installs is whether an action is one the daemon asks of the grid
// itself for the tools, before the actions of a build: one that installs
// a tool, or asks mise what tools put in the environment.
func installs(action exec.Action) bool {
	return strings.HasPrefix(action.Label, "mise install ") || toolstest.Asks(action)
}

// left is what an action was asked to leave, as a grid that ran it
// would say it left it.
func left(action exec.Action) map[string]exec.Held {
	if len(action.Outputs) == 0 {
		return nil
	}
	all := map[string]exec.Held{}
	for _, output := range action.Outputs {
		all[output] = exec.Held{Digest: exec.Digest{Hash: strings.Repeat("0", 64), Size: 1}, Directory: true}
	}
	return all
}

func (s scripted) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	// mise says at once what the tools put in the environment, and a
	// tool is installed at once.
	if toolstest.Asks(action) {
		return toolstest.Environment(action), nil
	}
	if installs(action) {
		return exec.Result{Outputs: left(action)}, nil
	}
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return exec.Result{}, ctx.Err()
		}
	}
	if s.hang {
		<-ctx.Done()
		return exec.Result{}, ctx.Err()
	}
	for _, word := range s.fails {
		if strings.Contains(action.Label, word) {
			return exec.Result{ExitCode: 1, Stderr: []byte("main.go:3: something is wrong\nvet failed\n")}, nil
		}
	}
	return exec.Result{Stdout: []byte("ok\n"), Outputs: left(action)}, nil
}

func create(t testing.TB, client buildv1alpha1connect.BuildServiceClient) *buildv1.Build {
	t.Helper()
	created, err := client.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return created.GetBuild()
}

// wait follows a run to its end and returns it as it ended. A run on the
// grid shares its workers with every other test that is running.
func wait(t testing.TB, client buildv1alpha1connect.BuildServiceClient, id string) *buildv1.Build {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ended, err := client.WaitBuild(ctx, &buildv1.WaitBuildRequest{BuildId: id})
	if err != nil {
		t.Fatal(err)
	}
	return ended.GetBuild()
}

// events reads a run's events from a sequence to the run's end.
func events(t *testing.T, client buildv1alpha1connect.BuildServiceClient, id string, from int64) []*buildv1.BuildEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := client.StreamBuildEvents(ctx, &buildv1.StreamBuildEventsRequest{BuildId: id, FromSequence: from})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var out []*buildv1.BuildEvent
	for {
		part, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, part.GetEvents()...)
	}
}

// watched is an executor that knows how many of its actions are running,
// and says when the first begins.
type watched struct {
	scripted
	began   chan struct{}
	running *atomic.Int32
}

func (w watched) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	if !installs(action) {
		w.running.Add(1)
		defer w.running.Add(-1)
		select {
		case w.began <- struct{}{}:
		default:
		}
	}
	return w.scripted.Execute(ctx, action)
}

func TestADaemonThatEndsLeavesNoRunGoing(t *testing.T) {
	executor := watched{scripted: scripted{hang: true}, began: make(chan struct{}, 1), running: new(atomic.Int32)}
	served := serve(t, executor, oneModule(t), true)
	going := create(t, served.runs)
	select {
	case <-executor.began:
	case <-time.After(30 * time.Second):
		t.Fatal("no action of the run began")
	}

	served.daemon.Close()

	if still := executor.running.Load(); still != 0 {
		t.Errorf("%d actions still run after the daemon ended", still)
	}
	described, err := served.runs.DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: going.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if got := described.GetBuild().GetState(); got != buildv1.Build_STATE_CANCELLED {
		t.Errorf("the run is %v after the daemon ended, want cancelled", got)
	}
	_, err = served.runs.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("a run asked of a daemon that ended: %v, want unavailable", err)
	}
}

func TestARunIsMadeAtOnceAndGoesOnWithoutItsCaller(t *testing.T) {
	gate := make(chan struct{})
	client := daemon(t, scripted{gate: gate})

	caller, leave := context.WithCancel(context.Background())
	created, err := client.CreateBuild(caller, &buildv1.CreateBuildRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := created.GetBuild().GetState(); got != buildv1.Build_STATE_RUNNING {
		t.Errorf("the run was returned %v, want running", got)
	}
	leave()
	close(gate)

	ended := wait(t, client, created.GetBuild().GetId())
	if ended.GetState() != buildv1.Build_STATE_SUCCEEDED || ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		t.Errorf("the run ended %v %s, want finished SUCCESS", ended.GetState(), ended.GetExitCode())
	}
}

func TestEventsAreNumberedFromOneWithNoGapsAndEndWithTheRun(t *testing.T) {
	client := daemon(t, scripted{})
	run := create(t, client)

	got := events(t, client, run.GetId(), 0)

	for i, e := range got {
		if e.GetSequence() != int64(i+1) {
			t.Fatalf("event %d has sequence %d", i+1, e.GetSequence())
		}
	}
	var planned []string
	for _, e := range got {
		for _, a := range e.GetPlanned().GetActions() {
			planned = append(planned, a.GetLabel())
		}
	}
	want := []string{"//hello:go.lint", "//hello:go.format", "//hello:go.compile"}
	if strings.Join(planned, " ") != strings.Join(want, " ") {
		t.Errorf("planned %v, want %v", planned, want)
	}
	last := got[len(got)-1].GetFinished().GetBuild()
	if last == nil {
		t.Fatalf("the last event is %v, want the run's end", got[len(got)-1])
	}
	if last.GetCounts().GetPassed() != 3 || last.GetLastSequence() != int64(len(got)) {
		t.Errorf("the run ended with %d passed at sequence %d, want 3 at %d", last.GetCounts().GetPassed(), last.GetLastSequence(), len(got))
	}
}

func TestAStreamThatBrokeCarriesOnFromTheSequenceAfterTheLastItSaw(t *testing.T) {
	client := daemon(t, scripted{})
	run := create(t, client)
	all := events(t, client, run.GetId(), 0)

	const seen = 4
	rest := events(t, client, run.GetId(), seen+1)

	if len(rest) != len(all)-seen || rest[0].GetSequence() != seen+1 {
		t.Errorf("carrying on after %d gave %d events from sequence %d, want %d from %d", seen, len(rest), rest[0].GetSequence(), len(all)-seen, seen+1)
	}
}

func TestACancelledRunEndsInterruptedAndStartsNothingMore(t *testing.T) {
	// An entity with two actions of its own, the second reading what the
	// first leaves: while the first runs, the second has not started.
	workspace := t.TempDir()
	write(t, workspace, "frontseat.yaml", "plugins: {}\n")
	write(t, workspace, "catalog-info.yaml", `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: chain
spec:
  type: service
  lifecycle: production
  owner: team
  frontseat:
    actions:
      first:
        command: ["true"]
        outputs: [made/]
      second:
        command: ["true"]
        inputs: [":custom.first"]
`)
	client := serve(t, scripted{hang: true}, workspace, true).runs
	run := create(t, client)
	// The first is running once the run says it started one: its fourth
	// event, after two stages and its plan.
	for {
		got, err := client.DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: run.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetBuild().GetLastSequence() >= 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := client.CancelBuild(context.Background(), &buildv1.CancelBuildRequest{BuildId: run.GetId()}); err != nil {
		t.Fatal(err)
	}

	ended := wait(t, client, run.GetId())
	if ended.GetState() != buildv1.Build_STATE_CANCELLED || ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_INTERRUPTED {
		t.Errorf("the run ended %v %v, want cancelled and interrupted", ended.GetState(), ended.GetExitCode())
	}
	if ended.GetCounts().GetUnreached() != 1 || ended.GetCounts().GetSkipped() != 0 {
		t.Errorf("%d actions were unreached and %d skipped, want the 1 that never started unreached", ended.GetCounts().GetUnreached(), ended.GetCounts().GetSkipped())
	}
}

func TestAFailedActionFailsTheRunAndTheOthersStillRun(t *testing.T) {
	client := daemon(t, scripted{fails: []string{"go.lint"}})
	run := create(t, client)

	ended := wait(t, client, run.GetId())
	if ended.GetState() != buildv1.Build_STATE_FAILED || ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_BUILD_FAILURE || ended.GetCounts().GetPassed() != 2 || ended.GetCounts().GetFailed() != 1 {
		t.Errorf("the run ended %s, %s with %d passed and %d failed, want failed, BUILD_FAILURE with 2 and 1", ended.GetState(), ended.GetExitCode(), ended.GetCounts().GetPassed(), ended.GetCounts().GetFailed())
	}

	listed, err := client.ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{BuildId: run.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	var failed []string
	for _, action := range listed.GetActions() {
		if action.GetOutcome() == buildv1.Outcome_OUTCOME_FAILED {
			failed = append(failed, action.GetLabel())
		}
	}
	if len(failed) != 1 || failed[0] != "//hello:go.lint" {
		t.Fatalf("the failed actions are %v, want //hello:go.lint alone", failed)
	}

	logs, err := logsOf(client, run.GetId(), "//hello:go.lint")
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || logs[0].GetText() != "main.go:3: something is wrong" {
		t.Errorf("the action's output is %v, want its two lines", logs)
	}
}

func TestARunExecutesItsActionsOnTheGridAndTheNextIsAnsweredFromWhatItKept(t *testing.T) {
	grid := reapitest.Dial(t)
	workspace := t.TempDir()
	write(t, workspace, "hello/go.mod", "module example.com/hello\n\ngo 1.27\n")
	write(t, workspace, "hello/main.go", fmt.Sprintf("package main\n\nfunc main() { println(%d) }\n", time.Now().UnixNano()))
	testpins.Go(t, workspace)
	client := serve(t, grid, workspace, true).runs

	first := wait(t, client, create(t, client).GetId())
	if first.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		actions, _ := client.ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{BuildId: first.GetId()})
		t.Fatalf("the run ended %s (%s): %v", first.GetExitCode(), first.GetFailure(), actions.GetActions())
	}
	if first.GetCost().GetActionsExecuted() != 3 {
		t.Errorf("%d actions were executed, want 3", first.GetCost().GetActionsExecuted())
	}

	second := wait(t, client, create(t, client).GetId())
	if second.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || second.GetCounts().GetPassed() != 3 || second.GetCost().GetActionsExecuted() != 0 {
		t.Errorf("the second run ended %s with %d passed and %d executed; want all 3 answered from the grid's cache", second.GetExitCode(), second.GetCounts().GetPassed(), second.GetCost().GetActionsExecuted())
	}
	t.Logf("first run %s, second %s", first.GetCost().GetWallTime().AsDuration().Round(time.Millisecond), second.GetCost().GetWallTime().AsDuration().Round(time.Millisecond))
}

func TestARunThatDoesNotExistIsNotFound(t *testing.T) {
	client := daemon(t, scripted{})

	_, err := client.DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: "build_nothing"})

	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("err = %v, want not found", err)
	}
}

// readWith is the version control system the tests' directories are
// read with: the one a build daemon gives a build (life.Serve).
func readWith() directory.System { return chosen.System() }
