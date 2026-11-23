package sandbox_test

import (
	"context"
	"github.com/frontseat-dev/frontseat/testsupport/testidentity"
	"time"

	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	. "github.com/onsi/gomega"
	"testing"
)

// These specs exercise the retained engine in the real workspace daemon:
// cloned source in an OpenShell sandbox, reached through its control plane.
// They make no claim about process-memory bounds or comparative build latency.
// shares graph analysis with routed builds and validates changed sandbox source.
func TestRetainedWorkspaceSharesGraphAnalysisAndValidatesChangedSource(t *testing.T) {
	spec := newScenario(t)
	w := spec.retainedTool(spec.retainedPlane())
	collector := w.observeRetained()
	w.uniqueRetainedProgram()
	spec.Expect(w.retainedToolProject().GetProvides()).To(ContainElement("go:example.com/tool"))

	first := w.build(retainedCompileOnly)
	spec.Expect(first.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_EXECUTED))
	initial := first.retainedStages(collector, true, false, false)

	// The generated Connect client uses no Frontseat client wrapper: an
	// HTTP client of the caller's that presents the owner's token.
	raw := buildsAt(w.Build)
	second := w.buildWith(raw, retainedCompileOnly)
	spec.Expect(second.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_GRID_CACHE))
	warm := second.retainedStages(collector, true, true, false)
	spec.Expect(warm.evaluate.TraceId).NotTo(Equal(initial.evaluate.TraceId))
	spec.Expect(warm.evaluate.SpanId).NotTo(Equal(initial.evaluate.SpanId))

	w.copyIn("/work/lab/tool/go.mod", w.holds("/work/lab/tool/go.mod")+"\n// equal analyzed module\n")
	w.uniqueRetainedProgram()
	equal := w.build(retainedCompileOnly)
	spec.Expect(equal.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_EXECUTED))
	equal.retainedStages(collector, true, true, true)

	w.retainedModule("example.com/changed")
	changed := w.build(retainedCompileOnly)
	spec.Expect(changed.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_EXECUTED))
	changed.retainedStages(collector, false, false, true)
	spec.Expect(w.retainedToolProject().GetProvides()).To(ContainElement("go:example.com/changed"))
	spec.Expect(w.retainedToolProject().GetProvides()).NotTo(ContainElement("go:example.com/tool"))
	w.delete()
	spec.Expect(w.askingAfterItsSandbox()).To(HaveOccurred())
}

// isolates two workspaces and reconstructs evaluation after the daemon restarts.
func TestRetainedWorkspacesAreIsolatedAndReconstructAfterRestart(t *testing.T) {
	spec := newScenario(t)
	at := spec.retainedPlane()
	first, other := spec.retainedTool(at), spec.retainedTool(at)
	spec.Expect(first.Workspace).NotTo(Equal(other.Workspace))
	spec.Expect(first.Sandbox).NotTo(Equal(other.Sandbox))
	one, two := first.observeRetained(), other.observeRetained()
	first.retainedModule("example.com/private")
	first.uniqueRetainedProgram()
	spec.Expect(first.retainedToolProject().GetProvides()).To(ContainElement("go:example.com/private"))
	warm := first.build(retainedCompileOnly)
	spec.Expect(warm.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_EXECUTED))
	warm.retainedStages(one, true, false, false)

	cold := other.build(retainedCompileOnly)
	cold.retainedCompileResult()
	cold.retainedStages(two, false, false, true)
	spec.Expect(other.retainedToolProject().GetProvides()).To(ContainElement("go:example.com/tool"))
	spec.Expect(other.retainedToolProject().GetProvides()).NotTo(ContainElement("go:example.com/private"))
	spec.Expect(one.TraceSpansFor("cicd.pipeline.run.id", cold.run.GetId())).To(BeEmpty())
	spec.Expect(two.TraceSpansFor("cicd.pipeline.run.id", warm.run.GetId())).To(BeEmpty())

	before := first.retainedDaemonIdentity()
	first.pause()
	// Reopen waits for the workspace daemon. ResumeSandbox alone starts
	// its sandbox and can return before the build API is listening.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	reopened, _, err := made(ctx, making{Server: first.Server, WorkspaceSpec: frontseat.WorkspaceSpec{Name: first.Workspace, AuthorizedKey: testidentity.Authorized()}})
	cancel()
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(reopened).To(Equal(first.reached))
	spec.Expect(first.retainedDaemonIdentity()).NotTo(Equal(before))
	restartedCollector := first.observeRetained()
	restarted := first.build(retainedCompileOnly)
	spec.Expect(restarted.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_GRID_CACHE))
	restarted.retainedStages(restartedCollector, false, false, true)
	spec.Expect(first.retainedToolProject().GetProvides()).To(ContainElement("go:example.com/private"))
	recorded, err := buildsAt(first.Build).DescribeBuild(context.Background(), &buildv1.DescribeBuildRequest{BuildId: warm.run.GetId()})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(recorded.GetBuild().GetId()).To(Equal(warm.run.GetId()))
	spec.Expect(recorded.GetBuild().GetExitCode()).To(Equal(buildv1.ExitCode_EXIT_CODE_SUCCESS))
	first.delete()
	other.delete()
}
