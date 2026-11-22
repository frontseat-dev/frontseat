package sandbox_test

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/sandbox"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
	"google.golang.org/protobuf/types/known/durationpb"
)

// serve serves the sandbox API over a driver that holds sandboxes in
// memory, and returns a client of it and the server's URL.
func serve(t *testing.T) (sandboxv1alpha1connect.SandboxServiceClient, *httptest.Server) {
	t.Helper()
	return serveOver(t, drivertest.NewMemory())
}

// serveOver serves the sandbox API over a driver the test keeps hold of.
func serveOver(t *testing.T, d driver.Driver) (sandboxv1alpha1connect.SandboxServiceClient, *httptest.Server) {
	t.Helper()
	return serving(t, func(address string) (http.Handler, error) { return sandbox.Handler(d, address, t.TempDir()) })
}

// serving serves the sandbox API that made returns for the address it is
// served at, until the test ends, and returns a client of it and the
// server.
func serving(t *testing.T, made func(address string) (http.Handler, error)) (sandboxv1alpha1connect.SandboxServiceClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	t.Cleanup(srv.Close)
	handler, err := made(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = handler
	client := sandboxv1alpha1connect.NewSandboxServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	return client, srv
}

func create(t *testing.T, client sandboxv1alpha1connect.SandboxServiceClient, spec *sandboxv1.SandboxSpec) *sandboxv1.Sandbox {
	t.Helper()
	created, err := client.CreateSandbox(context.Background(), &sandboxv1.CreateSandboxRequest{Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	return created.GetSandbox()
}

func TestASandboxIsMadeAndRuns(t *testing.T) {
	client, _ := serve(t)

	made := create(t, client, &sandboxv1.SandboxSpec{Image: "box", Ports: []int32{7600}})

	if !strings.HasPrefix(made.GetId(), "sbx_") || made.GetDriver() != "memory" {
		t.Errorf("made %s by %s, want an sbx_ id by the memory driver", made.GetId(), made.GetDriver())
	}
	waited, err := client.WaitSandbox(context.Background(), &sandboxv1.WaitSandboxRequest{SandboxId: made.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	last := waited.GetSandbox()
	if last.GetState() != sandboxv1.Sandbox_STATE_RUNNING {
		t.Errorf("the sandbox settled %v, want running", last.GetState())
	}
}

func TestWhatTheDriverCannotEnforceIsRefusedByNameAndNothingIsMade(t *testing.T) {
	client, _ := serve(t)

	_, err := client.CreateSandbox(context.Background(), &sandboxv1.CreateSandboxRequest{
		Spec: &sandboxv1.SandboxSpec{Image: "box", Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK}},
	})

	if connect.CodeOf(err) != connect.CodeUnimplemented || !strings.Contains(err.Error(), "blocked egress") {
		t.Errorf("err = %v, want it refused as not served, naming blocked egress", err)
	}
	listed, err := client.ListSandboxes(context.Background(), &sandboxv1.ListSandboxesRequest{})
	if err != nil || len(listed.GetSandboxes()) != 0 {
		t.Errorf("after the refusal the server holds %v (%v), want nothing", listed.GetSandboxes(), err)
	}
}

func TestAStartupGateIsNotSilentlyDroppedByAnotherDriver(t *testing.T) {
	client, _ := serve(t)
	control := create(t, client, &sandboxv1.SandboxSpec{Image: "box"})
	_, err := client.CreateSandbox(t.Context(), &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{
		Image: "box", Containers: []*sandboxv1.Container{{Name: "app", Image: "box"}},
		BeforeContainers: &sandboxv1.StartupGate{Arguments: []string{"true"}, Timeout: durationpb.New(time.Second)},
	}})
	if err == nil || !strings.Contains(err.Error(), "startup gate") {
		t.Fatalf("a driver lacking startup gates accepted one: %v", err)
	}
	listed, err := client.ListSandboxes(t.Context(), &sandboxv1.ListSandboxesRequest{})
	if err != nil || len(listed.GetSandboxes()) != 1 || listed.Sandboxes[0].Id != control.Id {
		t.Fatalf("a refused gate created a sandbox: %v %v", listed, err)
	}
}

func TestASandboxsPortIsReachedBeneathItsPath(t *testing.T) {
	client, srv := serve(t)
	made := create(t, client, &sandboxv1.SandboxSpec{Image: "box", Ports: []int32{7600}})

	endpoint := made.GetEndpoints()[0]
	if want := srv.URL + "/sandboxes/" + made.GetId() + "/ports/7600"; endpoint.GetUrl() != want {
		t.Fatalf("the port's URL is %s, want %s", endpoint.GetUrl(), want)
	}
	// The memory driver's sandboxes answer a connection with their id and
	// no HTTP, which a proxy reports as a bad gateway, and says: the
	// connection reached the sandbox. What it says is the test's to read,
	// and is not printed among a gate's output as though something failed.
	var said bytes.Buffer
	prev := log.Writer()
	t.Cleanup(func() { log.SetOutput(prev) })
	log.SetOutput(&said)
	resp, err := srv.Client().Get(endpoint.GetUrl() + "/")
	log.SetOutput(prev)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("the port answered %s, want a bad gateway from a sandbox that speaks no HTTP", resp.Status)
	}
	if !strings.Contains(said.String(), "proxy error") || !strings.Contains(said.String(), made.GetId()) {
		t.Errorf("the proxy said %q, want the sandbox's id read where an HTTP answer was expected", said.String())
	}
	missing, err := srv.Client().Get(srv.URL + "/sandboxes/sbx_nothing/ports/7600/")
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("a port of no sandbox answered %s, want not found", missing.Status)
	}
}

func TestASandboxIsPausedResumedAndDeleted(t *testing.T) {
	client, _ := serve(t)
	ctx := context.Background()
	made := create(t, client, &sandboxv1.SandboxSpec{Image: "box"})

	paused, err := client.PauseSandbox(ctx, &sandboxv1.PauseSandboxRequest{SandboxId: made.GetId()})
	if err != nil || paused.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_PAUSED {
		t.Fatalf("paused = %v, %v; want paused", paused.GetSandbox().GetState(), err)
	}
	resumed, err := client.ResumeSandbox(ctx, &sandboxv1.ResumeSandboxRequest{SandboxId: made.GetId()})
	if err != nil || resumed.GetSandbox().GetState() != sandboxv1.Sandbox_STATE_RUNNING {
		t.Fatalf("resumed = %v, %v; want running", resumed.GetSandbox().GetState(), err)
	}
	if _, err := client.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: made.GetId()}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: made.GetId()}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("describing a deleted sandbox = %v, want not found", err)
	}
}

// What makes sandboxes may hold other things too: a container runtime
// holds every container on the machine. The API deletes only what it
// made.
func TestWhatTheServiceDidNotMakeIsNotDeleted(t *testing.T) {
	held := drivertest.NewMemory()
	ctx := context.Background()
	if _, err := held.Apply(ctx, driver.Sandbox{ID: "somebody-elses", Image: "an-image"}); err != nil {
		t.Fatal(err)
	}
	client, _ := serveOver(t, held)

	_, err := client.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: "somebody-elses"})

	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("err = %v, want not found: the service made no such sandbox", err)
	}
	if _, err := held.Observe(ctx, "somebody-elses"); err != nil {
		t.Errorf("what the service did not make is gone: %v", err)
	}
}
