package sandbox_test

import (
	"context"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"

	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testsandbox"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	. "github.com/onsi/gomega"
)

// A workspace is what a person or an agent asks the control plane for:
// where a supervisor lives and what it manages, in one sandbox of its own.
// The control plane makes it, names it, says which there are, leads to
// its supervisor by its name, and ends it. A sandbox is its isolation, and
// is asked for by itself only by what is no person: a runner, a worker.
//
// These specs ask the workspace API as any client does, with the
// generated client and nothing of the command line's.
// Workspaces of a control plane / A workspace is asked for by what it holds, is named by it, and is reached by its name.
func TestAWorkspaceIsAskedForByWhatItHoldsIsNamedByItAndIsReachedByItsName(t *testing.T) {
	spec := newScenario(t)

	ctx := context.Background()

	repositories := spec.source()
	repositories[0].Ref = "main"

	made := spec.asked(ctx, &workspacev1.CreateWorkspaceRequest{Spec: &workspacev1.WorkspaceSpec{Repositories: repositories}})

	spec.Expect(made.GetCreated()).To(BeTrue())
	// It is named by the repository and where it stands.
	described := made.GetWorkspace()
	spec.Expect(described.GetName()).To(Equal("code-main"))
	// It holds what was asked for, as the control plane says by its
	// name.
	held, err := workspacesAt(server).DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "code-main"})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(held.GetWorkspace().GetName()).To(Equal("code-main"))
	spec.Expect(held.GetWorkspace().GetStatus().GetRepositories()).To(HaveLen(1))
	spec.Expect(held.GetWorkspace().GetStatus().GetRepositories()[0].GetRemote()).To(Equal(repositories[0].GetRemote()))
	// It is among the workspaces there are, ready.
	listed, err := workspacesAt(server).ListWorkspaces(ctx, &workspacev1.ListWorkspacesRequest{PageSize: 1000})
	spec.Expect(err).NotTo(HaveOccurred())
	var names []string
	for _, each := range listed.GetWorkspaces() {
		if each.GetName() == "code-main" {
			spec.Expect(each.GetStatus().GetState()).To(Equal(workspacev1.WorkspaceStatus_STATE_READY))
		}
		names = append(names, each.GetName())
	}
	spec.Expect(names).To(ContainElement("code-main"))
}

// Workspaces of a control plane / A workspace asked for again is the one there is, and one asked for otherwise is refused.
func TestAWorkspaceAskedForAgainIsTheOneThereIsAndOneAskedForOtherwiseIsRefused(t *testing.T) {
	spec := newScenario(t)

	ctx := context.Background()

	repositories := spec.source()
	name := aName()
	first := spec.asked(ctx, &workspacev1.CreateWorkspaceRequest{Name: name, Spec: &workspacev1.WorkspaceSpec{Repositories: repositories}})

	again := spec.asked(ctx, &workspacev1.CreateWorkspaceRequest{Name: name, Spec: &workspacev1.WorkspaceSpec{Repositories: repositories}})

	spec.Expect(first.GetCreated()).To(BeTrue())
	spec.Expect(again.GetCreated()).To(BeFalse())
	lives := livingIn(server, name)
	spec.Expect(lives).NotTo(BeEmpty())
	spec.Expect(spec.sandboxesOf(name)).To(ConsistOf(lives))
	// The same name asked for with other source is not that workspace,
	// and is not made over it.
	_, err := workspacesAt(server).CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: name, Spec: &workspacev1.WorkspaceSpec{Repositories: spec.source(), Image: image}})
	spec.Expect(connect.CodeOf(err)).To(Equal(connect.CodeAlreadyExists))
	spec.Expect(err).To(BeRefusedSaying("holds other source"))
	spec.Expect(spec.sandboxesOf(name)).To(ConsistOf(lives))
}

// Workspaces of a control plane / A workspace that is ended is gone, and its sandbox with it.
func TestAWorkspaceThatIsEndedIsGoneAndItsSandboxWithIt(t *testing.T) {
	spec := newScenario(t)

	ctx := context.Background()

	name := aName()
	spec.asked(ctx, &workspacev1.CreateWorkspaceRequest{Name: name, Spec: &workspacev1.WorkspaceSpec{Repositories: spec.source()}})
	workspaces := workspacesAt(server)
	lived := livingIn(server, name)
	spec.Expect(lived).NotTo(BeEmpty())

	_, err := workspaces.DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: name})
	spec.Expect(err).NotTo(HaveOccurred())

	// It ends after the call that asks for it, and is then gone.
	spec.Eventually(func() connect.Code {
		_, err := workspaces.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: name})
		return connect.CodeOf(err)
	}, 2*time.Minute, 250*time.Millisecond).Should(Equal(connect.CodeNotFound))
	_, err = testsandbox.Client(beneathOf(server)).DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: lived})
	spec.Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
	// The control: one that was never there is said so too.
	_, err = workspaces.DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: aName()})
	spec.Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
}

// Workspaces of a control plane / What is no workspace is not made.
func TestWhatIsNoWorkspaceIsNotMade(t *testing.T) {
	spec := newScenario(t)

	ctx := context.Background()

	workspaces := workspacesAt(server)
	// A name a workspace could have, so that a sandbox made for what was
	// refused would be found by it.
	sourceless := aName()
	for what, request := range map[string]*workspacev1.CreateWorkspaceRequest{
		"nothing asked for":   {Name: sourceless, Spec: &workspacev1.WorkspaceSpec{Image: image}},
		"a name that is none": {Name: "Not A Name", Spec: &workspacev1.WorkspaceSpec{Repositories: spec.source(), Image: image}},
		"a name that climbs":  {Name: "../elsewhere", Spec: &workspacev1.WorkspaceSpec{Repositories: spec.source(), Image: image}},
		"a name with a slash": {Name: "a/b", Spec: &workspacev1.WorkspaceSpec{Repositories: spec.source(), Image: image}},
		"a name of no length": {Name: "-", Spec: &workspacev1.WorkspaceSpec{Repositories: spec.source(), Image: image}},
	} {
		_, err := workspaces.CreateWorkspace(ctx, request)
		spec.Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument), what)
	}
	spec.Expect(spec.sandboxesOf(sourceless)).To(BeEmpty())
}

func (spec *scenario) asked(ctx context.Context, request *workspacev1.CreateWorkspaceRequest) *workspacev1.CreateWorkspaceResponse {
	spec.Helper()
	workspaces := workspacesAt(server)
	request.Spec.Image = image
	made, err := workspaces.CreateWorkspace(ctx, request)
	spec.Expect(err).NotTo(HaveOccurred())
	name := made.GetWorkspace().GetName()
	spec.Cleanup(func() { ended(name) })
	last := spec.ready(ctx, workspaces, name)
	// As it is once ready: where its supervisor is is known once its
	// sandbox is.
	return &workspacev1.CreateWorkspaceResponse{Workspace: last, Created: made.GetCreated()}
}

func (spec *scenario) source() []*workspacev1.RepositorySource {
	var out []*workspacev1.RepositorySource
	for _, r := range spec.cloning(spec.aRepository()).Repositories {
		out = append(out, &workspacev1.RepositorySource{Remote: r.Remote, Ref: r.Ref})
	}
	return out
}
