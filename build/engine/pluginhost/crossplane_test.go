package pluginhost_test

import (
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/conformance"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// crossplaneFixture is a Configuration of one definition, one Composition
// and one example: its package, and the check of its example against its
// definitions, which reads what validate printed as JSON and finds the
// resource it names in the example that holds it.
func crossplaneFixture(t *testing.T) conformance.Fixture {
	context := &pluginv1.Context{Profile: typev1.Profile_PROFILE_DEVELOPMENT, Tools: map[string]string{"crossplane": "2.5.0"}}
	excluded := []*pluginv1.Input{pluginhost.SourceInput("!**/testdata/**"), pluginhost.SourceInput("!**/catalog-info.yaml")}
	return arrangingNothing(context, conformance.Fixture{
		Name: "crossplane", ABI: 25, Description: &pluginv1.DescribeResponse{Name: "crossplane", Interests: []string{"**/crossplane.yaml", "!**/testdata/**"}},
		Source: fixtureSource(t, "crossplane", "valid"), Absent: files{}, Malformed: fixtureSource(t, "crossplane", "malformed"), ErrorCode: "INVALID_MANIFEST",
		Analyze: &pluginv1.AnalyzeRequest{Context: context, Roots: []string{"app"}},
		Projects: []*pluginv1.Project{{Root: "app", Name: "network", Kind: "crossplane-configuration", Manifest: "app/crossplane.yaml",
			Actions: []*pluginv1.Action{
				{Name: "package", Command: &pluginv1.Command{Arguments: []string{"crossplane", "xpkg", "build", "--package-root", ".", "--examples-root", "./examples", "--ignore", "catalog-info.yaml", "--package-file", "network.xpkg"}},
					Inputs: append([]*pluginv1.Input{pluginhost.SourceInput("**/*.yaml"), pluginhost.SourceInput("**/*.yml")}, excluded...), Tools: []string{"crossplane"},
					Outputs: []*pluginv1.Output{{Path: "network.xpkg"}}, Kind: &pluginv1.Action_Package{Package: &pluginv1.Package{}}},
				{Name: "validate", Command: &pluginv1.Command{Arguments: []string{"crossplane", "resource", "validate", "--crossplane-image=", "--output", "json", "--", "./apis/composition.yaml,./apis/definition.yaml", "./examples"}},
					Inputs: append([]*pluginv1.Input{pluginhost.SourceInput("apis/composition.yaml"), pluginhost.SourceInput("apis/definition.yaml"), pluginhost.SourceInput("examples/**/*.yaml"), pluginhost.SourceInput("examples/**/*.yml")}, excluded...), Tools: []string{"crossplane"},
					Kind: &pluginv1.Action_Check{Check: &pluginv1.Check{}}},
			},
		}},
		Reads: []conformance.Observation{
			{Import: "read", Argument: "app/crossplane.yaml", Found: true},
			{Import: "glob", Argument: "**/crossplane.yaml\x00!**/testdata/**", Directory: "app", Found: true},
			{Import: "glob", Argument: "**/*.yaml\x00**/*.yml\x00!crossplane.yaml\x00!examples*\x00!examples*/**\x00!**/testdata/**\x00!**/catalog-info.yaml", Directory: "app", Found: true},
			{Import: "glob", Argument: "examples/**/*.yaml\x00examples/**/*.yml\x00!**/testdata/**\x00!**/catalog-info.yaml", Directory: "app", Found: true},
		},
		Report: &pluginv1.ReportRequest{Context: context, Root: "app", Action: "validate", ExitCode: 1,
			Stdout: []byte(`{"summary": {"total": 1, "valid": 0, "invalid": 1, "missingSchemas": 0}, "resources": [{"apiVersion": "example.org/v1", "kind": "Network", "name": "eu", "namespace": "default", "status": "invalid", "errors": [{"type": "unknownField", "field": "spec.bogus", "message": "spec.bogus: Invalid value: \"bogus\": unknown field: \"bogus\"", "value": "bogus"}]}]}`),
			Stderr: []byte("crossplane: error: could not validate all resources\n")},
		Findings: []*typev1.Diagnostic{{RuleId: "crossplane/validate", Message: `Network default/eu: spec.bogus: Invalid value: "bogus": unknown field: "bogus"`,
			SourceLocation: &typev1.SourceLocation{Path: "app/examples/network.yaml", Line: 1},
			Help:           "Make the example what its CompositeResourceDefinition's schema says, or the schema what the example is; `crossplane resource validate` says why each is refused."}},
	})
}
