package sandbox_test

import (
	"context"
	"time"

	connect "connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"testing"
)

// Repository settings are mutable inputs even though the daemon's loaded Wasm
// plugins are fixed. The graph and the worker must agree after a warm settings
// change; a cached success cannot hide malformed settings or prevent repair.
// revalidates repository options for graph queries and real builds, refuses malformed settings and recovers.
func TestRetainedWorkspaceRevalidatesOptionsAndRecoversAfterMalformedSettings(t *testing.T) {
	spec := newScenario(t)
	w := spec.retainedTool(spec.retainedPlane())
	collector := w.observeRetained()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	graph, runs := graphAt(w.Build), buildsAt(w.Build)
	original := w.holds("/work/lab/frontseat.yaml")
	w.uniqueRetainedProgram()
	// The same source succeeds with the tag absent and fails with it set.
	// Its error is compiled on the worker, not predicted by the host.
	w.copyIn("/work/lab/tool/option_probe.go", "//go:build retained_option_probe\n\npackage main\n\nvar retainedOptionProbe int = \"selected by the changed option\"\n")
	before := w.retainedToolProject()
	first := w.build(retainedCompileOnly)
	spec.Expect(first.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_EXECUTED))
	first.retainedStages(collector, true, false, false)

	w.copyIn("/work/lab/frontseat.yaml", "plugins:\n  go: {tags: [retained_option_probe]}\n  backstage: {}\n")
	changed := w.retainedToolProject()
	var tags []*buildv1.ProjectOption
	for _, option := range changed.GetOptions() {
		if option.GetName() == "tags" {
			tags = append(tags, option)
		}
	}
	spec.Expect(tags).To(HaveLen(1))
	spec.Expect(tags[0].GetValue().GetStringListValue().GetValues()).To(Equal([]string{"retained_option_probe"}))
	spec.Expect(tags[0].GetOrigin().GetRepository()).To(Equal("lab"))
	spec.Expect(tags[0].GetOrigin().GetPath()).To(Equal("frontseat.yaml"))
	spec.Expect(proto.Equal(before, changed)).To(BeFalse())
	failed := w.build(retainedCompileOnly)
	spec.Expect(failed.outcome()).To(Equal("BUILD_FAILURE"))
	failed.retainedStages(collector, true, false, false)
	actions, err := runs.ListBuildActions(ctx, &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: failed.run.GetId()})
	spec.Expect(err).NotTo(HaveOccurred())
	var compile []*buildv1.ActionResult
	for _, action := range actions.GetActions() {
		if action.GetLabel() == retainedCompile {
			compile = append(compile, action)
		}
	}
	spec.Expect(compile).To(HaveLen(1))
	spec.Expect(compile[0].GetOutcome()).To(Equal(buildv1.Outcome_OUTCOME_FAILED))
	spec.Expect(compile[0].GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_EXECUTED))
	logs, err := frontseat.Collect(w.sdk().BuildByID(failed.run.GetId()).Logs(ctx, retainedCompile))
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(logs).To(ContainElement(HaveField("Text", ContainSubstring("selected by the changed option"))))

	w.copyIn("/work/lab/frontseat.yaml", "plugins: [\n")
	_, projectErr := graph.ListProjects(ctx, &buildv1.ListProjectsRequest{PageSize: 1000})
	_, entityErr := graph.ListEntities(ctx, &buildv1.ListEntitiesRequest{PageSize: 1000})
	for _, refusal := range []error{projectErr, entityErr} {
		spec.Expect(refusal).To(HaveOccurred())
		spec.Expect(connect.CodeOf(refusal)).To(Equal(connect.CodeFailedPrecondition))
		spec.Expect(refusal.Error()).To(ContainSubstring("frontseat.yaml"))
	}
	invalid := w.build(retainedCompileOnly)
	spec.Expect(invalid.outcome()).NotTo(Equal("SUCCESS"))
	spec.Expect(invalid.run.GetFailure()).To(ContainSubstring("frontseat.yaml"))
	unplanned, err := runs.ListBuildActions(ctx, &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: invalid.run.GetId()})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(unplanned.GetActions()).To(BeEmpty())

	w.copyIn("/work/lab/frontseat.yaml", original)
	spec.Expect(proto.Equal(w.retainedToolProject(), before)).To(BeTrue())
	repaired := w.build(retainedCompileOnly)
	spec.Expect(repaired.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_GRID_CACHE))
	repaired.retainedStages(collector, true, false, false)
	again := w.build(retainedCompileOnly)
	spec.Expect(again.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_GRID_CACHE))
	again.retainedStages(collector, true, true, false)
	w.delete()
}
