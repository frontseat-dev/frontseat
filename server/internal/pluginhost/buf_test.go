package pluginhost_test

import (
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbudget"
	"strings"
	"testing"
	"time"
)

func bufFixture(t *testing.T) conformance.Fixture {
	context := &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT}
	return arrangingNothing(context, conformance.Fixture{
		Name: "buf", Description: &pluginv1.DescribeResponse{Name: "buf", Abi: 22, Interests: []string{"**/buf.yaml", "!**/testdata/**"}, GeneratesSources: true},
		Source: fixtureSource(t, "buf", "valid"), Absent: files{}, Malformed: fixtureSource(t, "buf", "malformed"), ErrorCode: "INVALID_MANIFEST",
		Analyze: &pluginv1.AnalyzeRequest{Context: context, Roots: []string{"app"}},
		Projects: []*pluginv1.Project{{Root: "app", Name: "app", Kind: "buf-workspace", Manifest: "app/buf.yaml",
			Actions: []*pluginv1.Action{
				{Name: "lint", Command: &pluginv1.Command{Arguments: []string{"buf", "lint"}}, Inputs: []*pluginv1.Input{pluginhost.SourceInput("buf.yaml"), pluginhost.SourceInput("buf.lock"), pluginhost.SourceInput("**/*.proto")}, Tools: []string{"buf"}},
				{Name: "format", Command: &pluginv1.Command{Arguments: []string{"buf", "format", "-d", "--exit-code"}}, Inputs: []*pluginv1.Input{pluginhost.SourceInput("buf.yaml"), pluginhost.SourceInput("**/*.proto")}, Tools: []string{"buf"}, Check: true, Fix: &pluginv1.Command{Arguments: []string{"buf", "format", "-w"}}, Rewrites: []string{"**/*.proto"}},
				// A template whose output is outside the workspace is
				// generated beneath the project, at where the repository
				// has it, and placed by the host at its destination.
				{Name: "generate-lib", Command: &pluginv1.Command{Arguments: []string{"buf", "generate", "--template", "buf.gen.lib.yaml", "-o", ".gen-lib/app"}}, Inputs: []*pluginv1.Input{pluginhost.SourceInput("buf.yaml"), pluginhost.SourceInput("buf.lock"), pluginhost.SourceInput("**/*.proto"), pluginhost.SourceInput("buf.gen.lib.yaml")}, Tools: []string{"buf", "protoc-gen-go"}, Outputs: []*pluginv1.Output{{Path: ".gen-lib/lib/gen/"}}},
				{Name: "generate", Command: &pluginv1.Command{Arguments: []string{"buf", "generate"}}, Inputs: []*pluginv1.Input{pluginhost.SourceInput("buf.yaml"), pluginhost.SourceInput("buf.lock"), pluginhost.SourceInput("**/*.proto"), pluginhost.SourceInput("buf.gen.yaml")}, Tools: []string{"buf", "protoc-gen-go", "protoc-gen-connect-go"}, Outputs: []*pluginv1.Output{{Path: "gen/go/"}}},
			},
			GeneratedSources: []*pluginv1.GeneratedSource{
				{Action: "generate-lib", Output: ".gen-lib/lib/gen/", Destination: "lib/gen", Patterns: []string{"**/*.pb.go", "**/*.pb.go.meta"}},
				{Action: "generate", Output: "gen/go/", Directory: "gen/go", Patterns: []string{"**/*.pb.go", "**/*.pb.go.meta", "**/*.connect.go"}},
			},
		}},
		Reads: []conformance.Observation{{Import: "read", Argument: "app/buf.yaml", Found: true}, {Import: "glob", Argument: "buf.gen.yaml\x00buf.gen.*.yaml", Directory: "app", Found: true}, {Import: "read", Argument: "app/buf.gen.lib.yaml", Found: true}, {Import: "read", Argument: "app/buf.gen.yaml", Found: true}},
		Report: &pluginv1.ReportRequest{Context: context, Root: "app", Action: "format", ExitCode: 100,
			Stdout: []byte("--- hello/v1/hello.proto.orig\t2026-11-08 12:00:00.000000000 +0000\n+++ hello/v1/hello.proto\t2026-11-08 12:00:00.000000000 +0000\n@@ -1,3 +1,5 @@\n-message Hello {string message=1;}\n+message Hello {\n+  string message = 1;\n+}\n")},
		Findings: []*typev1.Diagnostic{{RuleId: "buf/format", Message: "it is not formatted as buf formats it", SourceLocation: &typev1.SourceLocation{Path: "app/hello/v1/hello.proto"}, Help: "Run `frontseat build --fix`, which runs buf format -w where the checkout is."}},
	})
}

