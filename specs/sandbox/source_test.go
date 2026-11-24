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
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// A workspace has one writer of what it holds: the control plane, which
// keeps what it was asked for and tells its box's supervisor. Inside its
// sandbox its source is fixed, and the command line there builds, runs
// and releases alone; it is filled from outside, by its name. The
// supervisor's source API, through which the control plane tells it, is
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
	// The command line inside still builds, through the build daemon it
	// starts there.
	spec.Expect(workspace.exits("frontseat build list")).To(Equal(0))

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

// Workspaces in sandboxes / The supervisor's source API is the control plane's alone: no route leads a caller there.
func TestTheSupervisorsSourceAPIIsTheControlPlanesAlone(t *testing.T) {
	spec := newScenario(t)

	workspace := spec.theWorkspace()

	// At the control plane, as the workspace's owner, a call of the
	// source API leads nowhere, beneath the workspace's name or at the
	// control plane's own address: it is no path the control plane has.
	for _, call := range []string{"SetSource", "WaitSource"} {
		for _, beneath := range []string{"", "/workspaces/" + workspace.Workspace + "/build", "/workspaces/" + workspace.Workspace + "/source"} {
			status, said := spec.postAs(workspace.Server+beneath+"/frontseat.source.v1alpha1.SourceService/"+call, ownerToken(workspace.Server))
			spec.Expect(status).To(Equal(http.StatusNotFound), beneath+" "+call+": "+said)
		}
	}
	// The build daemon in the sandbox, which a caller is carried to,
	// serves no source API; the control is a call of the build API,
	// which it answers.
	daemon := workspace.daemon()
	answer, err := daemon.HTTP.Post(daemon.Address+"/frontseat.source.v1alpha1.SourceService/WaitSource", "application/json", strings.NewReader("{}"))
	spec.Expect(err).NotTo(HaveOccurred())
	answer.Body.Close()
	spec.Expect(answer.StatusCode).To(Equal(http.StatusNotFound))
	_, err = buildsAt(workspace).ListBuilds(context.Background(), &buildv1.ListBuildsRequest{PageSize: 1})
	spec.Expect(err).NotTo(HaveOccurred())
	// The control: the control plane told the supervisor what to hold
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
