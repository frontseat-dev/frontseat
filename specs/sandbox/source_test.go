package sandbox_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/frontseat-dev/frontseat/sdk/go"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// A workspace has one writer of what it holds: the control plane, which
// keeps what it was asked for and tells its daemon. Inside its sandbox
// its source is fixed, and the command line there asks its daemon to
// build, run and release alone; it is filled from outside, by its name.
// The daemon's source API, through which the control plane tells it, is
// led to by no route of the control plane's.

// Workspaces in sandboxes / Inside a workspace its source is fixed, and it is filled from outside.
func TestInsideAWorkspaceItsSourceIsFixedAndItIsFilledFromOutside(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.workspaceInASandboxOf("tool")
	ctx := context.Background()
	before := spec.describedAt(ctx, workspace)
	spec.Expect(before.GetSpec().GetEntities()).To(HaveLen(1))

	// Inside, adding is refused by name, before the control plane is
	// asked, and so is describing it; nothing is brought.
	printed, exit := workspace.run("frontseat workspace add --entity chain 2>&1")
	spec.Expect(exit).NotTo(Equal(0))
	spec.Expect(printed).To(ContainSubstring("its source is fixed"))
	printed, exit = workspace.run("frontseat workspace describe 2>&1")
	spec.Expect(exit).NotTo(Equal(0))
	spec.Expect(printed).To(ContainSubstring("its source is fixed"))
	inside := spec.describedAt(ctx, workspace)
	spec.Expect(inside.GetSpec().GetEntities()).To(HaveLen(1))
	spec.Expect(repositoriesOf(inside)).To(Equal(repositoriesOf(before)))
	// The command line inside still asks the daemon there to build.
	spec.Expect(workspace.exits("frontseat builds")).To(Equal(0))

	// The control: the same request from outside is kept in the spec the
	// control plane keeps, and brought.
	adding, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	// chain is in the repository its sandbox was made to reach: one in
	// another is beyond it, which is said when the sandbox is made.
	spec.Expect(workspace.sdk().Add(adding, frontseat.WorkspaceSpec{Entities: []entity.Pattern{{Kind: "component", Name: "chain"}}})).To(Succeed())
	after := spec.describedAt(ctx, workspace)
	spec.Expect(after.GetSpec().GetEntities()).To(HaveLen(2))
	spec.Expect(after.GetStatus().GetState()).To(Equal(workspacev1.WorkspaceStatus_STATE_READY))
	spec.Expect(locatedOf(after)).To(ContainElement("chain@lab/chain"))
	spec.Expect(locatedOf(before)).NotTo(ContainElement("chain@lab/chain"))
}

// Workspaces in sandboxes / The daemon's source API is the control plane's alone: no route leads a caller there.
func TestTheDaemonsSourceAPIIsTheControlPlanesAlone(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	// Through the workspace's build route, as its owner, a call of the
	// source API is not led on, refused as no call of the route's; one
	// of the build API is.
	for _, call := range []string{"SetSource", "WaitSource"} {
		status, said := spec.postAs(workspace.Build+"/frontseat.source.v1alpha1.SourceService/"+call, ownerToken(workspace.Server))
		spec.Expect(status).To(Equal(http.StatusNotImplemented), call+": "+said)
		spec.Expect(said).To(ContainSubstring("is none of its calls"), call)
	}
	status, said := spec.postAs(workspace.Build+"/frontseat.build.v1alpha1.BuildService/ListBuilds", ownerToken(workspace.Server))
	spec.Expect(status).To(Equal(http.StatusOK), said)
	// Inside the sandbox, the daemon's build port serves no source API.
	spec.Expect(workspace.prints("curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:7600/frontseat.source.v1alpha1.SourceService/WaitSource")).To(Equal("404"))
	// The control: the control plane told the daemon what to hold
	// through it, and reads back what it holds.
	held := spec.describedAt(context.Background(), workspace)
	spec.Expect(held.GetStatus().GetState()).To(Equal(workspacev1.WorkspaceStatus_STATE_READY))
	spec.Expect(held.GetStatus().GetRepositories()).NotTo(BeEmpty())
}

// describedAt is a workspace as the control plane that keeps it says.
func (spec *scenario) describedAt(ctx context.Context, w *Workspace) *workspacev1.Workspace {
	spec.Helper()
	said, err := workspacesAt(w.Server).DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: w.Workspace})
	spec.Expect(err).NotTo(HaveOccurred())
	return said.GetWorkspace()
}

// repositoriesOf are the names of the repositories a workspace holds.
func repositoriesOf(w *workspacev1.Workspace) []string {
	var out []string
	for _, r := range w.GetStatus().GetRepositories() {
		out = append(out, r.GetName())
	}
	return out
}

// postAs posts an empty message to an address, presenting a token as a
// bearer's, and returns the status and what was answered.
func (spec *scenario) postAs(at, token string) (int, string) {
	spec.Helper()
	request, err := http.NewRequest(http.MethodPost, at, strings.NewReader("{}"))
	spec.Expect(err).NotTo(HaveOccurred())
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	spec.Expect(err).NotTo(HaveOccurred())
	defer response.Body.Close()
	said, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	return response.StatusCode, string(said)
}

// Workspaces in sandboxes / A workspace of an entity reaches the repositories it resolved to, and not the catalog.
//
// The control plane resolves an entity through its Backstage, and its
// workspace's sandbox is let reach the repositories the entity resolved
// to and nothing else of where they are: the box never asks the
// catalog, and a repository of the same forge the resolution did not
// name is refused. The control is a repository it resolved to, fetched
// from inside.
func TestAWorkspaceOfAnEntityReachesWhatItResolvedToAndNotTheCatalog(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.workspaceInASandboxOf("gateway")

	spec.Expect(workspace.exits("git ls-remote -- " + quoted(company.Repository("platform")) + " >/dev/null")).To(Equal(0))
	spec.Expect(workspace.exits("git ls-remote -- " + quoted(company.Repository("lab")) + " >/dev/null 2>&1")).NotTo(Equal(0))
	at, err := url.Parse(companyCatalog.URL)
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(workspace.exits("curl -sf --max-time 10 -o /dev/null http://host.docker.internal:" + at.Port() + "/api/catalog/entities/by-name/component/default/gateway")).NotTo(Equal(0))
	spec.Expect(locatedOf(spec.describedAt(context.Background(), workspace))).To(Equal([]string{"gateway@platform/gateway", "greet@services/greet"}))
}

// locatedOf is where a workspace says each of its entities is, as
// name@repository/root.
func locatedOf(w *workspacev1.Workspace) []string {
	var out []string
	for _, each := range w.GetStatus().GetEntities() {
		out = append(out, each.GetRef().GetName()+"@"+each.GetRepository()+"/"+each.GetRoot())
	}
	return out
}
