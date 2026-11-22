package pluginhost_test

import (
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/pluginhosttest"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
	"google.golang.org/protobuf/proto"
)

// Every plugin directory must register real ecosystem fixtures. Adding a plugin
// without these shared API checks fails validation rather than silently skipping it.
func pluginFixtures(t *testing.T) map[string]conformance.Fixture {
	goReport, goFindings := expectedGoReport()
	return map[string]conformance.Fixture{
		"apko":      apkoFixture(t),
		"backstage": backstageFixture(t),
		"buf":       bufFixture(t),
		"cargo":     cargoFixture(t),
		"container": containerFixture(t),
		"postgres":  postgresFixture(t),
		"npm":       npmFixture(),
		"jest":      jestFixture(),
		"release":   releaseFixture(),
		"github":    githubFixture(),
		"go": {
			ABI: 25, Description: &pluginv1.DescribeResponse{Name: "go", Interests: []string{"**/go.mod", "**/go.sum", "**/*.go", "!**/testdata/**"}, Options: []*pluginv1.Option{{Name: "test-timeout", Description: "How long each test action may run, as the go command writes a duration (10m).", Kind: pluginv1.Option_KIND_STRING, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: "10m"}}, EntitySettable: true}, {Name: "test-shards", Description: "Into how many actions each package's tests are cut at most, each running a share.", Kind: pluginv1.Option_KIND_INT, Default: &typev1.OptionValue{Value: &typev1.OptionValue_IntValue{IntValue: 1}}, EntitySettable: true}, {Name: "tags", Description: "The build tags a module is linted, compiled and tested with.", Kind: pluginv1.Option_KIND_STRING_LIST, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{}}}, EntitySettable: true}, {Name: "version", Description: "Variables a module's programs say their version by, each <package>.<variable>=<version|tag|commit|unit>.", Kind: pluginv1.Option_KIND_STRING_LIST, Default: &typev1.OptionValue{Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{}}}, EntitySettable: true}}},
			Name: "go", Source: fixtureSource(t, "go", "valid"),
			Absent: files{}, Malformed: fixtureSource(t, "go", "malformed"), ErrorCode: "INVALID_MANIFEST",
			Projects:       []*pluginv1.Project{{Root: "app", Name: "example.com/hello", Kind: "go-module", Manifest: "app/go.mod", Actions: expectedGoActions(), InputSets: []*pluginv1.InputSet{{Name: "sources", AcceptsGenerated: true, Inputs: []*pluginv1.Input{pluginhost.SourceInput("go.mod"), pluginhost.SourceInput("go.sum"), pluginhost.SourceInput("**/*.go"), pluginhost.SourceInput("**/*.s"), pluginhost.SourceInput("**/*.h"), pluginhost.SourceInput("**/*.syso"), pluginhost.SourceInput("!**/testdata/**")}}, {Name: "module", AcceptsGenerated: true, Inputs: []*pluginv1.Input{pluginhost.SourceInput("go.mod"), pluginhost.SourceInput("go.sum"), pluginhost.SourceInput("**/*.go"), pluginhost.SourceInput("**/*.s"), pluginhost.SourceInput("**/*.h"), pluginhost.SourceInput("**/*.syso"), pluginhost.SourceInput("!**/testdata/**"), pluginhost.SourceInput("!**/*_test.go")}}}, Provides: []*pluginv1.Provide{{Coordinate: &pluginv1.Coordinate{Ecosystem: "go", Name: "example.com/hello"}, Sources: "module"}}, Requires: []*pluginv1.Requirement{{Coordinate: &pluginv1.Coordinate{Ecosystem: "go", Name: "example.com/dep"}, Origin: &pluginv1.Origin{Path: "app/go.mod", Line: 5}, Version: "v0.0.0"}}}},
			Reads:          []conformance.Observation{{Import: "read", Argument: "app/go.mod", Found: true}, {Import: "read", Argument: "app/go.sum", Found: false}, {Import: "glob", Argument: "**/go.mod\x00!**/testdata/**", Directory: "app", Found: true}, {Import: "glob", Argument: "**/*.go\x00!**/testdata/**", Directory: "app", Found: true}, {Import: "read", Argument: "app/internal/ledger/ledger.go", Found: true}, {Import: "read", Argument: "app/internal/ledger/ledger_test.go", Found: true}, {Import: "read", Argument: "app/main.go", Found: true}, {Import: "read", Argument: "app/main_test.go", Found: true}},
			Files:          []*pluginv1.File{{Path: "app/go.work", Content: []byte("go 1.27.3\n\nuse .\n\nreplace (\n\texample.com/dep => ./.frontseat/go/example.com/dep\n)\n")}},
			Analyze:        &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT, Options: map[string]*typev1.OptionValue{"test-timeout": &typev1.OptionValue{Value: &typev1.OptionValue_StringValue{StringValue: "10m"}}, "test-shards": &typev1.OptionValue{Value: &typev1.OptionValue_IntValue{IntValue: 1}}, "tags": &typev1.OptionValue{Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{}}}, "version": &typev1.OptionValue{Value: &typev1.OptionValue_StringListValue{StringListValue: &typev1.OptionValue_StringList{}}}}}},
			InvalidArrange: &pluginv1.ArrangeRequest{Root: "app", Context: goContext()}, ArrangeErrorCode: "UNPINNED_TOOL", ArrangeErrorPath: "app/go.mod",
			Report:   goReport,
			Findings: goFindings,
			Arrange:  &pluginv1.ArrangeRequest{Root: "app", Context: &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT, Options: goContext().Options, Tools: map[string]string{"go": "1.27.3"}}, Actions: []string{"compile"}, Handed: []*pluginv1.Handed{{Coordinate: &pluginv1.Coordinate{Ecosystem: "go", Name: "example.com/dep"}, Into: ".frontseat/go/example.com/dep"}}},
		},
	}
}