func BenchmarkBufAnalyze(b *testing.B) {
	plugin := conformance.Plugin(b, "buf")
	source := fixtureSource(b, "buf", "valid")
	request := &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT}}
	benchmarkAnalysis(b, plugin, "buf", source, request)
}

// `mise exec -- go test -run '^$' -bench '^BenchmarkBufAnalyze$' -benchmem
// -count=3 ./server/internal/pluginhost` measured 276–284 us and 1094
// allocations on 2026-11-14, including tracked YAML reads and host validation.
// The contract carries typed input messages; the allocation
// allowance includes those messages. The 3.5 ms time allowance is unchanged.
func TestBudgetOfBufAnalysis(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 3500 * time.Microsecond, Allocs: 1150}, BenchmarkBufAnalyze)
}

func TestBufRejectsUnsupportedGeneration(t *testing.T) {
	plugin := conformance.Plugin(t, "buf")
	request := &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT}}
	valid := "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: gen/go\ninputs:\n  - directory: .\n"
	cases := map[string]string{
		"remote input":                  strings.Replace(valid, "directory: .", "module: buf.build/acme/api", 1),
		"escaping input":                strings.Replace(valid, "directory: .", "directory: ../outside", 1),
		"absolute input":                strings.Replace(valid, "directory: .", "directory: /outside", 1),
		"two input formats":             strings.Replace(valid, "directory: .", "directory: .\n    git_repo: https://example.com/repo.git", 1),
		"remote generator":              strings.Replace(valid, "local: protoc-gen-go", "local: protoc-gen-go\n    remote: buf.build/protocolbuffers/go", 1),
		"builtin generator":             strings.Replace(valid, "local: protoc-gen-go", "local: protoc-gen-go\n    protoc_builtin: go", 1),
		"archive output":                strings.Replace(valid, "out: gen/go", "out: gen/go.zip", 1),
		"output outside the repository": strings.Replace(valid, "out: gen/go", "out: ../../outside", 1),
		"output absolute":               strings.Replace(valid, "out: gen/go", "out: /outside", 1),
		"output the repository":         strings.Replace(valid, "out: gen/go", "out: ..", 1),
		"output the workspace":          strings.Replace(valid, "out: gen/go", "out: .", 1),
	}
	invoke := func(generation string) error {
		_, err := plugin.Call(t.Context(), files{"app/buf.yaml": "version: v2\nmodules: [{path: .}]\n", "app/buf.gen.yaml": generation}, "Analyze", request, new(pluginv1.AnalyzeResponse))
		return err
	}
	if err := invoke(valid); err != nil {
		t.Fatal(err)
	}
	for name, generation := range cases {
		t.Run(name, func(t *testing.T) {
			if err := invoke(generation); err == nil {
				t.Fatal("unsupported generation accepted")
			}
		})
	}
	if err := invoke(valid); err != nil {
		t.Fatal(err)
	}
}

// A template is named buf.gen.yaml or buf.gen.<name>.yaml, and its action
// generate or generate-<name>: a name that cannot be an action's is
// refused by its file, beside a well-named one accepted.
func TestBufNamesAnActionForEachTemplate(t *testing.T) {
	plugin := conformance.Plugin(t, "buf")
	request := &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: &pluginv1.Context{Profile: pluginv1.Profile_PROFILE_DEVELOPMENT}}
	template := "version: v2\nplugins:\n  - local: protoc-gen-go\n    out: ../sdk/go/api\ninputs:\n  - directory: .\n"
	analyze := func(name string) (*pluginv1.AnalyzeResponse, error) {
		answer := new(pluginv1.AnalyzeResponse)
		_, err := plugin.Call(t.Context(), files{"app/buf.yaml": "version: v2\nmodules: [{path: .}]\n", "app/" + name: template}, "Analyze", request, answer)
		return answer, err
	}
	answer, err := analyze("buf.gen.sdk.yaml")
	if err != nil {
		t.Fatal(err)
	}
	project := answer.GetAnalyses()[0].GetProjects()[0]
	if got := project.GetActions()[2].GetName(); got != "generate-sdk" {
		t.Fatalf("buf.gen.sdk.yaml is generated by %s", got)
	}
	if got := project.GetGeneratedSources()[0]; got.GetDestination() != "sdk/go/api" || got.GetOutput() != ".gen-sdk/sdk/go/api/" || got.GetDirectory() != "" {
		t.Fatalf("buf.gen.sdk.yaml contributes %v", got)
	}
	for _, name := range []string{"buf.gen.Sdk.yaml", "buf.gen.a.b.yaml", "buf.gen..yaml", "buf.gen.x_y.yaml"} {
		if _, err := analyze(name); err == nil || !strings.Contains(err.Error(), "app/"+name) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
