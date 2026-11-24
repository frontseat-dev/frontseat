package frontseat

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"

	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
)

// A remote workspace is asked for by the stored repositories it holds,
// the task it works on and whether it lands, as the spec says them; a
// control plane that serves none refuses it by name, and the refusal is
// read by its reason, ReasonStoreNotServed, never its words. The control
// is a local workspace, which is asked for with nothing of the store.
func TestARemoteWorkspaceIsAskedForByWhatItHoldsOfTheStore(t *testing.T) {
	unserved := connect.NewError(connect.CodeUnimplemented, "this server serves no remote workspace")
	named, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Domain: DomainWorkspace, Reason: "STORE_NOT_SERVED"})
	if err != nil {
		t.Fatal(err)
	}
	p := &plane{refused: unserved.WithDetail(named)}
	fs := client(t, served(t, p))
	remote := WorkspaceSpec{Name: "fix", Task: "fix-refunds", Land: true, Stored: []StoredRepository{
		{Repository: "payments", Roots: []string{"refunds", "ledger"}, Task: "tidy"},
		{Repository: "acme/gateway", Name: "gateway", At: "v2"},
	}}
	_, err = fs.Open(context.Background(), remote)
	var refused *Error
	if !errors.As(err, &refused) || refused.Code != connect.CodeUnimplemented || refused.Reason != ReasonStoreNotServed || refused.Domain != DomainWorkspace {
		t.Errorf("a remote workspace asked of a control plane that serves none: %v, want an Error of unimplemented, %s, %s", err, ReasonStoreNotServed, DomainWorkspace)
	}
	payments := &workspacev1.StoredRepository{Repository: "payments", Roots: []string{"refunds", "ledger"}, Task: "tidy"}
	gateway := &workspacev1.StoredRepository{Repository: "acme/gateway", Name: "gateway", At: "v2"}
	want := &workspacev1.WorkspaceSpec{Stored: []*workspacev1.StoredRepository{payments, gateway}, Land: true, Task: "fix-refunds"}
	if len(p.created) != 1 || p.created[0].GetName() != "fix" || !proto.Equal(p.created[0].GetSpec(), want) {
		t.Fatalf("a remote workspace was asked for as %v, want once, as %v", p.created, want)
	}
	if _, err := fs.Open(context.Background(), here); err == nil {
		t.Fatal("the test's control plane made a workspace")
	}
	if local := p.created[1].GetSpec(); len(local.GetStored()) != 0 || local.GetTask() != "" || local.GetLand() {
		t.Errorf("the control, a local workspace, was asked for with %v on the task %q, landing %v", local.GetStored(), local.GetTask(), local.GetLand())
	}
}
