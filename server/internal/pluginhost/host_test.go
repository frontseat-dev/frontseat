package pluginhost_test

import (
	"context"
	"errors"
	"fmt"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
)

// The Go plugin, as the wasm module the tests load.
var module []byte

func TestMain(m *testing.M) {
	module = testmodule.PluginOrExit("go")
	os.Exit(m.Run())
}

// files is a repository held in memory, read as pluginhosttest.Files is
// read; a type of this package's, so that its tests may add to what it
// answers (same_test.go).
type files map[string]string

func (f files) Read(ctx context.Context, path string) ([]byte, bool, error) {
	return pluginhosttest.Files(f).Read(ctx, path)
}

func (f files) Glob(ctx context.Context, directory string, patterns []string) ([]string, error) {
	return pluginhosttest.Files(f).Glob(ctx, directory, patterns)
}

const goMod = "module example.com/hello\n\ngo 1.26\n"

func load(t *testing.T) *pluginhost.Plugin {
	t.Helper()
	plugin, err := loadModule(t, module)
	if err != nil {
		t.Fatal(err)
	}
	return plugin
}

func TestAModuleSaysWhatItIsWhenItIsLoaded(t *testing.T) {
	plugin := load(t)

	if got := plugin.Description.GetName(); got != "go" {
		t.Errorf("name = %q, want go", got)
	}
	if got := plugin.Description.GetInterests(); !slices.Contains(got, "**/go.mod") {
		t.Errorf("interests = %v, want **/go.mod among them", got)
	}
	var options []string
	for _, option := range plugin.Description.GetOptions() {
		options = append(options, option.GetName())
	}
	if !slices.Equal(options, []string{"test-timeout", "test-shards", "tags", "version"}) {
		t.Errorf("options = %v, want test-timeout, test-shards and tags", options)
	}
}

func TestAnalyzeCrossesTheBoundaryAndTheHostSeesWhatWasRead(t *testing.T) {
	plugin := load(t)
	source := files{"services/hello/go.mod": goMod, "services/hello/main.go": "package main\n", "services/hello/main_test.go": "package main\n"}
	roots := []string{"services/hello"}

	got := &pluginv1.AnalyzeResponse{}
	reads, err := plugin.Call(context.Background(), source, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext(), Roots: roots}, got)
	if err != nil {
		t.Fatal(err)
	}
	if err := pluginhost.CheckAnalysis("go", roots, got); err != nil {
		t.Errorf("the answer breaks the contract: %v", err)
	}
	project := got.GetAnalyses()[0].GetProjects()[0]
	if project.GetName() != "example.com/hello" || len(project.GetActions()) != 6 {
		t.Errorf("project %q with %d actions, want example.com/hello with 6: three of a build, its tests' program and its one test, and its one program", project.GetName(), len(project.GetActions()))
	}
	if len(reads) != 6 || reads[0].Import != "read" || reads[0].Argument != "services/hello/go.mod" || !reads[0].Found || reads[0].Digest == "" {
		t.Fatalf("reads = %+v, want six, the read of the go.mod first, found, with its digest", reads)
	}
	// That a file was not there is something the answer depended on too.
	if reads[1].Argument != "services/hello/go.sum" || reads[1].Found {
		t.Errorf("reads = %+v, want the read of the go.sum second, not found", reads)
	}
	// Which modules are nested in this one, which Go files there are,
	// and what each embeds, decide what the module is built from.
	if reads[2].Import != "glob" || reads[3].Import != "glob" || reads[4].Import != "read" || reads[4].Argument != "services/hello/main.go" || !reads[4].Found || reads[5].Argument != "services/hello/main_test.go" || !reads[5].Found {
		t.Errorf("reads = %+v, want the nested modules and the module's Go files asked for and then its two Go files read", reads)
	}
	// A compile may leave the module's programs, what builds its tests
	// leaves their program, and nothing else leaves anything.
	for _, action := range project.GetActions() {
		outputs := action.GetOutputs()
		if action.GetName() == "test-build" {
			if len(outputs) != 1 || outputs[0].GetPath() != "_tests/0/hello.test" || !outputs[0].GetOptional() {
				t.Errorf("%s leaves %v; want the program of the module's one tested package", action.GetName(), outputs)
			}
			continue
		}
		if compiles := action.GetName() == "compile"; compiles != (len(outputs) == 1) || compiles && (outputs[0].GetPath() != "~/go/bin/" || !outputs[0].GetOptional()) {
			t.Errorf("%s leaves %v; want the compile alone to leave ~/go/bin/, where it has programs to", action.GetName(), outputs)
		}
	}
}

