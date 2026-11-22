package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	sandboxv1 "github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
)

// A server that stops leaves its sandboxes running, and the one that
// starts in its place is the only thing that can end them. It finds
// them: each is listed as it was asked for, is deleted through it, and
// the request that made it still makes no second one.
func TestASandboxIsStillTheServicesWhenTheServerStartsAgain(t *testing.T) {
	ctx := context.Background()
	provider, state := drivertest.NewMemory(), t.TempDir()
	first := started(t, provider, state)
	request := box(func(spec *sandboxv1.SandboxSpec) {
		spec.Labels = map[string]string{"owner": "someone"}
	})
	request.RequestId = "5d2f3c0a-8f0b-4c59-a1cf-0a4c3b1a9d10"
	made, err := first.CreateSandbox(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	was := made.GetSandbox()

	second := started(t, provider, state)

	listed, err := second.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{LabelSelector: "owner=someone"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetSandboxes()) != 1 {
		t.Fatalf("the server that started again lists %v, want the sandbox the first made", listed.GetSandboxes())
	}
	now := listed.GetSandboxes()[0]
	if now.GetId() != was.GetId() || !proto.Equal(now.GetSpec(), was.GetSpec()) {
		t.Errorf("it is listed as %v, want it as it was asked for: %v", now, was)
	}
	if !now.GetCreateTime().AsTime().Equal(was.GetCreateTime().AsTime()) {
		t.Errorf("it was made at %v, want %v as before", now.GetCreateTime().AsTime(), was.GetCreateTime().AsTime())
	}

	again, err := second.CreateSandbox(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if there, _ := provider.List(ctx, nil); again.GetSandbox().GetId() != was.GetId() || len(there) != 1 {
		t.Errorf("the request sent again was answered with %s and there are %d sandboxes, want %s and one", again.GetSandbox().GetId(), len(there), was.GetId())
	}

	if _, err := second.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: was.GetId()}); err != nil {
		t.Fatalf("it could not be deleted through the server that started again: %v", err)
	}
	if there, _ := provider.List(ctx, nil); len(there) != 0 {
		t.Errorf("deleted, the provider still holds %d", len(there))
	}
	if left, _ := os.ReadDir(state); len(left) != 0 {
		t.Errorf("deleted, its record is still kept: %v", left)
	}
}

func TestARecordOfASandboxThatIsGoneIsDroppedWhenTheServerStarts(t *testing.T) {
	ctx := context.Background()
	provider, state := drivertest.NewMemory(), t.TempDir()
	first := started(t, provider, state)
	made, err := first.CreateSandbox(ctx, box(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Delete(ctx, made.GetSandbox().GetId()); err != nil {
		t.Fatal(err)
	}

	second := started(t, provider, state)

	if _, err := second.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: made.GetSandbox().GetId()}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("err = %v, want the sandbox not found", err)
	}
	if left, _ := os.ReadDir(state); len(left) != 0 {
		t.Errorf("its record is still kept: %v", left)
	}
}

// A sandbox that was not made leaves no record: a server that starts
// would otherwise look for it every time.
func TestASandboxThatWasNotMadeLeavesNoRecord(t *testing.T) {
	state := t.TempDir()
	s := started(t, drivertest.NewMemory(), state)

	_, err := s.CreateSandbox(context.Background(), box(func(spec *sandboxv1.SandboxSpec) {
		spec.Egress = &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_LISTED, Rules: []*sandboxv1.EgressRule{{Host: "example.com"}}}
	}))

	if err == nil {
		t.Fatal("the memory driver made a sandbox that may reach only listed hosts")
	}
	if left, _ := os.ReadDir(state); len(left) != 0 {
		t.Errorf("a record is kept of what was not made: %v", left)
	}
}

// A record written with what this server does not know, a field since
// removed, is not read in part: the server does not start, and says which
// file it is and that it can be removed.
func TestARecordThisServerCannotReadIsNamedByItsFile(t *testing.T) {
	state := t.TempDir()
	file := filepath.Join(state, "sb-1.json")
	if err := os.WriteFile(file, []byte(`{"spec":{"image":"example.com/box@sha256:`+strings.Repeat("a", 64)+`","ttl":"60s"},"created":"2026-11-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(Options{Driver: drivertest.NewMemory(), State: state})
	if err == nil {
		t.Fatal("a record with a field this server does not know was read")
	}
	for _, want := range []string{file, "remove the file", "ttl"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the server says %q, which does not say %q", err, want)
		}
	}
	// The control: without the field, the same record is read.
	if err := os.WriteFile(file, []byte(`{"spec":{"image":"example.com/box@sha256:`+strings.Repeat("a", 64)+`"},"created":"2026-11-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := open(state)
	if err != nil {
		t.Fatal(err)
	}
	if records, err := files.all(); err != nil || len(records) != 1 {
		t.Errorf("the record without the field is read as %v, %v", records, err)
	}
}
