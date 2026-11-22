package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/sandbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testcontainer"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
)

type interruptedSandboxes struct {
	sandboxv1alpha1connect.SandboxServiceClient
	loseCreate, refuseDelete bool
	created                  string
	deleted                  []string
}

func (c *interruptedSandboxes) CreateSandbox(ctx context.Context, req *sandboxv1.CreateSandboxRequest) (*sandboxv1.CreateSandboxResponse, error) {
	answer, err := c.SandboxServiceClient.CreateSandbox(ctx, req)
	if err == nil && c.loseCreate {
		c.loseCreate = false
		c.created = answer.GetSandbox().GetId()
		return nil, connect.NewError(connect.CodeUnavailable, "successful creation reply lost")
	}
	return answer, err
}

func (c *interruptedSandboxes) DeleteSandbox(ctx context.Context, req *sandboxv1.DeleteSandboxRequest) (*sandboxv1.DeleteSandboxResponse, error) {
	c.deleted = append(c.deleted, req.SandboxId)
	if c.refuseDelete {
		c.refuseDelete = false
		return nil, connect.NewError(connect.CodeUnavailable, "transient deletion failure")
	}
	return c.SandboxServiceClient.DeleteSandbox(ctx, req)
}

func runtimeSandboxes(t *testing.T, wrap ...func(http.Handler) http.Handler) (*runner, sandboxv1alpha1connect.SandboxServiceClient) {
	testrequired.Stack(t)
	t.Helper()
	served, err := sandbox.New(sandbox.Options{Driver: testcontainer.Driver{}, PublicURL: "http://runner-test", State: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	handler := served.Handler()
	r := &runner{}
	for _, wrapper := range wrap {
		handler = wrapper(handler)
	}
	r.creations.next = handler
	server := httptest.NewServer(&r.creations)
	t.Cleanup(server.Close)
	client := sandboxv1alpha1connect.NewSandboxServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	r.sandboxes = client
	return r, client
}

func runtimeAttempt(t *testing.T, r *runner) *attempt {
	t.Helper()
	a := &attempt{r: r, id: filepath.Base(t.TempDir()), mine: filepath.Join(t.TempDir(), "owned")}
	if err := os.Mkdir(a.mine, 0700); err != nil {
		t.Fatal(err)
	}
	var err error
	a.creating, err = r.creations.begin(a.id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if !a.remove(ctx) {
			t.Error("attempt cleanup failed")
		}
	})
	return a
}

func TestAnAttemptReconcilesARealCreationWhoseReplyWasLost(t *testing.T) {
	image := testbox.Plain(t)
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary reply", true: "lost reply and retried deletion"}[interrupted], func(t *testing.T) {
			r, client := runtimeSandboxes(t)
			transport := &interruptedSandboxes{SandboxServiceClient: client, refuseDelete: interrupted}
			r.sandboxes = transport
			a := runtimeAttempt(t, r)
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			provider, err := a.create(ctx, &sandboxv1.SandboxSpec{Image: image, Arguments: []string{"sleep", "600"}, Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK}})
			if err != nil {
				t.Fatal(err)
			}
			transport.loseCreate = interrupted
			id, err := a.create(ctx, &sandboxv1.SandboxSpec{Image: image, Arguments: []string{"sleep", "600"}, Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK}})
			if interrupted {
				if err == nil || transport.created == "" {
					t.Fatalf("reply was not lost after a real creation: %s %v", id, err)
				}
			} else if err != nil || id == "" {
				t.Fatalf("allowed creation: %s %v", id, err)
			}
			before, err := client.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{LabelSelector: attemptLabel + "=" + a.id})
			if err != nil || len(before.GetSandboxes()) != 2 {
				t.Fatalf("creation control: %v %v", before, err)
			}
			cancel() // Cleanup must work after the caller has gone away.
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer stop()
			if !a.remove(cleanup) {
				t.Fatal("cleanup retained admission after reconciling its sandbox")
			}
			if !a.remove(cleanup) {
				t.Fatal("repeating cleanup failed")
			}
			if interrupted && (len(transport.deleted) != 3 || transport.deleted[0] != transport.created || transport.deleted[1] != transport.created || transport.deleted[2] != provider) {
				t.Fatalf("dependency released before consumer deletion succeeded: %v", transport.deleted)
			}
			after, err := client.ListSandboxes(cleanup, &sandboxv1.ListSandboxesRequest{LabelSelector: attemptLabel + "=" + a.id})
			if err != nil || len(after.GetSandboxes()) != 0 {
				t.Fatalf("cleanup left owned sandboxes: %v %v", after, err)
			}
			if _, err := os.Stat(a.mine); !os.IsNotExist(err) {
				t.Fatalf("attempt files remain: %v", err)
			}
		})
	}
}