// A go.sum of one module the build is made with, and of the go.mod of
// another the go command read to choose it.
const goSum = "example.com/dep v1.2.3 h1:AAAA=\nexample.com/dep v1.2.3/go.mod h1:BBBB=\nexample.com/other v0.9.0/go.mod h1:CCCC=\n"

func TestAModuleWithALockFetchesEachModuleTheLockHoldsOnItsOwn(t *testing.T) {
	plugin := load(t)
	source := files{"go.mod": goMod, "go.sum": goSum}

	got := &pluginv1.AnalyzeResponse{}
	if _, err := plugin.Call(context.Background(), source, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext()}, got); err != nil {
		t.Fatal(err)
	}
	if err := pluginhost.CheckAnalysis("go", nil, got); err != nil {
		t.Errorf("the answer breaks the contract: %v", err)
	}
	actions := got.GetAnalyses()[0].GetProjects()[0].GetActions()
	want := []struct{ name, command, sum, kept string }{
		{"fetch/example.com/dep@v1.2.3", "go mod download -x example.com/dep@v1.2.3", "example.com/dep v1.2.3 h1:AAAA=\nexample.com/dep v1.2.3/go.mod h1:BBBB=\n",
			"~/go/pkg/mod/cache/download/example.com/dep/@v/v1.2.3.info ~/go/pkg/mod/cache/download/example.com/dep/@v/v1.2.3.mod ~/go/pkg/mod/cache/download/example.com/dep/@v/v1.2.3.zip ~/go/pkg/mod/cache/download/example.com/dep/@v/v1.2.3.ziphash ~/go/pkg/mod/example.com/dep@v1.2.3/"},
		{"metadata/example.com/other@v0.9.0", "go list -x -m -json example.com/other@v0.9.0", "example.com/other v0.9.0/go.mod h1:CCCC=\n",
			"~/go/pkg/mod/cache/download/example.com/other/@v/v0.9.0.info ~/go/pkg/mod/cache/download/example.com/other/@v/v0.9.0.mod"},
	}
	for i, w := range want {
		fetch := actions[i]
		if fetch.GetName() != w.name || fetch.GetSharedFetch() == nil {
			t.Fatalf("action %d is %v, want the shared fetch %s", i, fetch, w.name)
		}
		if got := strings.Join(fetch.GetCommand().GetArguments(), " "); got != w.command {
			t.Errorf("%s runs %q, want %q: its one module", w.name, got, w.command)
		}
		if len(fetch.GetInputs()) != 0 {
			t.Errorf("%s reads %v of the project, want nothing", w.name, fetch.GetInputs())
		}
		told := map[string]string{}
		for _, file := range fetch.GetSharedFetch().GetFiles() {
			told[file.GetPath()] = string(file.GetContent())
		}
		if told["go.sum"] != w.sum || told["go.mod"] != "module fetch.invalid\n\ngo 1.21\n" || len(told) != 2 {
			t.Errorf("%s is told %q, want its own lines of the lock and a module of its own", w.name, told)
		}
		var kept []string
		for _, output := range fetch.GetOutputs() {
			kept = append(kept, output.GetPath())
		}
		// Its own module at its own version, and nothing the cache keeps
		// of the module at every version, which another fetch also writes.
		if got := strings.Join(kept, " "); got != w.kept {
			t.Errorf("%s leaves %s, want %s", w.name, got, w.kept)
		}
	}
	for _, action := range actions[len(want):] {
		for _, w := range want {
			if !slices.ContainsFunc(action.GetInputs(), func(input *pluginv1.Input) bool { return input.GetOutput() == w.name }) {
				t.Errorf("%s reads %v, want all that %s left", action.GetName(), action.GetInputs(), w.name)
			}
		}
		if action.GetCommand().GetEnvironment()["GOPROXY"] != "off" {
			t.Errorf("%s is not told it reaches nothing", action.GetName())
		}
	}
}

