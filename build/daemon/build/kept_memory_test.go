package build

// What the daemon remembers from one run to the next.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/build/engine/exec/tools/toolstest"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// serveWithGraph is serveAs, with a client of the graph too.
func serveWithGraph(t testing.TB, config configuration) (clients, buildv1alpha1connect.GraphServiceClient) {
	t.Helper()
	// The Go and the Backstage plugins, and no release strategy.
	c := serveFirst(t, config, 2)
	return c, c.graph
}

func createAs(t testing.TB, client buildv1alpha1connect.BuildServiceClient, profile typev1.Profile) *buildv1.Build {
	t.Helper()
	created, err := client.CreateBuild(context.Background(), &buildv1.CreateBuildRequest{Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	return created.GetBuild()
}

func projectsOf(t testing.TB, graph buildv1alpha1connect.GraphServiceClient) []string {
	t.Helper()
	listed, err := graph.ListProjects(context.Background(), &buildv1.ListProjectsRequest{PageSize: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, project := range listed.GetProjects() {
		out = append(out, project.GetRef().GetRoot())
	}
	sort.Strings(out)
	return out
}

// Runs of two profiles and questions of the graph, all at once, on one
// daemon: what is kept is shared by all of them.
func TestRunsAndGraphQueriesAtOnce(t *testing.T) {
	workspace := oneModule(t)
	for i := range 20 {
		write(t, workspace, fmt.Sprintf("m%d/go.mod", i), fmt.Sprintf("module example.com/m%d\n\ngo 1.27\n", i))
		write(t, workspace, fmt.Sprintf("m%d/main.go", i), "package main\n\nfunc main() {}\n")
	}
	write(t, workspace, "frontseat.yaml", "plugins:\n  go:\n    tags: [a]\n")
	write(t, workspace, "m0/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: m0\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    plugins:\n      go:\n        tags: [b]\n        test-timeout: 1m\n    actions:\n      check:\n        command: [\"true\"]\n        inputs: [\"*.go\"]\n")
	c, graph := serveWithGraph(t, configuration{Workspace: workspace, Mounted: true, Executor: asking{edited: &sync.Once{}}})

	for round := range 3 {
		var all sync.WaitGroup
		for i := range 6 {
			all.Go(func() {
				profile := typev1.Profile_PROFILE_DEVELOPMENT
				if i%2 == 1 {
					profile = typev1.Profile_PROFILE_PRODUCTION
				}
				run := wait(t, c.runs, createAs(t, c.runs, profile).GetId())
				if run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
					t.Errorf("round %d: a run ended %s: %s", round, run.GetExitCode(), run.GetFailure())
				}
			})
			all.Go(func() {
				if got := projectsOf(t, graph); len(got) != 21 {
					t.Errorf("round %d: the graph holds %d projects; want 21", round, len(got))
				}
			})
			all.Go(func() {
				if _, err := graph.ListEntities(context.Background(), &buildv1.ListEntitiesRequest{}); err != nil {
					t.Errorf("round %d: %v", round, err)
				}
			})
		}
		all.Wait()
	}
}

// What a plugin looked for and did not find is part of what it answered
// from, including absent files tracked by the plugin host. A directory of Go files is
// asked about, and the plugin finds no go.mod there; when one comes,
// the plugin is asked again and the module is found.
func TestAModuleThatComesWhereThePluginLookedIsFound(t *testing.T) {
	workspace := oneModule(t)
	write(t, workspace, "tools/gen/main.go", "package main\n\nfunc main() {}\n")
	c, graph := serveWithGraph(t, configuration{Workspace: workspace, Mounted: true, Executor: asking{edited: &sync.Once{}}})

	if got := projectsOf(t, graph); fmt.Sprint(got) != "[hello]" {
		t.Fatalf("the control: the projects are %v; want only hello", got)
	}
	if run := wait(t, c.runs, create(t, c.runs).GetId()); run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		t.Fatalf("the first run ended %s: %s", run.GetExitCode(), run.GetFailure())
	}

	write(t, workspace, "tools/gen/go.mod", "module example.com/gen\n\ngo 1.27\n")

	if got := projectsOf(t, graph); fmt.Sprint(got) != "[hello tools/gen]" {
		t.Errorf("with a go.mod come where the plugin had looked for one, the projects are %v; want hello and tools/gen", got)
	}
	run := wait(t, c.runs, create(t, c.runs).GetId())
	if _, built := actions(t, c, run)["//tools/gen:go.compile"]; !built {
		t.Errorf("the run after did not build the module that came: %v", run.GetCounts())
	}
}

// An answer that is asked for in every evaluation is kept
// (answers_internal_test.go: "one that is asked for is kept"): however
// many builds there are with nothing changed, no plugin is asked and
// what the repository says is not read again.
func TestAnAnswerAskedForInEveryBuildIsKept(t *testing.T) {
	workspace := oneModule(t)
	settled(t, workspace)
	recorder, signals := recording(t)
	c := serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: asking{edited: &sync.Once{}}, Telemetry: signals})
	for build := 1; build <= 12; build++ {
		run := wait(t, c.runs, create(t, c.runs).GetId())
		if run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
			t.Fatalf("build %d ended %s: %s", build, run.GetExitCode(), run.GetFailure())
		}
		if cost := costOf(t, recorder); build > 1 && (cost.asked != 0 || cost.readEvaluating != 0) {
			t.Errorf("build %d, with nothing changed, cost %+v; want no plugin asked and no file read", build, cost)
		}
	}
}

