package pluginhost_test

import (
	"errors"
	"testing"
	"time"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/conformance"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func helmContext() *pluginv1.Context {
	return &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT, Tools: map[string]string{"helm": "4.3.1"}}
}

// helmChecks are a chart's lint and template, each reading what it is
// given beside the chart's own files.
func helmChecks(inputs ...*pluginv1.Input) []*pluginv1.Action {
	inputs = append([]*pluginv1.Input{pluginhost.SetInput("chart")}, inputs...)
	return []*pluginv1.Action{
		{Name: "lint", Command: &pluginv1.Command{Arguments: []string{"helm", "lint", "--strict", "."}}, Inputs: inputs, Tools: []string{"helm"}, Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{}}},
		{Name: "template", Command: &pluginv1.Command{Arguments: []string{"helm", "template", "."}}, Inputs: inputs, Tools: []string{"helm"}, Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{}}},
	}
}

func helmFixture(t *testing.T) conformance.Fixture {
	context := helmContext()
	return arrangingNothing(context, conformance.Fixture{
		Name: "helm", ABI: 25, Description: &pluginv1.DescribeResponse{Name: "helm", Interests: []string{"**/Chart.yaml", "!**/testdata/**"}},
		Source: fixtureSource(t, "helm", "valid"), Absent: files{}, Malformed: fixtureSource(t, "helm", "malformed"), ErrorCode: "INVALID_MANIFEST",
		Analyze: &pluginv1.AnalyzeRequest{Context: context, Roots: []string{"app"}},
		Projects: []*pluginv1.Project{{Root: "app", Name: "web", Kind: "helm-chart", Manifest: "app/Chart.yaml",
			// A chart is provided by its directory: two of one name are two.
			Provides:  []*pluginv1.Provision{{Coordinate: &pluginv1.Coordinate{Ecosystem: "helm", Name: "app"}, Sources: "chart"}},
			Actions:   helmChecks(),
			InputSets: []*pluginv1.InputSet{{Name: "chart", Inputs: []*pluginv1.Input{pluginhost.SourceInput("**"), pluginhost.SourceInput("!**/testdata/**")}}},
		}},
		Reads: []conformance.Observation{{Import: "read", Argument: "app/Chart.yaml", Found: true}, {Import: "read", Argument: "app/requirements.yaml"}, {Import: "read", Argument: "app/requirements.lock"}, {Import: "read", Argument: "app/Chart.lock"}},
		// Helm names a template by the chart's name, and the finding is
		// at its file and line in the chart's directory.
		Report: &pluginv1.ReportRequest{Context: context, Root: "app", Action: "lint", ExitCode: 1,
			Stdout: []byte("==> Linting .\n[INFO] Chart.yaml: icon is recommended\n[ERROR] templates/: parse error at (web/templates/configmap.yaml:2): unclosed action\n\nError: 1 chart(s) linted, 1 chart(s) failed\n")},
		Findings: []*typev1.Diagnostic{{RuleId: "helm/lint", Message: "parse error at (web/templates/configmap.yaml:2): unclosed action", SourceLocation: &typev1.SourceLocation{Path: "app/templates/configmap.yaml", Line: 2}, Help: "Fix what helm lint reports; run helm lint --strict in the chart's directory to see it."}},
	})
}

// A chart that depends on another of the repository by file:// requires
// it, and is handed it into charts/, where Helm looks; one whose
// dependency is fetched from outside is refused by name, and nothing of
// it is answered. The control is the same chart depending on a chart of
// the repository.
func TestAHelmChartWithARemoteDependencyIsRefusedByNameAndALocalOneServed(t *testing.T) {
	plugin := conformance.Plugin(t, "helm")
	request := &pluginv1.AnalyzeRequest{Roots: []string{"web"}, Context: helmContext()}
	depending := func(repository string) files {
		return files{
			"web/Chart.yaml":    "apiVersion: v2\nname: web\nversion: 0.1.0\ndependencies:\n  - name: common\n    version: 0.2.0\n    repository: " + repository + "\n",
			"common/Chart.yaml": "apiVersion: v2\nname: common\nversion: 0.2.0\n",
		}
	}
	answer := new(pluginv1.AnalyzeResponse)
	if _, err := plugin.Call(t.Context(), depending("file://../common"), "Analyze", request, answer); err != nil {
		t.Fatal(err)
	}
	if err := pluginhost.CheckAnalysis("helm", request.Roots, answer); err != nil {
		t.Fatal(err)
	}
	common := &pluginv1.Coordinate{Ecosystem: "helm", Name: "common"}
	want := &pluginv1.Project{Root: "web", Name: "web", Kind: "helm-chart", Manifest: "web/Chart.yaml",
		Provides:  []*pluginv1.Provision{{Coordinate: &pluginv1.Coordinate{Ecosystem: "helm", Name: "web"}, Sources: "chart"}},
		Requires:  []*pluginv1.Requirement{{Coordinate: common, Origin: &pluginv1.Origin{Path: "web/Chart.yaml", Line: 5}}},
		Actions:   helmChecks(pluginhost.SourceInput("!charts/common/**"), pluginhost.SourceInput("!charts/common-[0-9]*.tgz"), pluginhost.DependencyInput(common, "charts/common")),
		InputSets: []*pluginv1.InputSet{{Name: "chart", Inputs: []*pluginv1.Input{pluginhost.SourceInput("**"), pluginhost.SourceInput("!**/testdata/**")}}},
	}
	if got := answer.GetAnalyses()[0].GetProjects(); len(got) != 1 || !proto.Equal(got[0], want) {
		t.Fatalf("a chart of a local dependency is %v, want %v", got, want)
	}
	for _, remote := range []string{"oci://registry.example.com/charts", "https://charts.example.com"} {
		_, err := plugin.Call(t.Context(), depending(remote), "Analyze", request, new(pluginv1.AnalyzeResponse))
		var refused *pluginhost.Error
		if !errors.As(err, &refused) || refused.Reason != "REMOTE_DEPENDENCIES_NOT_SERVED" || codes.Code(refused.Status.GetCode()) != codes.Unimplemented {
			t.Errorf("%s: %v", remote, err)
			continue
		}
		if at := refused.Detail.GetSourceLocation(); at.GetPath() != "web/Chart.yaml" || at.GetLine() != 5 {
			t.Errorf("%s is refused at %v", remote, at)
		}
	}
}

func BenchmarkHelmAnalyze(b *testing.B) {
	plugin := conformance.Plugin(b, "helm")
	source := fixtureSource(b, "helm", "valid")
	request := &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: helmContext()}
	benchmarkAnalysis(b, plugin, "helm", source, request)
}

// `mise exec -- go test -run '^$' -bench '^BenchmarkHelmAnalyze$' -benchmem
// -count=3 ./build/engine/pluginhost`, through the gate, measured
// 139–140 us and 835 allocations on 2026-11-26 (Buf's beside it 358–360 us
// and 962). Time allows host scheduling noise, as Jest's does.
func TestBudgetOfHelmAnalysis(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 1500 * time.Microsecond, Allocs: 900}, BenchmarkHelmAnalyze)
}
