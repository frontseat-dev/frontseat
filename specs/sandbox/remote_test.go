package sandbox_test

import (
	"context"
	"testing"

	connect "connectrpc.com/connect/v2"
	. "github.com/onsi/gomega"

	"github.com/frontseat-dev/frontseat/sdk/go"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
)

// A workspace is local or remote. The control plane of this repository
// serves local workspaces, and was given nothing that serves stored
// repositories: a remote workspace is refused by name, and nothing of
// it is made.
//
// Workspaces of a control plane / A remote workspace, asked of a control plane given no store, is refused by name, and nothing is made.
func TestARemoteWorkspaceIsRefusedByNameByAControlPlaneGivenNoStoreAndNothingIsMade(t *testing.T) {
	spec := newScenario(t)
	ctx := context.Background()
	local := spec.cloning(spec.aRepository())

	// Asked for as a person's client asks, through the SDK: each of what
	// makes a workspace remote, alone and together.
	for what, remote := range map[string]frontseat.WorkspaceSpec{
		"a stored repository":          {Stored: []frontseat.StoredRepository{{Repository: "code"}}},
		"a task":                       {Repositories: local.Repositories, Task: "fix-greeting"},
		"a landing":                    {Repositories: local.Repositories, Land: true},
		"a stored repository, landing": {Stored: []frontseat.StoredRepository{{Repository: "code"}}, Task: "fix-greeting", Land: true},
	} {
		name := aName()
		remote.Name, remote.Image = name, image
		opened, err := ownedClient(server).Open(ctx, remote)
		reason, domain := frontseat.Reason(err)
		spec.Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnimplemented), "%s: %v", what, err)
		spec.Expect(reason).To(Equal(frontseat.ReasonStoreNotServed), what)
		spec.Expect(domain).To(Equal(frontseat.DomainWorkspace), what)
		spec.Expect(opened.Workspace).To(BeNil(), what)
		// Nothing is made: no workspace of the name, and no sandbox that
		// carries it.
		_, err = workspacesAt(server).DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: name})
		spec.Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound), "%s: the workspace refused is there: %v", what, err)
		spec.Expect(spec.sandboxesLabelled("frontseat.dev/workspace="+name)).To(BeEmpty(), what)
	}

	// The control: the same repository, asked for local, is made, and
	// brought more by a spec that asks for the store it is refused, by
	// the same name, and is as it was.
	local.Server, local.Name, local.Image = server, aName(), image
	held, _, err := made(ctx, local)
	spec.Cleanup(func() { ended(local.Name) })
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(spec.sandboxesLabelled("frontseat.dev/workspace=" + held.Workspace)).To(HaveLen(1))
	described, err := workspacesAt(server).DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: held.Workspace})
	spec.Expect(err).NotTo(HaveOccurred())
	holds := described.GetWorkspace().GetStatus().GetRepositories()
	spec.Expect(holds).To(HaveLen(1))
	spec.Expect(holds[0].GetStored()).To(BeEmpty(), "a local workspace's repository is cloned, and of no store")
	spec.Expect(holds[0].GetRemote()).NotTo(BeEmpty())
	more := described.GetWorkspace().GetSpec()
	more.Stored = []*workspacev1.StoredRepository{{Repository: "code", Name: "stored"}}
	_, err = workspacesAt(server).UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: held.Workspace, Spec: more})
	reason, _ := frontseat.Reason(err)
	spec.Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnimplemented), "a stored repository added to a local workspace: %v", err)
	spec.Expect(reason).To(Equal(frontseat.ReasonStoreNotServed))
	after := spec.ready(ctx, workspacesAt(server), held.Workspace)
	spec.Expect(after.GetSpec().GetStored()).To(BeEmpty())
	spec.Expect(after.GetStatus().GetRepositories()).To(HaveLen(1))
}
