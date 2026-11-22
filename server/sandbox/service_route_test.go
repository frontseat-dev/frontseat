package sandbox_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/frontseat-dev/frontseat/server/sandbox"
	sandboxv1 "github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testcontainer"
	"github.com/frontseat-dev/frontseat/server/testsupport/testlisten"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
	"google.golang.org/protobuf/types/known/durationpb"
)

// This is a transport proof through the actual sandbox API, not a served build
// feature. Its grant is a read-only directory containing one Unix socket. The
// service remains separate from each consumer's process and network namespace.
// SQL allocation, framework adapters and a production forwarding API are not
// established by this test.
func TestSeparateBlockedConsumersReachOneServiceByItsRoute(t *testing.T) {
	testrequired.Stack(t)
	p := newServiceRoutes(t)
	route := p.route()
	identity := unique()
	service := p.make([]string{"echo", "/route/service.sock", identity}, route, false)
	p.waitFor(service, []string{"check", "/route/service.sock", identity})

	consumers := make([]string, 2)
	for n := range consumers {
		consumer := p.make([]string{"proxy", "/route/service.sock", "127.0.0.1:7000"}, route, true)
		consumers[n] = consumer
		p.waitFor(consumer, []string{"dial", "tcp", "127.0.0.1:7000"})
	}
	// Both processes bind the same loopback port, in distinct sandboxes. The
	// route, rather than a common network namespace, grants service access.
	var workers sync.WaitGroup
	results := make(chan error, len(consumers))
	for _, consumer := range consumers {
		workers.Go(func() {
			code, output, err := p.exec(context.Background(), consumer, "check", "127.0.0.1:7000", identity)
			if err != nil || code != 0 {
				results <- fmt.Errorf("roundtrip: exit %d: %v: %s", code, err, output)
			}
		})
	}
	workers.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	// Retain the red control beside the now-working route.
	noGrant := p.make([]string{"proxy", "/route/service.sock", "127.0.0.1:7000"}, "", true)
	p.waitFor(noGrant, []string{"dial", "tcp", "127.0.0.1:7000"})
	if code, output := p.run(noGrant, "check", "127.0.0.1:7000", identity); code == 0 || !strings.Contains(output, "service greeting: EOF") {
		t.Fatalf("consumer without a grant: exit %d: %s", code, output)
	}

	// A read-only route cannot be replaced with another service or used as a
	// writable directory. The service's writable grant is the allowed control.
	if code, out := p.run(service, "write", "/route/control", "owned"); code != 0 {
		t.Fatalf("writable control: %s", out)
	}
	for _, consumer := range consumers {
		if code, out := p.run(consumer, "write", "/route/other", "changed"); code == 0 || !strings.Contains(out, "read-only file system") {
			t.Fatalf("consumer writing the route: exit %d: %s", code, out)
		}
	}
	p.proveBlocked(consumers[0])
	// Ending one consumer cannot end the service or the other consumer.
	if _, err := p.client.DeleteSandbox(context.Background(), &sandboxv1.DeleteSandboxRequest{SandboxId: consumers[0]}); err != nil {
		t.Fatal(err)
	}
	if code, out := p.run(consumers[1], "check", "127.0.0.1:7000", identity); code != 0 {
		t.Fatalf("unaffected consumer after the negative controls: %s", out)
	}
	t.Log("one service, two separate non-root blocked consumers; binary roundtrips, absent-grant, read-only and host-egress controls passed")
}

func TestAServiceRouteCarriesARequestSentAfterTheServerHalfClosed(t *testing.T) {
	testrequired.Stack(t)
	p := newServiceRoutes(t)
	for _, throughProxy := range []bool{false, true} {
		route := p.route()
		identity := unique()
		service := p.make([]string{"halfclose", "/route/service.sock", identity}, route, false)
		p.waitFor(service, []string{"dial", "unix", "/route/service.sock"})
		consumer, address := service, "/route/service.sock"
		if throughProxy {
			consumer = p.make([]string{"proxy", "/route/service.sock", "127.0.0.1:7000"}, route, true)
			p.waitFor(consumer, []string{"dial", "tcp", "127.0.0.1:7000"})
		}
		if throughProxy {
			address = "127.0.0.1:7000"
		}
		if code, out := p.run(consumer, "after-eof", address, identity); code != 0 {
			t.Fatalf("writing after server-first EOF (proxy=%v): exit %d: %s", throughProxy, code, out)
		}
		p.waitFor(service, []string{"receipt", "unused", "unused"})
		t.Logf("server-first half-close control passed, proxy=%v", throughProxy)
	}
}