func TestAModuleWithNoLockFetchesNothing(t *testing.T) {
	plugin := load(t)

	got := &pluginv1.AnalyzeResponse{}
	if _, err := plugin.Call(context.Background(), files{"go.mod": goMod}, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext()}, got); err != nil {
		t.Fatal(err)
	}
	for _, action := range got.GetAnalyses()[0].GetProjects()[0].GetActions() {
		for _, input := range action.GetInputs() {
			if action.GetFetch() != nil || action.GetSharedFetch() != nil || input.GetOutput() != "" {
				t.Errorf("%s fetches or reads a fetch, and the module has no lock", action.GetName())
			}
		}
	}
}

// What an action names of other actions is one thing of its own project:
// an action that leaves something, or one output of one. The control is
// each allowed form: an action by name, a shared one by its whole name,
// and an output as declared.
func TestWhatAnActionReadsOfOthersIsOneThingOfItsProject(t *testing.T) {
	answer := func(inputs ...string) *pluginv1.AnalyzeResponse {
		// Written here as a catalog file writes them, for brevity; a
		// plugin says each as a message of its kind.
		translated := make([]*pluginv1.Input, 0, len(inputs))
		for _, input := range inputs {
			switch {
			case strings.HasPrefix(input, ":"):
				translated = append(translated, pluginhosttest.OutputInput(strings.TrimPrefix(input, ":")))
			case strings.HasPrefix(input, "//"):
				translated = append(translated, pluginhosttest.OutputInput(input))
			case strings.HasPrefix(input, "="):
				translated = append(translated, pluginhost.SetInput(strings.TrimPrefix(input, "=")))
			default:
				translated = append(translated, pluginhost.SourceInput(input))
			}
		}
		return &pluginv1.AnalyzeResponse{Analyses: []*pluginv1.Analysis{{Projects: []*pluginv1.Project{{Root: "a", Name: "a", Kind: "go-module", Manifest: "a/go.mod",
			InputSets: []*pluginv1.InputSet{{Name: "sources", Inputs: []*pluginv1.Input{pluginhost.SourceInput("**/*.go")}}},
			Actions: []*pluginv1.Action{
				{Name: "fetch", Command: &pluginv1.Command{Arguments: []string{"true"}}, Kind: &pluginv1.Action_Fetch{Fetch: &pluginv1.Fetch{}}, Outputs: []*pluginv1.Output{{Path: "~/go/pkg/mod/"}}},
				{Name: "fetch/example.com/dep@v1.0.0", Command: &pluginv1.Command{Arguments: []string{"true"}}, Kind: &pluginv1.Action_SharedFetch{SharedFetch: &pluginv1.SharedFetch{}}, Outputs: []*pluginv1.Output{{Path: "~/go/pkg/mod/example.com/dep@v1.0.0/"}}},
				{Name: "test", Command: &pluginv1.Command{Arguments: []string{"true"}}, Kind: &pluginv1.Action_Test{Test: &pluginv1.Test{}}},
				{Name: "both", Command: &pluginv1.Command{Arguments: []string{"true"}}, Outputs: []*pluginv1.Output{{Path: "test"}}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
				{Name: "compile", Command: &pluginv1.Command{Arguments: []string{"true"}}, Inputs: translated, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}}},
			}}}}}}
	}
	for _, allowed := range [][]string{{":fetch"}, {":fetch/example.com/dep@v1.0.0"}, {":~/go/pkg/mod/"}, {"=sources", ":fetch", ":~/go/pkg/mod/example.com/dep@v1.0.0/"}} {
		if err := pluginhost.CheckAnalysis("go", nil, answer(allowed...)); err != nil {
			t.Errorf("%v was refused: %v", allowed, err)
		}
	}
	for input, says := range map[string]string{
		":lint":           `reads ":lint", which names no action of the project and nothing one leaves`,
		":test":           `reads ":test", which names both an action and what an action leaves`,
		":test/":          `reads ":test/", which names both an action and what an action leaves`,
		":":               `reads ":", which names no action of the project and nothing one leaves`,
		"//b:fetch":       `reads "//b:fetch", which names another project`,
		":~/go/pkg/mod/x": `reads ":~/go/pkg/mod/x", which names no action of the project and nothing one leaves`,
	} {
		err := pluginhost.CheckAnalysis("go", nil, answer(input))
		if err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: err = %v, want %q", input, err, says)
		}
	}
	// An action that leaves nothing cannot be read: there is no ordering
	// but what is read.
	nothing := answer(":test")
	nothing.GetAnalyses()[0].GetProjects()[0].Actions[3].Outputs = nil
	if err := pluginhost.CheckAnalysis("go", nil, nothing); err == nil || !strings.Contains(err.Error(), `reads ":test", which leaves nothing`) {
		t.Errorf("err = %v, want the test named as leaving nothing", err)
	}
	// An output two actions of the project leave names no one of them.
	twice := answer(":~/go/pkg/mod/")
	twice.GetAnalyses()[0].GetProjects()[0].Actions[3].Outputs = []*pluginv1.Output{{Path: "~/go/pkg/mod/"}}
	if err := pluginhost.CheckAnalysis("go", nil, twice); err == nil || !strings.Contains(err.Error(), `reads ":~/go/pkg/mod/", which more than one action of the project leaves`) {
		t.Errorf("err = %v, want the output two actions leave refused", err)
	}
	// A set is of source: a reference in one is refused.
	set := answer(":fetch")
	set.GetAnalyses()[0].GetProjects()[0].InputSets[0].Inputs = []*pluginv1.Input{pluginhost.SourceInput("**/*.go"), pluginhosttest.OutputInput("fetch")}
	if err := pluginhost.CheckAnalysis("go", nil, set); err == nil || !strings.Contains(err.Error(), `a set is of source`) {
		t.Errorf("err = %v, want the reference in a set refused", err)
	}
}