// A run cancelled while its plugins are being asked leaves the daemon as
// it was: the next run builds everything.
func TestARunCancelledWhileEvaluatingLeavesTheNextOneWhole(t *testing.T) {
	workspace := oneModule(t)
	for i := range 300 {
		write(t, workspace, fmt.Sprintf("m%d/go.mod", i), fmt.Sprintf("module example.com/m%d\n\ngo 1.27\n", i))
		write(t, workspace, fmt.Sprintf("m%d/main.go", i), "package main\n\nfunc main() {}\n")
	}
	c, graph := serveWithGraph(t, configuration{Workspace: workspace, Mounted: true, Executor: asking{edited: &sync.Once{}}})

	for range 5 {
		going := create(t, c.runs)
		if _, err := c.runs.CancelBuild(context.Background(), &buildv1.CancelBuildRequest{BuildId: going.GetId()}); err != nil {
			t.Fatal(err)
		}
		wait(t, c.runs, going.GetId())
	}
	// A question of the graph whose asker goes away.
	for range 3 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = graph.ListProjects(ctx, &buildv1.ListProjectsRequest{})
		}()
		cancel()
		<-done
	}

	run := wait(t, c.runs, create(t, c.runs).GetId())
	if run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || run.GetCounts().GetPassed() != 903 {
		t.Errorf("the run after ended %s with %v: %s; want SUCCESS and 903 actions passed", run.GetExitCode(), run.GetCounts(), run.GetFailure())
	}
	if got := projectsOf(t, graph); len(got) != 301 {
		t.Errorf("the graph holds %d projects; want 301", len(got))
	}
}

// What a plugin refused is not kept, and what it answered before the
// file was broken is not given for the broken file.
func TestABrokenManifestIsRefusedThoughItWasAnsweredForBefore(t *testing.T) {
	workspace := oneModule(t)
	asked := &ran{}
	c := serve(t, asking{edited: &sync.Once{}, ran: asked}, workspace, true)
	good := "module example.com/hello\n\ngo 1.27\n"

	if run := wait(t, c.runs, create(t, c.runs).GetId()); run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		t.Fatalf("the first run ended %s: %s", run.GetExitCode(), run.GetFailure())
	}
	write(t, workspace, "hello/go.mod", good+"\nnonsense here\n")
	for range 2 {
		if run := wait(t, c.runs, create(t, c.runs).GetId()); run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_BUILD_FAILURE || !strings.Contains(run.GetFailure(), "hello/go.mod") {
			t.Errorf("with a broken go.mod the run ended %s: %s; want BUILD_FAILURE naming the file", run.GetExitCode(), run.GetFailure())
		}
	}
	asked.since()
	write(t, workspace, "hello/go.mod", good)
	if run := wait(t, c.runs, create(t, c.runs).GetId()); run.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		t.Errorf("mended, the run ended %s: %s", run.GetExitCode(), run.GetFailure())
	}
}

