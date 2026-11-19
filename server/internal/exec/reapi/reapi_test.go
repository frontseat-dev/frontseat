package reapi_test

import (
	"context"
	"errors"
	"github.com/frontseat-dev/frontseat/server/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/confine"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/reapi"
	"github.com/frontseat-dev/frontseat/server/internal/exec/reapi/reapitest"
	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
)

// dial is a client of the test grid.
func dial(t *testing.T) confining {
	t.Helper()
	return confining{reapitest.Dial(t)}
}

// confining is the grid, asked to run each action as frontseat's daemon
// asks it: its command begun by the confiner, its spec and the way up to
// its root (confine.Arguments). The grid's runner runs no other command.
type confining struct{ *reapi.Grid }

// confined is an action's command as the daemon writes it.
func confined(action exec.Action) exec.Action {
	spec, err := confine.Encode(confine.Spec{Home: ".frontseat/home", Temporary: ".frontseat/tmp"})
	if err != nil {
		panic(err)
	}
	up := "."
	if dir := path.Clean(action.WorkingDirectory); action.WorkingDirectory != "" && dir != "." {
		up = strings.TrimSuffix(strings.Repeat("../", strings.Count(dir, "/")+1), "/")
	}
	action.Arguments = confine.Arguments(spec, up, action.Arguments)
	return action
}

func (c confining) Execute(ctx context.Context, action exec.Action) (exec.Result, error) {
	return c.Grid.Execute(ctx, confined(action))
}

func (c confining) Key(ctx context.Context, action exec.Action) (string, error) {
	return c.Grid.Key(ctx, confined(action))
}

// root is a directory of inputs no earlier test run had, so that what the
// grid kept from one is not what this one is answered with.
func root(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["nonce"] = strconv.FormatInt(time.Now().UnixNano(), 10)
	testfiles.Tree(t, dir, files)
	return dir
}