func TestAPluginToldWhatAProjectWasHandedWritesWhatItsToolNeeds(t *testing.T) {
	plugin := load(t)
	source := files{"services/hello/go.mod": goMod}

	got := &pluginv1.ArrangeResponse{}
	_, err := plugin.Call(context.Background(), source, "Arrange", &pluginv1.ArrangeRequest{
		Context: &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT, Options: goContext().Options, Tools: map[string]string{"go": "1.27.3"}},
		Root:    "services/hello",
		Actions: []string{"compile"},
		Handed: []*pluginv1.Handoff{
			{Coordinate: &pluginv1.Coordinate{Ecosystem: "go", Name: "example.com/greet"}, Into: ".frontseat/go/example.com/greet"},
			{Coordinate: &pluginv1.Coordinate{Ecosystem: "go", Name: "example.com/log"}, Into: ".frontseat/go/example.com/log"},
		},
	}, got)
	if err != nil {
		t.Fatal(err)
	}
	want := "go 1.27.3\n\nuse .\n\nreplace (\n\texample.com/greet => ./.frontseat/go/example.com/greet\n\texample.com/log => ./.frontseat/go/example.com/log\n)\n"
	if len(got.GetFiles()) != 1 || got.GetFiles()[0].GetPath() != "services/hello/go.work" || string(got.GetFiles()[0].GetContent()) != want {
		t.Errorf("files = %v, want the one go.work of the module and what it was handed:\n%s", got.GetFiles(), want)
	}
}

func TestAWorkspaceIsNotWrittenForAGoNobodyPinned(t *testing.T) {
	plugin := load(t)

	_, err := plugin.Call(context.Background(), files{}, "Arrange", &pluginv1.ArrangeRequest{
		Context: goContext(),
		Root:    "services/hello",
		Handed:  []*pluginv1.Handoff{{Coordinate: &pluginv1.Coordinate{Ecosystem: "go", Name: "example.com/greet"}, Into: ".frontseat/go/example.com/greet"}},
	}, &pluginv1.ArrangeResponse{})
	if err == nil || !strings.Contains(err.Error(), "pins no go") {
		t.Errorf("err = %v, want the plugin to say the repository pins no go", err)
	}
}

