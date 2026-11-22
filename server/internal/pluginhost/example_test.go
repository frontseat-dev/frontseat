package pluginhost_test

import (
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
)

// The plugin a plugin's author starts from (sdk/plugin/examples/make.rs)
// is a module the host serves: it says what it is, finds its project
// and the host holds its answer to the contract, and it reads what its
// check printed into findings of files of the project, and of nothing
// else.
func TestTheSDKsExamplePluginIsServedByTheHost(t *testing.T) {
	plugin := conformance.SDKExample(t, "make")
	context := &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT}
	analyzed := new(pluginv1.AnalyzeResponse)
	if _, err := plugin.Call(t.Context(), files{"app/Makefile": "test:\n"}, "Analyze", &pluginv1.AnalyzeRequest{Context: context, Roots: []string{"app"}}, analyzed); err != nil {
		t.Fatal(err)
	}
	if err := pluginhost.CheckAnalysis("make", []string{"app"}, analyzed); err != nil {
		t.Fatalf("the example's answer is not held to the contract: %v", err)
	}
	projects := analyzed.GetAnalyses()[0].GetProjects()
	if len(projects) != 1 || len(projects[0].GetActions()) != 2 {
		t.Fatalf("the example found %v, want one project with its test and its check", projects)
	}
	reported := new(pluginv1.ReportResponse)
	printed := []byte("lib.c:3:1: a tab where a space is\n../outside.c:1: not of the project\nmake: *** [lint] Error 1\n")
	if _, err := plugin.Call(t.Context(), files{}, "Report", &pluginv1.ReportRequest{Context: context, Root: "app", Action: "lint", ExitCode: 2, Stdout: printed}, reported); err != nil {
		t.Fatal(err)
	}
	found := reported.GetDiagnostics()
	if len(found) != 1 || found[0].GetRuleId() != "make/lint" || found[0].GetSourceLocation().GetPath() != "app/lib.c" || found[0].GetSourceLocation().GetLine() != 3 {
		t.Fatalf("the example found %v, want lib.c's one finding", found)
	}
}
