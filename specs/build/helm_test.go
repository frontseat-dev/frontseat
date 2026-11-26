package build_test

import (
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"

	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
)

// A directory that holds a Chart.yaml is a Helm chart: Helm lints it and
// renders it, on the grid, as checks. A chart that depends on another of
// the repository is handed it; one that depends on a chart fetched from
// outside is refused by name, since nothing fetches a chart yet.

// Helm charts / What helm lint finds is reported at the template's file and line, and a clean chart finds nothing.
func TestWhatHelmLintFindsIsReportedAtTheTemplatesFileAndLine(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	const label = "//web:helm.lint"
	workspace := spec.helmWorkspace()
	clean := workspace.build(of(label))
	spec.Expect(clean.outcome()).To(Equal("SUCCESS"), clean.diagnostics())
	spec.Expect(clean.outcomeOf(label)).To(Equal("passed"))
	spec.Expect(clean.diagnosticsOf(label)).To(BeEmpty())

	// Helm names the template by the chart's name, web, and the finding
	// is at the file of the repository: here the same place.
	workspace.write("web/templates/a.yaml", "apiVersion: v1\nkind: {{ nofunc }}\n")
	found := workspace.build(of(label))
	spec.Expect(found.outcomeOf(label)).To(Equal("failed"), found.diagnostics())
	diagnostics := found.diagnosticsOf(label)
	spec.Expect(diagnostics).To(HaveLen(1), found.printed(label))
	spec.Expect(diagnostics[0].GetRuleId()).To(Equal("helm/lint"))
	spec.Expect(diagnostics[0].GetSourceLocation().GetPath()).To(Equal("web/templates/a.yaml"))
	spec.Expect(diagnostics[0].GetSourceLocation().GetLine()).To(BeEquivalentTo(2))
	spec.Expect(diagnostics[0].GetMessage()).To(ContainSubstring(`function "nofunc" not defined`))
}

// Helm charts / A chart Helm cannot render fails its template, and one it can passes.
func TestAChartHelmCannotRenderFailsItsTemplate(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	const label = "//web:helm.template"
	workspace := spec.helmWorkspace()
	rendered := workspace.build(of(label))
	spec.Expect(rendered.outcomeOf(label)).To(Equal("passed"), rendered.diagnostics())
	workspace.write("web/templates/a.yaml", "apiVersion: v1\nkind: {{ .Values.greeting.missing.deeper }}\n")
	failed := workspace.build(of(label))
	spec.Expect(failed.outcomeOf(label)).To(Equal("failed"), failed.diagnostics())
	diagnostics := failed.diagnosticsOf(label)
	spec.Expect(diagnostics).NotTo(BeEmpty(), failed.printed(label))
	spec.Expect(diagnostics[0].GetRuleId()).To(Equal("helm/template"))
	spec.Expect(diagnostics[0].GetSourceLocation().GetPath()).To(Equal("web/templates/a.yaml"))
}

// Helm charts / A chart that depends on a chart fetched from outside is refused by name, and one that depends on a chart of the repository is handed it.
func TestAChartThatDependsOnAChartFetchedFromOutsideIsRefusedByName(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	const label = "//web:helm.template"
	workspace := spec.helmWorkspace()
	workspace.write("common/Chart.yaml", "apiVersion: v2\nname: common\nversion: 0.2.0\ntype: library\n")
	workspace.write("common/templates/_greeting.tpl", "{{- define \"common.greeting\" -}}from-common{{- end -}}\n")
	depending := func(repository string) string {
		return "apiVersion: v2\nname: web\nversion: 0.1.0\nicon: https://example.com/web.png\ndependencies:\n  - name: common\n    version: 0.2.0\n    repository: " + repository + "\n"
	}
	workspace.write("web/templates/a.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\ndata:\n  greeting: {{ include \"common.greeting\" . | quote }}\n")

	// The control: common, of this repository, is handed into web's
	// charts/, where Helm finds it, and its template is rendered in web's.
	// A stale copy the checkout holds there is not read beside it.
	workspace.write("web/charts/common/Chart.yaml", "apiVersion: v2\nname: common\nversion: 0.1.0\ntype: library\n")
	workspace.write("web/charts/common/templates/_greeting.tpl", "{{- define \"common.greeting\" -}}stale{{- end -}}\n")
	workspace.write("web/Chart.yaml", depending("file://../common"))
	served := workspace.build(of(label, "//web:helm.lint"))
	spec.Expect(served.outcome()).To(Equal("SUCCESS"), served.diagnostics())
	spec.Expect(served.printed(label)).To(ContainSubstring("from-common"))
	spec.Expect(served.printed(label)).NotTo(ContainSubstring("stale"))

	workspace.write("web/Chart.yaml", depending("oci://registry.example.com/charts"))
	failure := workspace.projectFailure()
	spec.Expect(failure.GetCode()).To(Equal(int32(codes.Unimplemented)))
	spec.Expect(failure.GetDetails()).To(HaveLen(2))
	reason := new(errdetails.ErrorInfo)
	spec.Expect(failure.GetDetails()[0].UnmarshalTo(reason)).To(Succeed())
	spec.Expect(reason.GetReason()).To(Equal("REMOTE_DEPENDENCIES_NOT_SERVED"))
	finding := new(typev1.Diagnostic)
	spec.Expect(failure.GetDetails()[1].UnmarshalTo(finding)).To(Succeed())
	spec.Expect(finding.GetSourceLocation().GetPath()).To(Equal("web/Chart.yaml"))
	spec.Expect(finding.GetSourceLocation().GetLine()).To(BeEquivalentTo(6))
	refused := workspace.build(of(label))
	spec.Expect(refused.outcome()).NotTo(Equal("SUCCESS"))
	spec.Expect(refused.actions()).To(BeEmpty())
}
