package sandbox_test

import (
	"context"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"testing"

	. "github.com/onsi/gomega"
)

// The daemon in a sandbox speaks to the grid itself, with the same plugins
// and inferred artifact bindings as any daemon. A consumer must compile
// real generated source without a generated copy in the checkout.
// Generated sources in sandboxes / Buf generation reaches a Go consumer through the sandbox API.
func TestBufGenerationReachesAGoConsumerThroughTheSandboxAPI(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.generatedSandbox()
	_, err := graphAt(workspace.reached).ListProjects(context.Background(), &buildv1.ListProjectsRequest{PageSize: 1000})
	spec.Expect(err).NotTo(HaveOccurred())
	built := workspace.build(func(r *buildv1.CreateBuildRequest) { r.Targets = []string{"//code/app:go.compile"} })
	spec.Expect(built.outcome()).To(Equal("SUCCESS"), built.run.String())
	actions, err := buildsAt(workspace.reached).ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: built.run.GetId()})
	spec.Expect(err).NotTo(HaveOccurred())
	var passed []string
	for _, action := range actions.GetActions() {
		if action.GetOutcome() == buildv1.Outcome_OUTCOME_PASSED {
			passed = append(passed, action.GetLabel())
		}
	}
	spec.Expect(passed).To(ContainElements("//code/api:buf.generate", "//code/app:go.compile"))
	spec.Expect(workspace.exits("test -e "+code+"/api/gen/go/hello/v1/hello.pb.go")).NotTo(Equal(0), "generation must not write the checkout")
}
