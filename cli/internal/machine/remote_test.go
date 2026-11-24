package machine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/frontseat-dev/frontseat/cli/internal/vcs"
	"github.com/frontseat-dev/frontseat/sdk/go"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
)

// The workspace of a checkout, asked for remote, is asked for by its
// repository as the store names it, which is as its version control
// system names it by its remote, on the task named, landing: nothing is
// to be cloned, and the checkout's branch is not asked for. It is the
// workspace that holds that repository from the store, and one that
// holds it as a clone is not it. The control is the same checkout
// opened local, which is asked for by its remote and branch, with
// nothing of the store.
func TestACheckoutsRemoteWorkspaceIsAskedForByItsRepositoryInTheStore(t *testing.T) {
	here := standing{at: vcs.Standing{Remote: "https://forge.example/acme/code.git", Branch: "feature"}}
	held := &plane{asked: make(chan *workspacev1.CreateWorkspaceRequest, 1), stored: "code"}
	opened, err := OpenCheckout(context.Background(), serve(t, held), OpenOptions{Remote: true, Task: "fix-greeting"}, here)
	if err != nil || opened.Repository != "code" {
		t.Fatalf("opened remote, in %q: %v; want the repository it holds from the store", opened.Repository, err)
	}
	spec := (<-held.asked).GetSpec()
	if stored := spec.GetStored(); len(stored) != 1 || stored[0].GetRepository() != "code" || stored[0].GetAt() != "" || stored[0].GetTask() != "" || stored[0].GetName() != "" {
		t.Errorf("asked for the stored repositories %v, want the checkout's, by its name, at no ref", stored)
	}
	if spec.GetTask() != "fix-greeting" || !spec.GetLand() || len(spec.GetRepositories()) != 0 || spec.GetPush() {
		t.Errorf("asked for on the task %q, landing %v, cloning %v; want its task, landing, and nothing cloned", spec.GetTask(), spec.GetLand(), spec.GetRepositories())
	}
	// With no task named, none is said: the control plane names one for
	// the workspace.
	unnamed := &plane{asked: make(chan *workspacev1.CreateWorkspaceRequest, 1), stored: "code"}
	if _, err := OpenCheckout(context.Background(), serve(t, unnamed), OpenOptions{Remote: true}, here); err != nil {
		t.Fatal(err)
	}
	if spec := (<-unnamed.asked).GetSpec(); spec.GetTask() != "" || !spec.GetLand() {
		t.Errorf("with no task named, asked for on the task %q, landing %v", spec.GetTask(), spec.GetLand())
	}
	// A workspace that holds the repository as a clone is not the remote
	// one.
	cloned := &plane{asked: make(chan *workspacev1.CreateWorkspaceRequest, 1), holds: here.at.Remote}
	if _, err := OpenCheckout(context.Background(), serve(t, cloned), OpenOptions{Remote: true}, here); err == nil {
		t.Error("a workspace that holds the repository as a clone was taken for the remote one")
	}
	// The control: local, by its remote and branch.
	local := &plane{asked: make(chan *workspacev1.CreateWorkspaceRequest, 1), holds: here.at.Remote}
	if _, err := OpenCheckout(context.Background(), serve(t, local), OpenOptions{}, here); err != nil {
		t.Fatal(err)
	}
	if spec := (<-local.asked).GetSpec(); len(spec.GetStored()) != 0 || spec.GetTask() != "" || spec.GetLand() || len(spec.GetRepositories()) != 1 || spec.GetRepositories()[0].GetRef() != "feature" {
		t.Errorf("the control, opened local, was asked for as %v", spec)
	}
	// And a local workspace is not one that holds it from the store.
	if _, err := OpenCheckout(context.Background(), serve(t, &plane{asked: make(chan *workspacev1.CreateWorkspaceRequest, 1), stored: "code"}), OpenOptions{}, here); err == nil {
		t.Error("a workspace that holds the repository from the store was taken for the local one")
	}
}

// A control plane that serves no remote workspace refuses one by name,
// and the refusal reaches whoever opened it as it was given, read by
// its reason: nothing is said to be opened.
func TestARemoteWorkspaceRefusedByNameIsReadByItsReason(t *testing.T) {
	unserved := connect.NewError(connect.CodeUnimplemented, "this server serves no remote workspace")
	named, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Domain: frontseat.DomainWorkspace, Reason: frontseat.ReasonStoreNotServed})
	if err != nil {
		t.Fatal(err)
	}
	refusing := &plane{asked: make(chan *workspacev1.CreateWorkspaceRequest, 1), refuses: unserved.WithDetail(named)}
	here := standing{at: vcs.Standing{Remote: "https://forge.example/acme/code.git", Branch: "main"}}
	opened, err := OpenCheckout(context.Background(), serve(t, refusing), OpenOptions{Remote: true}, here)
	var refused *frontseat.Error
	if !errors.As(err, &refused) || refused.Reason != frontseat.ReasonStoreNotServed || refused.Code != connect.CodeUnimplemented || opened.Workspace != nil {
		t.Errorf("a remote workspace asked of a control plane that serves none: %v, opening %v; want it refused by %s and nothing opened", err, opened.Workspace, frontseat.ReasonStoreNotServed)
	}
}

// A repository is held by a remote workspace by its name, and on the
// workspace's task: one named with a branch or tag, or whose remote
// gives it no name, is refused before anything is asked, and nothing of
// its remote is repeated. The control is a repository named alone.
func TestARepositoryIsStoredByItsNameAndAtNoRef(t *testing.T) {
	nameOf := standing{}.NameOf
	if got, err := Stored(frontseat.Repository{Remote: "https://forge.example/acme/code.git"}, nameOf); err != nil || got.Repository != "code" || got.At != "" {
		t.Errorf("the control, a repository named alone: %+v, %v", got, err)
	}
	for what, named := range map[string]frontseat.Repository{
		"a branch":            {Remote: "https://forge.example/acme/t0ken-code.git", Ref: "main"},
		"a remote of no name": {Remote: "https://forge.example/t0ken/"},
	} {
		if _, err := Stored(named, nameOf); err == nil || strings.Contains(err.Error(), "t0ken") {
			t.Errorf("%s: %v, want it refused, and nothing of the remote said", what, err)
		}
	}
}