func TestAPluginsErrorArrivesWithItsCodeItsFileAndItsHelp(t *testing.T) {
	plugin := load(t)

	_, err := plugin.Call(context.Background(), files{"go.mod": "go 1.26\n"}, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext()}, &pluginv1.AnalyzeResponse{})

	var reported *pluginhost.Error
	if !errors.As(err, &reported) {
		t.Fatalf("err = %v, want the plugin's error", err)
	}
	if d := reported.Detail; reported.Reason != "INVALID_MANIFEST" || d.GetSourceLocation().GetPath() != "go.mod" || d.GetHelp() == "" {
		t.Errorf("error = %v, want invalid-manifest for go.mod with help", d)
	}
}

func TestAMethodThePluginDoesNotServeIsUnsupported(t *testing.T) {
	plugin := load(t)

	_, err := plugin.Call(context.Background(), files{}, "Release", &pluginv1.DescribeRequest{}, &pluginv1.DescribeResponse{})

	if !errors.Is(err, pluginhost.ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

// A call that its caller gave up on ends there. The plugin goes on
// answering: one build that was cancelled does not take the plugin from
// the builds after it.
func TestACallThatWasCancelledLeavesThePluginAnswering(t *testing.T) {
	plugin := load(t)
	source := files{"go.mod": goMod}
	gone, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := plugin.Call(gone, source, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext()}, &pluginv1.AnalyzeResponse{})
	if err == nil {
		t.Error("a call whose caller had gone was answered")
	}

	got := &pluginv1.AnalyzeResponse{}
	if _, err := plugin.Call(context.Background(), source, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext()}, got); err != nil {
		t.Fatalf("the call after it: %v", err)
	}
	if len(got.GetAnalyses()) != 1 || len(got.GetAnalyses()[0].GetProjects()) != 1 {
		t.Errorf("the call after it answered %v, want the module", got)
	}
}

// A module is somebody else's code. One that exports the ABI's names
// with other shapes is refused when it is loaded, by the name that is
// wrong, and the host is still there.
func TestAModuleWhoseExportsAreNotTheABIsIsRefused(t *testing.T) {
	// A module that exports the four names, each a function that takes
	// nothing and returns nothing.
	exports := []byte{4}
	for _, name := range []string{"frontseat_alloc", "frontseat_free", "frontseat_call", "frontseat_abi"} {
		exports = append(append(append(exports, byte(len(name))), name...), 0, 0)
	}
	module := handWritten(
		[]byte{1, 0x60, 0, 0}, // one type: () -> ()
		[]byte{1, 0},          // one function of it
		nil,
		exports,
		[]byte{1, 2, 0, 0x0b}, // its body: nothing
	)

	_, err := loadModule(t, module)

	if err == nil || !strings.Contains(err.Error(), "frontseat_abi") {
		t.Errorf("err = %v, want the module refused for its frontseat_abi", err)
	}
}

// A module built for another ABI is refused when it is loaded, by the
// number it speaks, before anything is asked of it: the contract it was
// built for is not this one, though every export has the ABI's shape.
// The controls are the same module speaking this host's ABI, which is
// asked what it is, and a plugin built with the SDK, which loads and
// says this host's ABI.
func TestAModuleBuiltForAnotherABIIsRefusedByItsNumber(t *testing.T) {
	// 17 is the ABI whose Action.program was a message: a module built at
	// it would mark no program here.
	for _, abi := range []uint32{17, pluginhost.MinABI - 1, pluginhost.MaxABI + 1} {
		_, err := loadModule(t, speaking(abi))
		want := fmt.Sprintf("speaks plugin ABI %d, and this host loads %d to %d", abi, pluginhost.MinABI, pluginhost.MaxABI)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ABI %d: err = %v, want it refused by its number", abi, err)
		}
	}

	if _, err := loadModule(t, speaking(pluginhost.MaxABI)); err == nil || !strings.Contains(err.Error(), "Describe: the module answered nothing") {
		t.Errorf("ABI %d: err = %v, want it asked what it is, and answering nothing", pluginhost.MaxABI, err)
	}
	if got := load(t).ABI; got != pluginhost.MaxABI {
		t.Errorf("the SDK's plugin says ABI %d, want %d", got, pluginhost.MaxABI)
	}
}