// An option that changes changes what the plugin is asked, and so the
// actions; set back, the actions are what they were.
func TestAnOptionThatChangesChangesTheActions(t *testing.T) {
	workspace := twoModules(t)
	asked := &ran{}
	c := serve(t, asking{edited: &sync.Once{}, ran: asked}, workspace, true)
	built := func() []string {
		t.Helper()
		succeeded(t, c, create(t, c.runs))
		return asked.since()
	}
	if first := built(); len(first) != 6 {
		t.Fatalf("the first run asked the grid about %v", first)
	}
	// An entity sets a tag for one module: the two of its actions that
	// build with tags change; format takes none, and is not asked again.
	write(t, workspace, "hello/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: hello\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    plugins:\n      go:\n        tags: [integration]\n")
	if second := built(); fmt.Sprint(second) != "[//hello:go.compile //hello:go.lint]" {
		t.Errorf("with a tag set for hello, the grid was asked about %v; want the two of hello's actions that build with tags", second)
	}
	// The repository sets another tag: the module no entity sets one
	// for changes, and hello keeps what its entity set.
	write(t, workspace, "frontseat.yaml", "plugins:\n  go:\n    tags: [slow]\n")
	if third := built(); fmt.Sprint(third) != "[//other:go.compile //other:go.lint]" {
		t.Errorf("with a tag set for the repository, the grid was asked about %v; want the two of other's actions that build with tags", third)
	}
	// Both taken away: every action is what it was in the first run, and
	// the grid answered for those.
	write(t, workspace, "frontseat.yaml", "plugins:\n  go: {}\n")
	write(t, workspace, "hello/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: hello\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n")
	if fourth := built(); len(fourth) != 0 {
		t.Errorf("with the options as they were at first, the grid was asked about %v; want nothing", fourth)
	}
}

// keeping is a grid that keeps as a grid does: what an action left is
// content it holds, an action handed content it no longer holds is not
// run, and what it answered before it answers again only while it holds
// everything that answer names.
type keeping struct {
	asking

	mu  sync.Mutex
	cas map[string]bool
	ac  map[string]exec.Result
	ran *ran
}

func (k *keeping) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	key := fmt.Sprint(action.Label, action.Arguments, action.Held)
	if !strings.HasPrefix(action.Label, "mise install ") {
		var err error
		if key, err = k.Key(ctx, action); err != nil {
			return exec.Result{}, err
		}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for at, held := range action.Held {
		if !k.cas[held.Digest.Hash] {
			return exec.Result{}, fmt.Errorf("%s: %s: %w", action.Label, at, exec.ErrNotKept)
		}
	}
	if done, kept := k.ac[key]; kept {
		whole := true
		for _, left := range done.Outputs {
			whole = whole && k.cas[left.Digest.Hash]
		}
		if whole {
			done.Cached = true
			return done, nil
		}
	}
	k.ran.note(action.Label)
	left := map[string]exec.Held{}
	for _, output := range action.Outputs {
		sum := sha256.Sum256([]byte(key + output))
		hash := hex.EncodeToString(sum[:])
		left[output] = exec.Held{Digest: exec.Digest{Hash: hash, Size: 1}, Directory: true}
		k.cas[hash] = true
	}
	done := exec.Result{Outputs: left, Stdout: []byte("ok\n")}
	if toolstest.Asks(action) {
		done.Stdout = toolstest.Environment(action).Stdout
	}
	k.ac[key] = done
	return done, nil
}

// lose has the grid let go of all the content it holds.
func (k *keeping) lose() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.cas = map[string]bool{}
}

// An action whose answer the daemon kept left something a later action
// needs, and the grid has let it go. The daemon does not ask about the
// first; the second is handed what is gone. The run starts over, the
// grid makes it again, and the run passes.
func TestWhatARememberedActionLeftIsMadeAgainWhenALaterActionNeedsIt(t *testing.T) {
	workspace := oneModule(t)
	write(t, workspace, "hello/go.mod", "module example.com/hello\n\ngo 1.27\n\nrequire example.com/dep v1.0.0\n")
	write(t, workspace, "hello/go.sum", "example.com/dep v1.0.0 h1:abc=\nexample.com/dep v1.0.0/go.mod h1:def=\n")
	grid := &keeping{asking: asking{edited: &sync.Once{}}, cas: map[string]bool{}, ac: map[string]exec.Result{}, ran: &ran{}}
	c := serve(t, grid, workspace, true)
	built := func() (*buildv1.Build, []string) {
		t.Helper()
		return succeeded(t, c, create(t, c.runs)), grid.ran.since()
	}
	first, asked := built()
	if first.GetCounts().GetPassed() != 4 {
		t.Fatalf("the first run passed %v, having asked %v; want fetch, lint, format and compile", first.GetCounts(), asked)
	}

	// The control: nothing let go, a file changed: no start over.
	write(t, workspace, "hello/main.go", "package main\n\nfunc main() { println(1) }\n")
	second, asked := built()
	if over, _ := startedOver(t, c, second.GetId()); len(over) != 0 || fmt.Sprint(asked) != "[//hello:go.compile //hello:go.format //hello:go.lint]" {
		t.Fatalf("the control: started over %d times and asked %v", len(over), asked)
	}

	grid.lose()
	write(t, workspace, "hello/main.go", "package main\n\nfunc main() { println(2) }\n")
	third, asked := built()
	over, _ := startedOver(t, c, third.GetId())
	if len(over) != 1 || third.GetCounts().GetPassed() != 4 || third.GetCounts().GetErrored() != 0 {
		t.Errorf("with what fetch left let go: started over %d times, %v, asked %v; want one start over and four passed", len(over), third.GetCounts(), asked)
	}
	fetched := false
	for _, label := range asked {
		fetched = fetched || strings.Contains(label, ":go.fetch/")
	}
	if !fetched {
		t.Errorf("the grid was asked about %v; want fetch run again", asked)
	}

	// And with nothing changed after the grid let go again: nothing needs
	// what is gone, and nothing is asked.
	grid.lose()
	if _, asked := built(); len(asked) > 2 {
		t.Logf("with nothing changed and everything let go, the grid ran %v", asked)
	}
}

