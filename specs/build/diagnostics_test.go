package build_test

import (
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"testing"

	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

// Plugin failures reach every client with their source details: the graph
// as a structured status, a run as its failure and its diagnostics. A
// client must not parse a human message to find the failing rule or file.
// Diagnostics / A manifest with invalid text is refused without replacing its bytes.
func TestAManifestWithInvalidTextIsRefusedWithoutReplacingItsBytes(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource()
	source.Write(spec.TB, "platform", "gateway/go.mod", "module company.example/gate\xffway\n\ngo 1.27.3\n")
	workspace := source.workspaceOf("gateway")
	failure := workspace.projectFailure()
	spec.Expect(failure.GetCode()).To(Equal(int32(codes.FailedPrecondition)))
	finding := new(typev1.Diagnostic)
	spec.Expect(failure.GetDetails()).To(HaveLen(2))
	spec.Expect(failure.GetDetails()[1].UnmarshalTo(finding)).To(Succeed())
	spec.Expect(finding.GetRuleId()).To(Equal("go/invalid-text"))
	spec.Expect(finding.GetSourceLocation().GetPath()).To(Equal("gateway/go.mod"))
	workspace.write("platform/gateway/go.mod", "module company.example/gateway\n\ngo 1.27.3")
	spec.Expect(workspace.option("platform/gateway", "tags").value).To(BeEmpty())
}

// Diagnostics / A plugin failure retains structured details through the graph API.
func TestAPluginFailureRetainsStructuredDetailsThroughTheGraphAPI(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	source := spec.changedSource()
	source.Write(spec.TB, "platform", "gateway/go.mod", "go 1.27.3\n")
	workspace := source.workspaceOf("gateway")
	failure, finding := workspace.projectRefused("INVALID_MANIFEST", "go.plugins.frontseat.dev")
	spec.Expect(proto.Equal(finding, &typev1.Diagnostic{
		RuleId: "go/invalid-manifest", Message: "the go.mod names no module",
		SourceLocation: &typev1.SourceLocation{Repository: "platform", Path: "gateway/go.mod"},
		Help:           "Add a module directive, e.g. `module example.com/hello`.",
	})).To(BeTrue(), "diagnostic: %v", finding)
	build := workspace.build()
	spec.Expect(build.outcome()).To(Equal("BUILD_FAILURE"))
	// The run says the same failure, and keeps what the plugin found as
	// the graph does: the rule, the file and what to do.
	spec.Expect(build.run.GetFailure()).To(Equal(failure.GetMessage()), "run and graph must say the same failure")
	spec.Expect(build.run.GetDiagnostics()).To(HaveLen(1))
	spec.Expect(proto.Equal(build.run.GetDiagnostics()[0], finding)).To(BeTrue(), "run and graph must retain the same diagnostic: %v", build.run.GetDiagnostics())
	workspace.write("platform/gateway/go.mod", "module company.example/gateway\n\ngo 1.27.3")
	spec.Expect(workspace.option("platform/gateway", "tags").value).To(BeEmpty())
}

// projectRefused is the failure of the graph's projects, which a plugin
// refused before anything was built, held to be a source's precondition
// refused for a reason of a domain, and what it found there.
func (w *Workspace) projectRefused(reason, domain string) (*statuspb.Status, *typev1.Diagnostic) {
	w.scenario.Helper()
	failure := w.projectFailure()
	w.scenario.Expect(failure.GetCode()).To(Equal(int32(codes.FailedPrecondition)))
	w.scenario.Expect(failure.GetDetails()).To(HaveLen(2))
	info := new(errdetails.ErrorInfo)
	w.scenario.Expect(failure.GetDetails()[0].UnmarshalTo(info)).To(Succeed())
	w.scenario.Expect(info.GetReason()).To(Equal(reason))
	w.scenario.Expect(info.GetDomain()).To(Equal(domain))
	finding := new(typev1.Diagnostic)
	w.scenario.Expect(failure.GetDetails()[1].UnmarshalTo(finding)).To(Succeed())
	return failure, finding
}
