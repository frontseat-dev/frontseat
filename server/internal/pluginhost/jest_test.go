package pluginhost_test

import (
	"errors"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
	"strings"
	"testing"
	"time"
)

func jestContext() *pluginv1.Context {
	return &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT, Options: map[string]*typev1.OptionValue{"test-shards": {Value: &typev1.OptionValue_IntValue{IntValue: 1}}}}
}

func jestFixture() conformance.Fixture {
	coordinate := &pluginv1.Coordinate{Ecosystem: "javascript-install", Name: "web"}
	return conformance.Fixture{
		Name: "jest", Description: &pluginv1.DescribeResponse{Name: "jest", Abi: 19, Interests: []string{"**/package.json", "!**/node_modules/**", "!**/testdata/**"}, Options: []*pluginv1.Option{{Name: "test-shards", Kind: pluginv1.Option_KIND_INT, Default: &typev1.OptionValue{Value: &typev1.OptionValue_IntValue{IntValue: 1}}, EntitySettable: true}}},
		Source: files{"app/package.json": npmPackage}, Absent: files{"app/package.json": `{"name":"library"}`}, Malformed: files{"app/package.json": strings.Replace(npmPackage, `"test":"jest"`, `"test":"jest --passWithNoTests"`, 1)}, ErrorCode: "INVALID_MANIFEST",
		Analyze: &pluginv1.AnalyzeRequest{Context: jestContext(), Roots: []string{"app"}},
		Projects: []*pluginv1.Project{{Root: "app", Name: "web", Kind: "jest-suite", Manifest: "app/package.json", AlwaysArranged: true,
			Requires: []*pluginv1.Requirement{{Coordinate: coordinate, Origin: &pluginv1.Origin{Path: "app/package.json", Line: 1}}},
			Actions:  []*pluginv1.Action{{Name: "test", Command: &pluginv1.Command{Arguments: []string{"npm", "run", "test", "--", "--ci"}}, Inputs: append([]*pluginv1.Input{pluginhost.SourceInput("**"), pluginhost.SourceInput("!node_modules/**")}, pluginhost.DependencyInput(coordinate, "{root}")), Tools: []string{"node"}, Test: true}},
		}},
		Reads:   []conformance.Observation{{Import: "read", Argument: "app/package.json", Found: true}},
		Arrange: &pluginv1.ArrangeRequest{Context: jestContext(), Root: "app", Actions: []string{"test"}, Handed: []*pluginv1.Handed{{Coordinate: coordinate, Into: "."}}}, Files: []*pluginv1.File{},
		InvalidArrange: &pluginv1.ArrangeRequest{Context: jestContext(), Root: "app", Actions: []string{"test"}}, ArrangeErrorCode: "MISSING_INSTALLATION", ArrangeErrorPath: "app/package.json",
	}
}

func TestJestKeepsInvocationSemanticsAndRequiresItsOwnInstallation(t *testing.T) {
	module, err := testmodule.Plugin("jest")
	if err != nil {
		t.Fatal(err)
	}
	plugin := conformance.Load(t, module)
	for name, change := range map[string]func(files, *pluginv1.Context){
		"script": func(s files, _ *pluginv1.Context) {
			s["app/package.json"] = strings.Replace(npmPackage, `"test":"jest"`, `"test":"jest --passWithNoTests"`, 1)
		},
		"pretest": func(s files, _ *pluginv1.Context) {
			s["app/package.json"] = strings.Replace(npmPackage, `"test":"jest"`, `"test":"jest","pretest":"node setup.js"`, 1)
		},
		"posttest": func(s files, _ *pluginv1.Context) {
			s["app/package.json"] = strings.Replace(npmPackage, `"test":"jest"`, `"test":"jest","posttest":"node finish.js"`, 1)
		},
		"shards past a wasm word": func(_ files, c *pluginv1.Context) {
			c.Options["test-shards"] = &typev1.OptionValue{Value: &typev1.OptionValue_IntValue{IntValue: 4294967297}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, c := files{"app/package.json": npmPackage}, jestContext()
			change(s, c)
			_, err := plugin.Call(t.Context(), s, "Analyze", &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: c}, new(pluginv1.AnalyzeResponse))
			var failure *pluginhost.Error
			if !errors.As(err, &failure) || failure.Reason != "INVALID_MANIFEST" {
				t.Fatalf("unsupported invocation accepted: %v", err)
			}
		})
	}
	for _, into := range []string{".", "../other", "", "node_modules"} {
		f := jestFixture()
		f.Arrange.Handed[0].Into = into
		_, err := plugin.Call(t.Context(), f.Source, "Arrange", f.Arrange, new(pluginv1.ArrangeResponse))
		if (err == nil) != (into == ".") {
			t.Fatalf("installation at %q: %v", into, err)
		}
	}
}

func TestNpmPreparesDependenciesWithoutATestRunner(t *testing.T) {
	module, err := testmodule.Plugin("npm")
	if err != nil {
		t.Fatal(err)
	}
	plugin := conformance.Load(t, module)
	s := files{"app/package.json": `{"name":"web","dependencies":{"a-library":"1.0.0"}}`, "app/package-lock.json": strings.ReplaceAll(npmLock, "jest", "a-library")}
	answer := new(pluginv1.AnalyzeResponse)
	if _, err := plugin.Call(t.Context(), s, "Analyze", &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: npmContext()}, answer); err != nil {
		t.Fatal(err)
	}
	project := answer.Analyses[0].Projects[0]
	if len(project.Actions) != 2 || len(project.Provides) != 1 {
		t.Fatalf("unexpected preparation: %v", project)
	}
	for _, a := range project.Actions {
		if a.GetTest() {
			t.Fatal("npm declares a test")
		}
	}
}

func BenchmarkJestAnalyze(b *testing.B) {
	module, err := testmodule.Plugin("jest")
	if err != nil {
		b.Fatal(err)
	}
	plugin := conformance.Load(b, module)
	s := files{"app/package.json": npmPackage}
	request := &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: jestContext()}
	for b.Loop() {
		answer := new(pluginv1.AnalyzeResponse)
		if _, err := plugin.Call(b.Context(), s, "Analyze", request, answer); err != nil {
			b.Fatal(err)
		}
		if err := pluginhost.CheckAnalysis("jest", request.Roots, answer); err != nil {
			b.Fatal(err)
		}
	}
}

// `mise exec -- go test -run '^$' -bench 'BenchmarkJestAnalyze$' -benchmem
// -count=3 ./server/internal/pluginhost` measured 104–170 us and
// 556–557 allocations on 2026-11-14. Time allows host scheduling noise.
func TestBudgetOfJestAnalysis(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 1500 * time.Microsecond, Allocs: 600}, BenchmarkJestAnalyze)
}
