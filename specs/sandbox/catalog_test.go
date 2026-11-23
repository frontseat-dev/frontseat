package sandbox_test

import (
	"context"
	"testing"

	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"

	. "github.com/onsi/gomega"
)

// The catalog plugin runs in the sandbox's daemon, with the same SDK and API
// as the build plugins. Its descriptors must survive the real sandbox image,
// cloned source, Wasm runtime and client tunnel, including error recovery.
// Catalogs in sandboxes / The sandbox discovers catalog entities and recovers after a malformed descriptor is repaired.
func TestTheSandboxDiscoversCatalogEntitiesAndRecoversAfterAMalformedDescriptorIsRepaired(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.catalogWorkspace()
	graph := graphAt(workspace.reached)
	listed, err := graph.ListEntities(context.Background(), &buildv1.ListEntitiesRequest{PageSize: 1000})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(listed.GetEntities()).To(HaveLen(2))
	component, group := listed.GetEntities()[0], listed.GetEntities()[1]
	spec.Expect(component.GetRef().GetKind()).To(Equal("component"))
	spec.Expect(component.GetRef().GetName()).To(Equal("app"))
	spec.Expect(component.GetRoots()).To(Equal([]string{"app"}))
	spec.Expect(component.GetRelations()).To(HaveLen(1))
	spec.Expect(component.GetRelations()[0].GetType()).To(Equal("ownedBy"))
	spec.Expect(component.GetRelations()[0].GetTargetPresent()).To(BeTrue())
	spec.Expect(component.GetRelations()[0].GetTarget().GetKind()).To(Equal("group"))
	spec.Expect(component.GetRelations()[0].GetTarget().GetNamespace()).To(Equal("default"))
	spec.Expect(component.GetRelations()[0].GetTarget().GetName()).To(Equal("team"))
	spec.Expect(group.GetRef().GetKind()).To(Equal("group"))
	spec.Expect(group.GetRoots()).To(BeEmpty())
	spec.Expect(group.GetRelations()).To(HaveLen(1))
	spec.Expect(group.GetRelations()[0].GetType()).To(Equal("ownerOf"))
	spec.Expect(group.GetRelations()[0].GetTarget().GetKind()).To(Equal("component"))
	spec.Expect(group.GetRelations()[0].GetTarget().GetNamespace()).To(Equal("default"))
	spec.Expect(group.GetRelations()[0].GetTarget().GetName()).To(Equal("app"))

	workspace.copyIn(code+"/app/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: app, namespace: tenant.scope}\nspec: {type: service, lifecycle: production, owner: team}\n")
	_, err = graph.ListEntities(context.Background(), &buildv1.ListEntitiesRequest{PageSize: 1000})
	spec.Expect(err).To(BeRefusedSaying("metadata.namespace"))
	workspace.copyIn(code+"/app/catalog-info.yaml", sandboxCatalog)
	listed, err = graph.ListEntities(context.Background(), &buildv1.ListEntitiesRequest{PageSize: 1000})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(listed.GetEntities()).To(HaveLen(2))
	built := workspace.build()
	spec.Expect(built.outcome()).To(Equal("SUCCESS"))
	actions, err := buildsAt(workspace.reached).ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: built.run.GetId()})
	spec.Expect(err).NotTo(HaveOccurred())
	var compiled bool
	for _, action := range actions.GetActions() {
		if action.GetLabel() == "//code/app:go.compile" {
			spec.Expect(action.GetOutcome()).To(Equal(buildv1.Outcome_OUTCOME_PASSED))
			spec.Expect(action.GetEntity().GetName()).To(Equal("app"))
			spec.Expect(action.GetActionDigest().GetHash()).NotTo(BeEmpty())
			compiled = true
		}
	}
	spec.Expect(compiled).To(BeTrue(), "the sandbox must compile the discovered Go project")
}