func TestEveryPluginConforms(t *testing.T) {
	fixtures := pluginFixtures(t)
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	manifests, err := filepath.Glob(filepath.Join(root, "plugins", "*", "Cargo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, manifest := range manifests {
		names = append(names, filepath.Base(filepath.Dir(manifest)))
	}
	if err := conformance.ValidateFixtures(names, fixtures); err != nil {
		t.Fatal(err)
	}
	for _, manifest := range manifests {
		name := filepath.Base(filepath.Dir(manifest))
		fixture, ok := fixtures[name]
		if !ok {
			t.Fatalf("plugin %s has no shared conformance fixture", name)
		}
		t.Run(name, func(t *testing.T) {
			fixture.Module, err = testmodule.Plugin(name)
			if err != nil {
				t.Fatal(err)
			}
			conformance.Run(t, fixture)
		})
	}
}

// The fixture spells the commands out independently of the Rust implementation.
func expectedGoActions() []*pluginv1.Action {
	action := func(name string, args []string) *pluginv1.Action {
		return &pluginv1.Action{
			Name: name, Command: &pluginv1.Command{Arguments: args, Environment: map[string]string{"CGO_ENABLED": "0", "GOPROXY": "off", "GOSUMDB": "off", "MISE_GO_SET_GOBIN": "false"}}, Inputs: append([]*pluginv1.Input{pluginhost.SetInput("sources")}, pluginhosttest.AllDependencies(".frontseat/go/{name}")), Tools: []string{"go"}, Kind: &pluginv1.Action_Build{Build: &pluginv1.Build{}},
		}
	}
	lint := action("lint", []string{"go", "vet", "./..."})
	lint.Kind = &pluginv1.Action_Check{Check: &pluginv1.Check{}}
	format := action("format", []string{"gofmt", "-l", "."})
	format.Kind = &pluginv1.Action_Check{Check: &pluginv1.Check{Fix: &pluginv1.Command{Arguments: []string{"gofmt", "-w", "."}}, Rewrites: []string{"**/*.go"}}}
	compile := action("compile", []string{"go", "install", "-trimpath", "-gcflags=all=-N -l", "./..."})
	compile.Outputs = []*pluginv1.Output{{Path: "~/go/bin/", Optional: true}}
	// The programs of the tests of whichever packages the go command
	// builds any for, and a test for each of the two that hold a test
	// file: its own program, run in its package's directory, with its
	// fixtures and nothing a build is handed, and nothing to do where
	// its program was not left.
	build := action("test-build", []string{"go", "test", "-c", "-trimpath", "-o", "_tests/0/", ".", "./internal/ledger/..."})
	build.Outputs = []*pluginv1.Output{{Path: "_tests/0/hello.test", Optional: true}, {Path: "_tests/0/ledger.test", Optional: true}}
	own := action("test/.", []string{"./_tests/0/hello.test", "-test.paniconexit0", "-test.timeout=10m"})
	own.Kind = &pluginv1.Action_Test{Test: &pluginv1.Test{IdleWithoutInputs: true}}
	own.Inputs = []*pluginv1.Input{pluginhosttest.OutputInput("_tests/0/hello.test"), pluginhost.SourceInput("**"), pluginhost.SourceInput("!**/vendor/**"), pluginhost.SourceInput("!**/node_modules/**"), pluginhost.SourceInput("!internal/ledger/**"), pluginhost.SourceInput("testdata/**")}
	ledger := action("test/internal/ledger", []string{"../../_tests/0/ledger.test", "-test.paniconexit0", "-test.timeout=10m"})
	ledger.Kind = &pluginv1.Action_Test{Test: &pluginv1.Test{IdleWithoutInputs: true}}
	ledger.Command.Directory = "internal/ledger"
	ledger.Inputs = []*pluginv1.Input{pluginhosttest.OutputInput("_tests/0/ledger.test"), pluginhost.SourceInput("internal/ledger/**"), pluginhost.SourceInput("!internal/ledger/**/vendor/**"), pluginhost.SourceInput("!internal/ledger/**/node_modules/**"), pluginhost.SourceInput("internal/ledger/testdata/**")}

	// The fixture's one directory says package main: a program, which
	// is what its compile left where the go command keeps programs.
	run := &pluginv1.Action{
		Name:    "run/hello",
		Command: &pluginv1.Command{Arguments: []string{"~/go/bin/hello"}},
		Inputs:  []*pluginv1.Input{pluginhosttest.OutputInput("compile")},
		Kind:    &pluginv1.Action_Program{Program: &pluginv1.Program{}},
	}
	return []*pluginv1.Action{lint, format, compile, build, own, ledger, run}
}

// arrangingNothing is the fixture of a plugin that arranges nothing:
// asked to arrange the project at app, it gives no file, and asked for an
// arrangement it does not serve, it refuses it by name.
func arrangingNothing(context *pluginv1.Context, fixture conformance.Fixture) conformance.Fixture {
	fixture.Arrange = &pluginv1.ArrangeRequest{Context: context, Root: "app"}
	fixture.Files = []*pluginv1.File{}
	fixture.InvalidArrange = &pluginv1.ArrangeRequest{Context: context, Root: "app", Actions: []string{"invented"}}
	fixture.ArrangeErrorCode = "UNSUPPORTED_ARRANGEMENT"
	return fixture
}

// vetFailed asks the Go plugin what the lint of the module at app found,
// where vet printed this and failed.
func vetFailed(stderr string) *pluginv1.ReportRequest {
	return &pluginv1.ReportRequest{
		Context:  &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT, Options: goContext().Options, Tools: map[string]string{"go": "1.27.3"}},
		Root:     "app",
		Action:   "lint",
		ExitCode: 1,
		Stderr:   []byte(stderr),
	}
}