// What the plugins answered for one profile is kept while the graph is
// asked about: a portal or an editor that lists the projects between
// two builds does not have the second build ask every plugin again.
func TestAskingTheGraphDoesNotLetGoOfWhatABuildOfAnotherProfileLearned(t *testing.T) {
	workspace := oneModule(t)
	recorder, signals := recording(t)
	c, graph := serveWithGraph(t, configuration{Workspace: workspace, Mounted: true, Executor: asking{edited: &sync.Once{}}, Telemetry: signals})
	shipped := func() cost {
		t.Helper()
		succeeded(t, c, createAs(t, c.runs, typev1.Profile_PROFILE_PRODUCTION))
		return costOf(t, recorder)
	}
	first := shipped()
	// The control: built again at once, no plugin is asked.
	if again := shipped(); again.asked != 0 {
		t.Fatalf("the control: built again, a run cost %+v", again)
	}
	for range 9 {
		projectsOf(t, graph)
	}
	if after := shipped(); after.asked != 0 {
		t.Errorf("after the graph was asked about nine times, with nothing changed, a build cost %+v; want no plugin asked (the first cost %+v)", after, first)
	}
}

// On the real grid, with the grid's own Key: a second build asks the
// grid only where its tools are, and a file changed has the grid asked
// about the actions that read it.
func TestOnTheRealGridARunAsksOnlyAboutWhatChanged(t *testing.T) {
	grid := reapitest.Dial(t)
	workspace := oneModule(t)
	unique := fmt.Sprint(time.Now().UnixNano())
	write(t, workspace, "hello/main.go", "package main\n\nfunc main() { println(\""+unique+"\") }\n")
	write(t, workspace, "other/go.mod", "module example.com/other\n\ngo 1.27\n")
	write(t, workspace, "other/main.go", "package main\n\nfunc main() { println(\"other "+unique+"\") }\n")
	recorder, signals := recording(t)
	c := serveAs(t, configuration{Workspace: workspace, Mounted: true, Executor: grid, Telemetry: signals})
	seen := 0
	built := func() (asked []string, run *buildv1.Build) {
		t.Helper()
		run = succeeded(t, c, create(t, c.runs))
		ended := recorder.Ended()
		for _, span := range ended[seen:] {
			if span.Name() != "grid" {
				continue
			}
			for _, attribute := range span.Attributes() {
				if attribute.Key == "label" {
					asked = append(asked, attribute.Value.AsString())
				}
			}
		}
		seen = len(ended)
		sort.Strings(asked)
		return asked, run
	}
	first, _ := built()
	t.Logf("the first run asked the grid about %v", first)
	second, run := built()
	for _, label := range second {
		if strings.HasPrefix(label, "//") {
			t.Errorf("with nothing changed the grid was asked about %s", label)
		}
	}
	for label, action := range actions(t, c, run) {
		if action.GetProvenance() != buildv1.Provenance_PROVENANCE_GRID_CACHE {
			t.Errorf("%s came from %s", label, action.GetProvenance())
		}
	}
	write(t, workspace, "hello/main.go", "package main\n\nfunc main() { println(\"again "+unique+"\") }\n")
	third, _ := built()
	var of []string
	for _, label := range third {
		if strings.HasPrefix(label, "//") {
			of = append(of, label)
		}
	}
	if fmt.Sprint(of) != "[//hello:go.compile //hello:go.format //hello:go.lint]" {
		t.Errorf("with a file of hello changed the grid was asked about %v; want hello's three actions", of)
	}
}