// speaking is a module whose exports have the ABI's shapes and whose
// frontseat_abi returns abi; it is given memory at address 8 and
// answers every call with nothing.
func speaking(abi uint32) []byte {
	if abi >= 64 {
		panic("an ABI from 64 takes more than one byte of an i32.const")
	}
	exports := []byte{5, 6, 'm', 'e', 'm', 'o', 'r', 'y', 2, 0}
	for i, name := range []string{"frontseat_abi", "frontseat_alloc", "frontseat_free", "frontseat_call"} {
		exports = append(append(append(exports, byte(len(name))), name...), 0, byte(i))
	}
	return handWritten(
		[]byte{4, // four types
			0x60, 0, 1, 0x7f, // () -> i32
			0x60, 1, 0x7f, 1, 0x7f, // (i32) -> i32
			0x60, 2, 0x7f, 0x7f, 0, // (i32, i32) -> ()
			0x60, 2, 0x7f, 0x7f, 1, 0x7e}, // (i32, i32) -> i64
		[]byte{4, 0, 1, 2, 3}, // four functions, one of each
		[]byte{1, 0, 1},       // a memory of one page
		exports,
		[]byte{4,
			4, 0, 0x41, byte(abi), 0x0b, // abi: the number
			4, 0, 0x41, 8, 0x0b, // alloc: address 8
			2, 0, 0x0b, // free: nothing
			4, 0, 0x42, 0, 0x0b}, // call: an empty answer
	)
}

// handWritten is a wasm module of the type, function, memory, export and
// code sections given, each left out where it is nil.
func handWritten(types, functions, memory, exports, code []byte) []byte {
	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	for _, s := range []struct {
		id   byte
		body []byte
	}{{1, types}, {3, functions}, {5, memory}, {7, exports}, {10, code}} {
		if s.body == nil {
			continue
		}
		module = append(append(module, s.id), counted(s.body)...)
	}
	return module
}

// loadModule loads module into a host of its own.
func loadModule(t *testing.T, module []byte) (*pluginhost.Plugin, error) {
	t.Helper()
	ctx := context.Background()
	host, err := pluginhost.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(ctx) })
	return host.Load(ctx, module)
}

func TestAModuleThatIsNotAPluginIsRefused(t *testing.T) {
	ctx := context.Background()
	host, err := pluginhost.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(ctx)

	// The smallest valid module: the magic number and the version.
	if _, err := host.Load(ctx, []byte("\x00asm\x01\x00\x00\x00")); err == nil {
		t.Error("a module with no exports was loaded")
	}
}

// TestWhatTheBoundaryCosts measures, and asserts nothing: the size of the
// module, the time to load it, and the time of a call once it is loaded.
func TestWhatTheBoundaryCosts(t *testing.T) {
	ctx := context.Background()
	host, err := pluginhost.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(ctx)

	start := time.Now()
	plugin, err := host.Load(ctx, module)
	if err != nil {
		t.Fatal(err)
	}
	loaded := time.Since(start)

	source := files{"go.mod": goMod}
	const calls = 200
	start = time.Now()
	for range calls {
		if _, err := plugin.Call(ctx, source, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext()}, &pluginv1.AnalyzeResponse{}); err != nil {
			t.Fatal(err)
		}
	}
	perCall := time.Since(start) / calls

	t.Logf("module: %.1f MiB", float64(len(module))/(1<<20))
	t.Logf("load (compile, instantiate, Describe): %s", loaded.Round(time.Millisecond))
	t.Logf("Analyze, loaded, mean of %d: %s", calls, perCall.Round(time.Microsecond))
}

// goContext is the fixture's explicit values, independent of plugin defaults.
func goContext() *pluginv1.Context {
	return &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT, Options: map[string]*typev1.OptionValue{
		"test-timeout": {Value: &typev1.OptionValue_StringValue{StringValue: "10m"}},
		"test-shards":  {Value: &typev1.OptionValue_IntValue{IntValue: 1}},
		"tags":         {Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{}}},
		"version":      {Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{}}},
	}}
}

