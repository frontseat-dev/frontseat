package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
)

// delayedDelivery models a lost answer from a transport that has already
// accepted the original message, but delivers it after a later request. The
// caller's cancellation cannot recall a message that the transport has accepted.
type delayedDelivery struct {
	sandboxv1alpha1connect.SandboxServiceClient
	delay    bool
	release  chan struct{}
	finished chan error
}

func (c *delayedDelivery) CreateSandbox(ctx context.Context, req *sandboxv1.CreateSandboxRequest) (*sandboxv1.CreateSandboxResponse, error) {
	if c.delay {
		c.delay = false
		go func() {
			<-c.release
			late, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			_, err := c.SandboxServiceClient.CreateSandbox(late, req)
			c.finished <- err
		}()
		return nil, connect.NewError(connect.CodeUnavailable, "the accepted original request has no reply")
	}
	return c.SandboxServiceClient.CreateSandbox(ctx, req)
}

func TestAnOriginalCreateDeliveredAfterCleanupCannotRecreate(t *testing.T) {
	image := testbox.Plain(t)
	r, client := runtimeSandboxes(t)
	transport := &delayedDelivery{SandboxServiceClient: client, release: make(chan struct{}), finished: make(chan error, 1)}
	r.sandboxes = transport
	a := runtimeAttempt(t, r)
	ctx, cancel := context.WithCancel(t.Context())
	spec := &sandboxv1.SandboxSpec{Image: image, Arguments: []string{"sleep", "600"}, Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK}}
	if _, err := a.create(ctx, spec); err != nil {
		t.Fatalf("admitted control: %v", err)
	}
	transport.delay = true
	_, err := a.create(ctx, spec)
	if err == nil {
		t.Fatal("creation reply was not lost")
	}
	cancel()
	cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	if !a.remove(cleanup) {
		t.Fatal("cleanup did not report success")
	}
	before, err := client.ListSandboxes(cleanup, &sandboxv1.ListSandboxesRequest{LabelSelector: attemptLabel + "=" + a.id})
	if err != nil || len(before.GetSandboxes()) != 0 {
		t.Fatalf("cleanup control: %v %v", before, err)
	}
	close(transport.release)
	if err := <-transport.finished; connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("late delivery was not refused at admission: %v", err)
	}
	after, err := client.ListSandboxes(cleanup, &sandboxv1.ListSandboxesRequest{LabelSelector: attemptLabel + "=" + a.id})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range after.GetSandboxes() {
		if _, err := client.DeleteSandbox(cleanup, &sandboxv1.DeleteSandboxRequest{SandboxId: s.Id}); err != nil {
			t.Errorf("probe cleanup: %v", err)
		}
	}
	if len(after.GetSandboxes()) != 0 {
		t.Fatalf("successful cleanup released admission, but a delayed original create left %d live sandbox(s)", len(after.GetSandboxes()))
	}
}

type sweepingClient struct {
	sandboxv1alpha1connect.SandboxServiceClient
	deleted []string
	refuse  bool
}

func (c *sweepingClient) ListSandboxes(context.Context, *sandboxv1.ListSandboxesRequest) (*sandboxv1.ListSandboxesResponse, error) {
	return &sandboxv1.ListSandboxesResponse{Sandboxes: []*sandboxv1.Sandbox{
		{Id: "provider", Spec: &sandboxv1.SandboxSpec{Labels: map[string]string{label: "1", "dev.frontseat.runner-order": "000"}}},
		{Id: "consumer", Spec: &sandboxv1.SandboxSpec{Labels: map[string]string{label: "1", "dev.frontseat.runner-order": "001"}}},
	}}, nil
}

func (c *sweepingClient) DeleteSandbox(_ context.Context, req *sandboxv1.DeleteSandboxRequest) (*sandboxv1.DeleteSandboxResponse, error) {
	c.deleted = append(c.deleted, req.SandboxId)
	if c.refuse && req.SandboxId == "consumer" {
		return nil, connect.NewError(connect.CodeUnavailable, "consumer still running")
	}
	return &sandboxv1.DeleteSandboxResponse{}, nil
}