func TestASandboxWhoseCreationReplyWasLostIsCleanedUp(t *testing.T) {
	p := newServiceRoutes(t)
	p.dropCreate.Store(true)
	_, err := p.client.CreateSandbox(context.Background(), &sandboxv1.CreateSandboxRequest{
		Spec: p.spec([]string{"proxy", "/absent", "127.0.0.1:7000"}, "", true), RequestId: unique(),
	})
	if err == nil {
		t.Fatal("the transport did not lose the creation reply")
	}
	if len(p.owned()) != 1 {
		t.Fatal("the failed call did not create a real sandbox; the control proves nothing")
	}
	if err := p.cleanup(); err != nil {
		t.Fatal(err)
	}
	if len(p.owned()) != 0 {
		t.Fatal("cleanup left the sandbox whose creation reply was lost")
	}
}

// The image and proxy are the already-used PostgreSQL plugin's pinned image.
// Here it supplies gosu; no database or credentials are created by the route
// proof. Both the fixture service and its consumers run as nobody.
const serviceImage = "docker.io/library/postgres@sha256:3540c0362c809df084ffac0ae69fe794515f9de92692f02178bcdce0ef0302ac"

type serviceRoutes struct {
	t          *testing.T
	client     sandboxv1alpha1connect.SandboxServiceClient
	bin        string
	root       string
	owner      string
	dropCreate atomic.Bool
	createMu   sync.Mutex
	closing    bool
	creating   sync.WaitGroup
	cleanupMu  sync.Mutex
}

func newServiceRoutes(t *testing.T) *serviceRoutes {
	t.Helper()
	testrequired.Stack(t)
	testbox.Runtime(t)
	arch, err := exec.Command("docker", "info", "--format", "{{.Architecture}}").Output()
	if err != nil {
		t.Fatalf("the container runtime answers and does not say its architecture: %v", err)
	}
	goarch := strings.TrimSpace(string(arch))
	switch goarch {
	case "aarch64", "arm64":
		goarch = "arm64"
	case "x86_64", "amd64":
		goarch = "amd64"
	default:
		t.Fatalf("unproven probe architecture %q", goarch)
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(bin, "actor"), "./testdata/service-route")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the probe artifact: %v: %s", err, out)
	}
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	t.Cleanup(srv.Close)
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	served, err := sandbox.New(sandbox.Options{Driver: testcontainer.Driver{}, PublicURL: srv.URL, State: state})
	if err != nil {
		t.Fatal(err)
	}
	handler := served.Handler()
	p := &serviceRoutes{t: t, bin: bin, root: root, owner: unique()}
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		creating := strings.HasSuffix(r.URL.Path, "/CreateSandbox")
		if creating {
			p.createMu.Lock()
			if p.closing {
				p.createMu.Unlock()
				http.Error(w, "probe is closing", http.StatusServiceUnavailable)
				return
			}
			p.creating.Add(1)
			p.createMu.Unlock()
			defer p.creating.Done()
		}
		if creating && p.dropCreate.Swap(false) {
			recorded := httptest.NewRecorder()
			handler.ServeHTTP(recorded, r)
			if recorded.Code == http.StatusOK {
				http.Error(w, "creation response lost by probe", http.StatusServiceUnavailable)
				return
			}
			for key, values := range recorded.Header() {
				w.Header()[key] = values
			}
			w.WriteHeader(recorded.Code)
			w.Write(recorded.Body.Bytes())
			return
		}
		handler.ServeHTTP(w, r)
	})
	client := sandboxv1alpha1connect.NewSandboxServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	p.client = client
	t.Cleanup(func() {
		if err := p.cleanup(); err != nil {
			t.Error(err)
		}
	})
	return p
}

func (p *serviceRoutes) route() string {
	p.t.Helper()
	dir, err := os.MkdirTemp(p.root, "route-")
	if err != nil {
		p.t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		p.t.Fatal(err)
	}
	return dir
}

func (p *serviceRoutes) make(args []string, route string, readonly bool) string {
	p.t.Helper()
	return p.create(p.spec(args, route, readonly))
}

func (p *serviceRoutes) spec(args []string, route string, readonly bool) *sandboxv1.SandboxSpec {
	spec := &sandboxv1.SandboxSpec{
		Image:     serviceImage,
		Arguments: append([]string{"gosu", "65534:65534", "/probe/actor"}, args...),
		Egress:    &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK},
		Volumes:   []*sandboxv1.Volume{{Name: "artifact", Kind: sandboxv1.Volume_KIND_HOST, Source: p.bin, Path: "/probe", ReadOnly: true}},
		Labels:    map[string]string{"dev.frontseat.test-owner": p.owner},
	}
	if route != "" {
		spec.Volumes = append(spec.Volumes, &sandboxv1.Volume{Name: "route", Kind: sandboxv1.Volume_KIND_HOST, Source: route, Path: "/route", ReadOnly: readonly})
	}
	return spec
}