// sharded asks the Go module, as it ships, about a module "m" of the
// source with test-shards set, and returns the names of its tests.
func sharded(t *testing.T, shards int64, source files) ([]string, error) {
	t.Helper()
	plugin := load(t)
	asked := goContext()
	asked.Options["test-shards"] = &typev1.OptionValue{Value: &typev1.OptionValue_IntValue{IntValue: shards}}
	roots := []string{"m"}
	got := &pluginv1.AnalyzeResponse{}
	if _, err := plugin.Call(context.Background(), source, "Analyze", &pluginv1.AnalyzeRequest{Context: asked, Roots: roots}, got); err != nil {
		return nil, err
	}
	if err := pluginhost.CheckAnalysis("go", roots, got); err != nil {
		return nil, fmt.Errorf("the answer breaks the contract: %w", err)
	}
	var tests []string
	for _, action := range got.GetAnalyses()[0].GetProjects()[0].GetActions() {
		if action.GetTest() != nil {
			tests = append(tests, action.GetName())
		}
	}
	return tests, nil
}

// testsNamed is a test file of the package many that declares the tests.
func testsNamed(names ...string) string {
	file := "package many\n\nimport \"testing\"\n\n"
	for _, name := range names {
		file += "func " + name + "(t *testing.T) {}\n"
	}
	return file
}

// The module that ships is wasm, where a count is thirty-two bits wide:
// what a repository sets is sixty-four, and is not cut to its low bits.
func TestAShardCountPastAModulesWordIsAsManyAsThereAreTests(t *testing.T) {
	source := files{"m/go.mod": goMod, "m/many/many.go": "package many\n", "m/many/many_test.go": testsNamed("TestA", "TestB", "TestC", "TestD")}
	if tests, err := sharded(t, 4, source); err != nil || len(tests) != 4 {
		t.Fatalf("four shards of four tests are %v (%v), so what follows proves nothing", tests, err)
	}
	for _, shards := range []int64{1 << 32, 1<<32 + 2, 1 << 40, math.MaxInt64} {
		if tests, err := sharded(t, shards, source); err != nil || len(tests) != 4 {
			t.Errorf("test-shards %d cut four tests into %v (%v); want four shards, as any count of four or more gives", shards, tests, err)
		}
	}
}

// A shard's share of a package's tests is named in one argument, and a
// worker takes none past 131072 bytes: more tests than that names are
// refused by where they are, never sent to fail there.
func TestMoreTestsThanAShardCanNameAreRefused(t *testing.T) {
	var names []string
	for i := range 8000 {
		names = append(names, fmt.Sprintf("TestGeneratedConformanceCase%05d", i))
	}
	source := files{"m/go.mod": goMod, "m/many/many.go": "package many\n", "m/many/many_test.go": testsNamed(names...)}
	if tests, err := sharded(t, 1, source); err != nil || len(tests) != 1 {
		t.Fatalf("with no shards the package is %v (%v), so what follows proves nothing", tests, err)
	}
	_, err := sharded(t, 2, source)
	if err == nil || !strings.Contains(err.Error(), "m/many") || !strings.Contains(err.Error(), "8000 tests") {
		t.Errorf("two shards of 8000 tests: %v; want them refused, naming the package and how many", err)
	}
}

// "@" parts a shard's name from its piece's. A directory named as a
// shard is, which the go command takes for no package, is refused by the
// plugin's own reason and never answered as two actions of one name.
func TestADirectoryNamedAsAShardIsRefusedByThePlugin(t *testing.T) {
	source := files{
		"m/go.mod": goMod,
		"m/x/x.go": "package many\n", "m/x/x_test.go": testsNamed("TestA", "TestB"),
		"m/x@1of2/x.go": "package many\n", "m/x@1of2/x_test.go": testsNamed("TestA"),
	}
	_, err := sharded(t, 2, source)
	if err == nil || strings.Contains(err.Error(), "breaks the contract") || !strings.Contains(err.Error(), "m/x@1of2") {
		t.Errorf("a directory named as a shard: %v; want the plugin to refuse it, by where it is", err)
	}
}