// What vet printed of the fixture's module, and what the plugin says it
// found: spelled out independently of the Rust implementation.
func expectedGoReport() (*pluginv1.ReportRequest, []*typev1.Diagnostic) {
	request := vetFailed("# example.com/hello\n./main.go:6:14: fmt.Printf format %d has arg \"x\" of wrong type string\nvet: typecheck failed\n")
	return request, []*typev1.Diagnostic{{
		RuleId:         "go/vet",
		Message:        "fmt.Printf format %d has arg \"x\" of wrong type string",
		SourceLocation: &typev1.SourceLocation{Path: "app/main.go", Line: 6},
		Help:           "Fix what vet reports; see `go doc cmd/vet`.",
	}}
}

// All plugin fixture source is loaded the same way. The Go host's existing
// in-memory Source supplies tracked reads and its production glob matcher.
func fixtureSource(t testing.TB, plugin, name string) files {
	t.Helper()
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	disk := os.DirFS(filepath.Join(root, "plugins", plugin, "testdata", name))
	out := files{}
	err = fs.WalkDir(disk, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := fs.ReadFile(disk, path)
		if err == nil {
			out[path] = string(raw)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestConformanceInventoryRejectsMissingPluginsAndMethods(t *testing.T) {
	fixtures := pluginFixtures(t)
	if err := conformance.ValidateFixtures([]string{"go", "backstage", "buf", "cargo", "release", "apko", "container", "postgres", "npm", "jest", "github"}, fixtures); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{{"go", "unregistered"}, {"unregistered"}, nil} {
		if err := conformance.ValidateFixtures(names, fixtures); err == nil {
			t.Fatalf("accepted plugin inventory %v", names)
		}
	}
	// A plugin that reads no file's content has no malformed source; one
	// that reads one must have, and neither names an error without one.
	unread := fixtures["github"]
	unread.Reads = append(unread.Reads, conformance.Observation{Import: "read", Argument: "frontseat.yaml", Found: true})
	named := fixtures["github"]
	named.ErrorCode = "INVALID_MANIFEST"
	for what, f := range map[string]conformance.Fixture{"a read with no malformed source": unread, "an error with no malformed source": named} {
		if err := conformance.ValidateFixtures([]string{"github"}, map[string]conformance.Fixture{"github": f}); err == nil || !strings.Contains(err.Error(), "Analyze") {
			t.Fatalf("%s was accepted, or refused otherwise: %v", what, err)
		}
	}
	for _, method := range []string{"Describe", "Analyze", "Arrange", "Arrange error"} {
		t.Run(method, func(t *testing.T) {
			f := fixtures["go"]
			switch method {
			case "Describe":
				f.Description = nil
			case "Analyze":
				f.Analyze = nil
			case "Arrange":
				f.Arrange = nil
			case "Arrange error":
				f.InvalidArrange = nil
			}
			err := conformance.ValidateFixtures([]string{"go"}, map[string]conformance.Fixture{"go": f})
			if err == nil || !strings.Contains(err.Error(), strings.Fields(method)[0]) {
				t.Fatalf("missing %s coverage accepted or wrong refusal: %v", method, err)
			}
		})
	}
}

// A strategy plugin's fixture is refused where a plugin that did nothing
// could pass it: a strategy it says and is never asked, asked only what
// it answers one way, never refused, or dressed as a plugin with a source.
func TestAStrategyFixtureThatProvesTooLittleIsRefused(t *testing.T) {
	sound := func() conformance.Fixture { return releaseFixture() }
	valid := func(f conformance.Fixture) error {
		return conformance.ValidateFixtures([]string{"release"}, map[string]conformance.Fixture{"release": f})
	}
	if err := valid(sound()); err != nil {
		t.Fatalf("the release plugin's fixture was refused, so this test proves nothing: %v", err)
	}
	branches := func(f conformance.Fixture, keep func(conformance.BranchCase) bool) []conformance.BranchCase {
		var out []conformance.BranchCase
		for _, c := range f.Branches {
			if keep(c) {
				out = append(out, c)
			}
		}
		return out
	}
	versions := func(f conformance.Fixture, keep func(conformance.VersionCase) bool) []conformance.VersionCase {
		var out []conformance.VersionCase
		for _, c := range f.Versions {
			if keep(c) {
				out = append(out, c)
			}
		}
		return out
	}
	for name, change := range map[string]func(*conformance.Fixture){
		"no branch that releases": func(f *conformance.Fixture) {
			f.Branches = branches(*f, func(c conformance.BranchCase) bool { return !c.Want.GetReleases() })
		},
		"no branch that releases nothing": func(f *conformance.Fixture) {
			f.Branches = branches(*f, func(c conformance.BranchCase) bool { return c.Refused != "" || c.Want.GetReleases() })
		},
		"no unit that moves": func(f *conformance.Fixture) {
			f.Versions = versions(*f, func(c conformance.VersionCase) bool { return c.Want.GetVersion() == "" })
		},
		"no unit that holds": func(f *conformance.Fixture) {
			f.Versions = versions(*f, func(c conformance.VersionCase) bool { return c.Refused != "" || c.Want.GetVersion() != "" })
		},
		"no version refused": func(f *conformance.Fixture) {
			f.Versions = versions(*f, func(c conformance.VersionCase) bool {
				return c.Refused == "" || c.Request.GetStrategy() != "conventional-commits"
			})
		},
		"no strategy it does not serve": func(f *conformance.Fixture) {
			f.Branches = branches(*f, func(c conformance.BranchCase) bool { return c.Request.GetStrategy() == "release-flow" })
			f.Versions = versions(*f, func(c conformance.VersionCase) bool { return c.Request.GetStrategy() == "conventional-commits" })
		},
		"a strategy it says and is never asked": func(f *conformance.Fixture) {
			f.Description = proto.Clone(f.Description).(*pluginv1.DescribeResponse)
			f.Description.Release.Branchings = append(f.Description.Release.Branchings, "github-flow")
		},
		"an answer expected of a strategy it does not say": func(f *conformance.Fixture) {
			f.Branches = append(f.Branches, conformance.BranchCase{Request: &pluginv1.BranchRequest{Strategy: "github-flow"}, Want: &pluginv1.BranchResponse{Releases: true}})
		},
		"a case with an answer and a refusal": func(f *conformance.Fixture) {
			f.Branches = append([]conformance.BranchCase{}, f.Branches...)
			f.Branches[0].Refused = "UNKNOWN_STRATEGY"
		},
		"a case with neither": func(f *conformance.Fixture) {
			f.Versions = append(f.Versions, conformance.VersionCase{Request: &pluginv1.VersionRequest{Strategy: "conventional-commits"}})
		},
		"no release roles": func(f *conformance.Fixture) {
			f.Description = &pluginv1.DescribeResponse{Name: "release"}
		},
		"interests": func(f *conformance.Fixture) {
			f.Description = proto.Clone(f.Description).(*pluginv1.DescribeResponse)
			f.Description.Interests = []string{"**/go.mod"}
		},
		"a source beside its questions": func(f *conformance.Fixture) { f.Source = files{} },
		"projects beside its questions": func(f *conformance.Fixture) { f.Projects = []*pluginv1.Project{{Root: "app"}} },
		"no description":                func(f *conformance.Fixture) { f.Description = nil },
	} {
		f := sound()
		change(&f)
		if err := valid(f); err == nil {
			t.Errorf("a strategy fixture with %s was accepted", name)
		}
	}
}

// githubFixture is the GitHub plugin's: found by the file that lists
// it being there, which it never reads, it has one project at the
// repository's root, whose one action publishes.
func githubFixture() conformance.Fixture {
	context := &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_PRODUCTION, Tools: map[string]string{"gh": "2.105.0"}}
	fixture := arrangingNothing(context, conformance.Fixture{
		Name: "github", ABI: 25, Description: &pluginv1.DescribeResponse{Name: "github", Interests: []string{"frontseat.yaml", "!**/testdata/**"}},
		Source: files{"frontseat.yaml": "plugins:\n  github: {}\n"}, Absent: files{},
		Analyze: &pluginv1.AnalyzeRequest{Context: context, Roots: []string{""}},
		Projects: []*pluginv1.Project{{Kind: "github-repository", Name: "github", Manifest: "frontseat.yaml", Actions: []*pluginv1.Action{{
			Name: "publish/release", Tools: []string{"gh"}, Kind: &pluginv1.Action_Publish{Publish: &pluginv1.Publish{Environment: []string{"GH_TOKEN"}}},
			Command: &pluginv1.Command{Arguments: []string{"gh", "release", "create", "{frontseat.tag}", "{frontseat.artifacts}", "--repo", "{frontseat.remote}", "--verify-tag", "--title", "{frontseat.tag}", "--notes-file", "{frontseat.notes}", "--prerelease={frontseat.prerelease}"}},
		}}}},
		Reads: []conformance.Observation{{Import: "glob", Argument: "frontseat.yaml", Found: true}},
	})
	fixture.Arrange.Root, fixture.InvalidArrange.Root = "", ""
	return fixture
}