func (p *serviceRoutes) create(spec *sandboxv1.SandboxSpec) string {
	p.t.Helper()
	spec.Labels = map[string]string{"dev.frontseat.test-owner": p.owner}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	created, err := p.client.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: spec, RequestId: unique()})
	if err != nil {
		p.t.Fatal(err)
	}
	id := created.GetSandbox().GetId()
	deadline := time.Now().Add(30 * time.Second)
	for {
		now, err := p.client.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: id})
		if err != nil {
			p.t.Fatal(err)
		}
		state := now.GetSandbox().GetState()
		if state == sandboxv1.Sandbox_STATE_RUNNING {
			break
		}
		if state == sandboxv1.Sandbox_STATE_FAILED || time.Now().After(deadline) {
			logs, _ := exec.CommandContext(ctx, "docker", "logs", id).CombinedOutput()
			p.t.Fatalf("probe sandbox state %s: %s", state, logs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return id
}

func (p *serviceRoutes) owned() []*sandboxv1.Sandbox {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	answer, err := p.client.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{LabelSelector: "dev.frontseat.test-owner=" + p.owner})
	if err != nil {
		p.t.Fatal(err)
	}
	return answer.GetSandboxes()
}

func (p *serviceRoutes) cleanup() error {
	// This API server and its records belong only to this probe. Close
	// creation admission and join accepted requests before finding everything
	// it made, including a sandbox whose response was lost. The marker limits
	// cleanup within this private server; it is not a production trust identity.
	p.cleanupMu.Lock()
	defer p.cleanupMu.Unlock()
	p.createMu.Lock()
	p.closing = true
	p.createMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	joined := make(chan struct{})
	go func() { p.creating.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-ctx.Done():
		return fmt.Errorf("probe creations did not finish: %w", ctx.Err())
	}
	answer, err := p.client.ListSandboxes(ctx, &sandboxv1.ListSandboxesRequest{LabelSelector: "dev.frontseat.test-owner=" + p.owner})
	if err != nil {
		return err
	}
	var failures []error
	for _, s := range answer.GetSandboxes() {
		if _, err := p.client.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: s.GetId()}); err != nil && connect.CodeOf(err) != connect.CodeNotFound {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (p *serviceRoutes) run(id string, args ...string) (int32, string) {
	p.t.Helper()
	code, output, err := p.exec(context.Background(), id, args...)
	if err != nil {
		p.t.Fatal(err)
	}
	return code, output
}

func (p *serviceRoutes) exec(parent context.Context, id string, args ...string) (int32, string, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	response, err := p.client.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{
		SandboxId: id,
		Arguments: append([]string{"gosu", "65534:65534", "/probe/actor"}, args...),
		Timeout:   durationpb.New(15 * time.Second),
	})
	if err != nil {
		return 0, "", err
	}
	return response.GetExitCode(), string(response.GetOutput()), nil
}

func (p *serviceRoutes) proveBlocked(blocked string) {
	p.t.Helper()
	canary, err := testlisten.Own("tcp4")
	if err != nil {
		p.t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			conn, err := canary.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	defer func() { canary.Close(); <-finished }()
	control := p.create(&sandboxv1.SandboxSpec{
		Image:     serviceImage,
		Arguments: []string{"sleep", "600"},
		Egress:    &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_ALL},
		Volumes:   []*sandboxv1.Volume{{Name: "artifact", Kind: sandboxv1.Volume_KIND_HOST, Source: p.bin, Path: "/probe", ReadOnly: true}},
	})
	code, resolved := p.run(control, "resolve", "host.docker.internal", "unused")
	if code != 0 {
		p.t.Fatalf("resolving the controlled listener: %s", resolved)
	}
	address := net.JoinHostPort(strings.TrimSpace(resolved), fmt.Sprint(canary.Addr().(*net.TCPAddr).Port))
	if code, out := p.run(control, "dial", "tcp", address); code != 0 {
		p.t.Fatalf("the allowed control cannot reach the listener: %s", out)
	}
	if code, out := p.run(blocked, "dial", "tcp", address); code == 0 || !strings.Contains(out, "network is unreachable") {
		p.t.Fatalf("the blocked consumer reaching the same live listener: exit %d: %s", code, out)
	}
}

func (p *serviceRoutes) waitFor(id string, args []string) {
	p.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		code, output, err := p.exec(context.Background(), id, args...)
		if err == nil && code == 0 {
			return
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("probe readiness: %v: %s", err, output)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func unique() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}