// succeedsSaying fails the test unless the grid runs the action to an
// exit of 0, and what it printed holds said.
func succeedsSaying(t *testing.T, grid confining, action exec.Action, said string) {
	t.Helper()
	done, err := grid.Execute(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	if done.ExitCode != 0 || !strings.Contains(string(done.Stdout), said) {
		t.Errorf("exit %d, stdout %q, stderr %q", done.ExitCode, done.Stdout, done.Stderr)
	}
}

func shell(dir, script string) exec.Action {
	return exec.Action{
		Label:     "//test:shell",
		Root:      dir,
		Inputs:    []string{"."},
		Arguments: []string{"/bin/sh", "-c", script},
		Platform:  testgrid.Platform(),
	}
}

func TestAnActionRunsOnTheGridOnItsInputs(t *testing.T) {
	grid := dial(t)
	action := shell(root(t, map[string]string{"module/greeting.txt": "hello from the workspace\n"}), "cat greeting.txt; echo to stderr >&2")
	action.WorkingDirectory = "module"

	result, err := grid.Execute(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Cached {
		t.Errorf("exit %d, cached %v; want it executed and passed", result.ExitCode, result.Cached)
	}
	if got := string(result.Stdout); got != "hello from the workspace\n" {
		t.Errorf("stdout = %q", got)
	}
	if got := string(result.Stderr); got != "to stderr\n" {
		t.Errorf("stderr = %q", got)
	}
}

func TestTheSameActionIsAnsweredFromWhatTheGridKept(t *testing.T) {
	grid := dial(t)
	action := shell(root(t, map[string]string{"a.txt": "a\n"}), "cat a.txt")

	first, err := grid.Execute(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	second, err := grid.Execute(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached || !second.Cached {
		t.Errorf("cached: first %v, second %v; want the first executed and the second kept", first.Cached, second.Cached)
	}
	if string(second.Stdout) != "a\n" {
		t.Errorf("the kept answer printed %q", second.Stdout)
	}
	t.Logf("executed in %s, answered from the cache in %s", first.Duration.Round(time.Millisecond), second.Duration.Round(time.Millisecond))
}

func TestAnActionThatFailsIsAResult(t *testing.T) {
	grid := dial(t)

	result, err := grid.Execute(context.Background(), shell(root(t, map[string]string{}), "echo broken >&2; exit 3"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 3 || string(result.Stderr) != "broken\n" {
		t.Errorf("exit %d, stderr %q", result.ExitCode, result.Stderr)
	}
}

func TestADirectoryAnActionLeftIsATreeOfTheNext(t *testing.T) {
	grid := dial(t)
	makes := shell(root(t, map[string]string{}), "mkdir -p out/bin && cp nonce out/bin/tool")
	makes.Outputs = []string{"out"}
	made, err := grid.Execute(context.Background(), makes)
	if err != nil {
		t.Fatal(err)
	}
	tree, ok := made.Outputs["out"]
	if !ok {
		t.Fatalf("outputs = %v, stderr %s", made.Outputs, made.Stderr)
	}

	dir := root(t, map[string]string{"src/main.txt": "source\n"})
	uses := shell(dir, "cat src/main.txt && test -s .tools/kept/bin/tool && echo found the tool")
	uses.Held = map[string]exec.Held{".tools/kept": tree}
	succeedsSaying(t, grid, uses, "found the tool")
}

func TestWhatAnActionLeftSitsBesideTheInputsOfTheNext(t *testing.T) {
	grid := dial(t)
	makes := shell(root(t, map[string]string{}), "mkdir -p out/bin && cp nonce out/bin/tool && cp nonce word.txt")
	makes.Outputs = []string{"out", "word.txt"}
	made, err := grid.Execute(context.Background(), makes)
	if err != nil {
		t.Fatal(err)
	}
	if len(made.Outputs) != 2 || !made.Outputs["out"].Directory || made.Outputs["word.txt"].Directory {
		t.Fatalf("outputs = %v, stderr %s", made.Outputs, made.Stderr)
	}

	dir := root(t, map[string]string{"src/main.txt": "source\n", "src/deep/other.txt": "other\n"})
	uses := shell(dir, "cat src/main.txt src/deep/other.txt && test -s src/made/bin/tool && test -s src/deep/word.txt && echo found both")
	uses.Held = map[string]exec.Held{"src/made": made.Outputs["out"], "src/deep/word.txt": made.Outputs["word.txt"]}
	succeedsSaying(t, grid, uses, "found both")
}

func TestWhatIsHeldDoesNotSitWhereAnInputDoes(t *testing.T) {
	grid := dial(t)
	for _, at := range []string{"src", "src/main.txt", "src/main.txt/below"} {
		action := shell(root(t, map[string]string{"src/main.txt": "source\n"}), "true")
		action.Held = map[string]exec.Held{at: {Digest: exec.Digest{Hash: strings.Repeat("0", 64), Size: 1}, Directory: true}}

		if _, err := grid.Execute(context.Background(), action); err == nil || !strings.Contains(err.Error(), "sits where an input does") {
			t.Errorf("held at %s: err = %v", at, err)
		}
	}
}

// What the grid already holds is not sent to it again, and what it
// already ran is not run again: the second time, an action costs what
// it costs to ask.
func TestAnActionTheGridKeptSendsNothingAndRunsNothing(t *testing.T) {
	grid := dial(t)
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	dir := root(t, map[string]string{"word.txt": time.Now().String()})
	action := shell(dir, "cat word.txt")
	action.Inputs = []string{"word.txt"}
	did := func() map[string]sdktrace.ReadOnlySpan {
		t.Helper()
		before := len(recorder.Ended())
		ctx, run := telemetry.Root(context.Background(), provider, "run")
		if _, err := grid.Execute(ctx, action); err != nil {
			t.Fatal(err)
		}
		telemetry.End(run, nil)
		named := map[string]sdktrace.ReadOnlySpan{}
		for _, span := range recorder.Ended()[before:] {
			named[span.Name()] = span
		}
		return named
	}
	missing := func(span sdktrace.ReadOnlySpan) int64 {
		for _, attribute := range span.Attributes() {
			if attribute.Key == "missing" {
				return attribute.Value.AsInt64()
			}
		}
		return -1
	}

	first, second := did(), did()

	// The control: the first time, it was sent and it was run.
	if first["grid.run"] == nil || missing(first["grid.send"]) < 1 {
		t.Fatalf("the first time it sent %d blobs and ran %v, so this test proves nothing", missing(first["grid.send"]), first["grid.run"] != nil)
	}
	if second["grid.run"] != nil {
		t.Error("the second time it was run again")
	}
	if sent := missing(second["grid.send"]); sent != 0 {
		t.Errorf("the second time %d blobs were sent, want none", sent)
	}
}

// When an action ran and for how long is what its worker says, and
// only of the run that was made for this answer.
func TestTheWorkerSaysWhenItRanAnAction(t *testing.T) {
	grid := dial(t)
	before := time.Now().Add(-time.Minute)
	action := shell(root(t, map[string]string{"word.txt": time.Now().String()}), "cat word.txt")
	action.Inputs = []string{"word.txt"}

	ran, err := grid.Execute(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := grid.Execute(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}

	if ran.Cached || ran.Started.Before(before) || ran.Started.After(time.Now().Add(time.Minute)) || ran.Worked <= 0 || ran.Worked > ran.Duration {
		t.Errorf("run for this answer, it started at %v and worked %v of the %v it took to answer; want a start just now and a time within the whole", ran.Started, ran.Worked, ran.Duration)
	}
	if !kept.Cached || !kept.Started.IsZero() || kept.Worked != 0 {
		t.Errorf("answered from what was kept, it started at %v and worked %v; want neither", kept.Started, kept.Worked)
	}
}

// The image an action runs in is part of what the action is. Asked of
// another image, or of none, it is another action: no worker here runs
// it, and it is not answered from what this one left.
func TestAnActionIsOfTheImageItRunsIn(t *testing.T) {
	grid := dial(t)
	action := shell(root(t, map[string]string{}), "cat nonce")
	image := action.Platform["container-image"]
	if !strings.HasPrefix(image, "docker://") || !strings.Contains(image, "@sha256:") {
		t.Fatalf("the grid's platform names the image %q; want docker://<repository>@sha256:<digest>", image)
	}
	ran, err := grid.Execute(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	if ran.ExitCode != 0 || ran.Cached {
		t.Fatalf("exit %d, cached %v; want it executed and passed", ran.ExitCode, ran.Cached)
	}

	for name, image := range map[string]string{
		"another image": "docker://example.com/another@sha256:" + strings.Repeat("0", 64),
		"no image":      "",
	} {
		t.Run(name, func(t *testing.T) {
			other := action
			other.Platform = map[string]string{}
			for name, value := range action.Platform {
				other.Platform[name] = value
			}
			delete(other.Platform, "container-image")
			if image != "" {
				other.Platform["container-image"] = image
			}
			// A grid that had such workers a while ago holds the action
			// for them rather than refusing it; that is not an answer.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			answered, err := grid.Execute(ctx, other)
			if err == nil {
				t.Fatalf("answered with exit %d, cached %v; want it refused, there being no worker of that image", answered.ExitCode, answered.Cached)
			}
			if !strings.Contains(err.Error(), "No workers exist") {
				t.Errorf("refused with %v; want the grid to say it has no such worker", err)
			}
		})
	}
}

// A grid may let go of what it kept. An action handed something the grid
// no longer holds was not run, and is told apart from one that failed:
// whoever handed it over can have it made again.
func TestAnActionHandedWhatTheGridLetGoSaysSo(t *testing.T) {
	grid := dial(t)
	for name, held := range map[string]exec.Held{
		"a directory": {Digest: exec.Digest{Hash: strings.Repeat("ab", 32), Size: 142}, Directory: true},
		"a file":      {Digest: exec.Digest{Hash: strings.Repeat("cd", 32), Size: 142}},
	} {
		t.Run(name, func(t *testing.T) {
			action := shell(root(t, map[string]string{}), "ls -R gone; cat gone")
			action.Held = map[string]exec.Held{"gone": held}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			result, err := grid.Execute(ctx, action)

			if !errors.Is(err, exec.ErrNotKept) {
				t.Errorf("exit %d, stderr %q, err %v; want it said that the grid keeps nothing under what the action was handed", result.ExitCode, result.Stderr, err)
			}
		})
	}
}