func TestRestartCleanupKeepsDependenciesOfAnUndeletedConsumer(t *testing.T) {
	c := &sweepingClient{refuse: true}
	r := &runner{sandboxes: c, own: t.TempDir()}
	owned := filepath.Join(r.own, strings.Repeat("a", 32))
	keep := filepath.Join(r.own, "keep")
	for _, dir := range []string{owned, keep} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.sweep(t.Context()); err == nil {
		t.Fatal("startup accepted unreconciled capacity")
	}
	if len(c.deleted) != 1 || c.deleted[0] != "consumer" {
		t.Fatalf("deleted dependencies after failing to stop consumer: %v", c.deleted)
	}
	if _, err := os.Stat(owned); err != nil {
		t.Fatal("files removed while a sandbox still owned them")
	}
	c.refuse, c.deleted = false, nil
	if err := r.sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.deleted, ",") != "consumer,provider" {
		t.Fatalf("successful restart deletion order: %v", c.deleted)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatalf("abandoned attempt files remain: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("restart removed an unknown directory: %v", err)
	}
}

type inventoryClient struct {
	sandboxv1alpha1connect.SandboxServiceClient
	entered chan struct{}
	once    sync.Once
}

func (c *inventoryClient) ListSandboxes(ctx context.Context, req *sandboxv1.ListSandboxesRequest) (*sandboxv1.ListSandboxesResponse, error) {
	c.once.Do(func() { close(c.entered) })
	return c.SandboxServiceClient.ListSandboxes(ctx, req)
}

func TestCleanupJoinsAnAlreadyAdmittedCreation(t *testing.T) {
	image := testbox.Plain(t)
	admitted, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	r, client := runtimeSandboxes(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != sandboxv1alpha1connect.SandboxServiceCreateSandboxProcedure {
				next.ServeHTTP(w, req)
				return
			}
			buffer := httptest.NewRecorder()
			next.ServeHTTP(buffer, req)
			close(admitted)
			<-release
			for k, v := range buffer.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(buffer.Code)
			_, _ = w.Write(buffer.Body.Bytes())
		})
	})
	a := runtimeAttempt(t, r)
	inventory := &inventoryClient{SandboxServiceClient: client, entered: make(chan struct{})}
	r.sandboxes = inventory
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	created := make(chan error, 1)
	go func() {
		_, err := a.create(ctx, &sandboxv1.SandboxSpec{Image: image, Arguments: []string{"sleep", "600"}, Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK}})
		created <- err
	}()
	select {
	case <-admitted:
	case <-time.After(time.Minute):
		t.Fatal("creation was not admitted")
	}
	before, err := client.ListSandboxes(t.Context(), &sandboxv1.ListSandboxesRequest{LabelSelector: attemptLabel + "=" + a.id})
	if err != nil || len(before.GetSandboxes()) != 1 {
		t.Fatalf("admitted real-creation control: %v %v", before, err)
	}
	cancel()
	if err := <-created; err == nil {
		t.Fatal("creation response was not interrupted")
	}
	cleaned := make(chan bool, 1)
	cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	go func() { cleaned <- a.remove(cleanup) }()
	// Observe admission closed before checking that cleanup has not begun its
	// inventory. Container deletion latency cannot make this assertion pass.
	for {
		r.creations.mu.Lock()
		closed := r.creations.active[a.id] == nil
		r.creations.mu.Unlock()
		if closed {
			break
		}
		select {
		case <-cleanup.Done():
			t.Fatal("cleanup did not close admission")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case <-inventory.entered:
		t.Fatal("cleanup listed sandboxes before its admitted creation finished")
	case <-cleaned:
		t.Fatal("cleanup returned before its admitted handler finished")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-inventory.entered:
	case <-cleanup.Done():
		t.Fatal("cleanup did not inventory after joining")
	}
	if !<-cleaned {
		t.Fatal("cleanup failed after admitted creation finished")
	}
	after, err := client.ListSandboxes(t.Context(), &sandboxv1.ListSandboxesRequest{LabelSelector: attemptLabel + "=" + a.id})
	if err != nil || len(after.GetSandboxes()) != 0 {
		t.Fatalf("cleanup left admitted sandbox: %v %v", after, err)
	}
}
