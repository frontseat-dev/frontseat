package sandbox_test

import (
	"context"
	"github.com/frontseat-dev/frontseat/testsupport/testtelemetry"
	"sync"
	"time"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"testing"
)

// answers concurrent graph callers while a build uses the same workspace.
func TestRetainedWorkspaceAnswersConcurrentGraphCallersDuringABuild(t *testing.T) {
	spec := newScenario(t)
	w := spec.retainedTool(spec.retainedPlane())
	collector := w.observeRetained()
	graph := graphAt(w.reached)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	want, err := graph.ListProjects(ctx, &buildv1.ListProjectsRequest{PageSize: 1000})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(want.GetProjects()).NotTo(BeEmpty())
	w.uniqueRetainedProgram()
	const callers = 16
	type response struct {
		projects *buildv1.ListProjectsResponse
		err      error
	}
	answers := make(chan response, callers)
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(callers)
	spec.Cleanup(func() { cancel(); group.Wait() })
	for range callers {
		go func() {
			defer group.Done()
			<-start
			got, err := graph.ListProjects(ctx, &buildv1.ListProjectsRequest{PageSize: 1000})
			answers <- response{got, err}
		}()
	}
	close(start)
	built := w.build(retainedCompileOnly)
	spec.Expect(built.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_EXECUTED))
	// The program changed, and what its plugin answers of it did not: the
	// evaluation kept stands, and whichever came first to the changed
	// file, this build or a graph caller beside it, asked its one plugin
	// again. The build asked it or was spared it; it asked no other.
	stages := built.stages(collector)
	spec.Expect(testtelemetry.RequireBool(spec.TB, stages.evaluate, "retained")).To(BeTrue())
	spec.Expect(testtelemetry.RequireBool(spec.TB, stages.plan, "retained")).To(BeFalse())
	spec.Expect(testtelemetry.RequireInt(spec.TB, stages.evaluate, "asked")).To(BeNumerically("<=", 1))
	spec.Expect(testtelemetry.RequireInt(spec.TB, stages.evaluate, "again")).To(BeZero())
	group.Wait()
	for range callers {
		got := <-answers
		spec.Expect(got.err).NotTo(HaveOccurred())
		spec.Expect(proto.Equal(got.projects, want)).To(BeTrue(), "every graph caller must receive the same validated workspace facts")
	}
	unchanged := w.build(retainedCompileOnly)
	spec.Expect(unchanged.retainedCompileResult().GetProvenance()).To(Equal(buildv1.Provenance_PROVENANCE_GRID_CACHE))
	unchanged.retainedStages(collector, true, true, false)
	w.delete()
}
