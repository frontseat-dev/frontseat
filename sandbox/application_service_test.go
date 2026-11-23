package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/client"
	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testowner"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
	"google.golang.org/protobuf/types/known/durationpb"
)

// This is trusted runner-harness plumbing, not a public artifact API or an
// implemented Component service provider. The compile goes through RunService
// and the real grid; only its exact executable is retrieved through REAPI and
// given to SandboxService as an immutable artifact (never a source checkout).
func TestABuiltApplicationServiceHasPrivateDependencies(t *testing.T) {
	testrequired.Stack(t)
	artifact, identity := buildApplication(t)
	p := newServiceRoutes(t)
	pg := newPostgres(p)
	a, b := pg.allocate(), pg.allocate()
	aa, ar := application(p, pg, a, artifact)
	_, br := application(p, pg, b, artifact)
	ca, cb := applicationClient(p, ar), applicationClient(p, br)
	expectHTTP(p, ca, "/build", identity)
	expectHTTP(p, cb, "/build", identity)
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for _, item := range []struct{ id, value string }{{ca, "101"}, {cb, "202"}} {
		workers.Go(func() {
			code, out, err := fetched(p, item.id, "/value?set="+item.value)
			if err != nil || code != 0 || strings.TrimSpace(out) != item.value {
				results <- fmt.Errorf("application write: exit %d: %v: %s", code, err, out)
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
	expectHTTP(p, ca, "/value", "101")
	expectHTTP(p, cb, "/value", "202")
	for _, id := range []string{ca, cb} {
		// A client gets no transitive database grant. The application itself
		// has already proved the upstream works by writing and reading SQL.
		if code, out := p.run(id, "dial", "unix", "/upstream/service.sock"); code == 0 || !strings.Contains(out, "no such file") {
			t.Fatalf("client reached a transitive dependency: exit %d: %s", code, out)
		}
		withoutDatabase(p, id)
	}
	p.proveBlocked(aa)
	// Stop the dependent before releasing its allocation. The other branch
	// shares the PostgreSQL process, but neither the app nor mutable state.
	ended(p, aa)
	pg.release(a)
	if code, _, err := fetched(p, ca, "/value"); err != nil || code == 0 {
		t.Fatalf("ended application's client: exit %d: %v", code, err)
	}
	expectHTTP(p, cb, "/value", "202")
	retry := pg.allocate()
	_, rr := application(p, pg, retry, artifact)
	expectHTTP(p, applicationClient(p, rr), "/value", "0")
	t.Log("grid-built application; two isolated app→database branches, no transitive client grant, reverse teardown and fresh retry passed")
}

// This fast lifecycle control uses the locally cross-compiled probe fixture;
// the test above independently requires a real Component build on the grid.
func TestAnApplicationServiceIsNotReadyWithAnInvalidDependency(t *testing.T) {
	testrequired.Stack(t)
	p := newServiceRoutes(t)
	pg := newPostgres(p)
	a := pg.allocate()
	_, route := application(p, pg, a, p.bin)
	client := applicationClient(p, route)
	expectHTTP(p, client, "/value", "0")
	withoutDatabase(p, client)
	control := p.spec([]string{"proxy", "/route/service.sock", "127.0.0.1:7000"}, route, true)
	control.Environment = map[string]string{"PGPASSWORD": a.password}
	withCredential := p.create(control)
	if code, out := p.run(withCredential, "without-db", "unused", "unused"); code == 0 || !strings.Contains(out, "unexpected database variable PGPASSWORD") {
		t.Fatalf("credential-detection control: exit %d: %s", code, out)
	}
	bad := a
	bad.password = "deliberately-wrong"
	badRoute := p.route()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	created, err := p.client.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{
		Spec: applicationSpec(p, pg, bad, p.bin, badRoute), RequestId: unique()})
	if err != nil {
		t.Fatal(err)
	}
	for {
		answer, err := p.client.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: created.GetSandbox().GetId()})
		if err != nil {
			t.Fatal(err)
		}
		if answer.GetSandbox().GetState() == sandboxv1.Sandbox_STATE_FAILED {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("application did not exit after its database refused authentication")
		case <-time.After(100 * time.Millisecond):
		}
	}
	logs, err := exec.CommandContext(ctx, "docker", "logs", created.GetSandbox().GetId()).CombinedOutput()
	if err != nil || !strings.Contains(string(logs), "password authentication failed") {
		t.Fatalf("application failed for another reason: %v: %s", err, logs)
	}
	if _, err := os.Stat(filepath.Join(badRoute, "service.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("application published a route before its database was ready: %v", err)
	}
	expectHTTP(p, client, "/value?set=303", "303")
}

func withoutDatabase(p *serviceRoutes, id string) {
	p.t.Helper()
	if code, out := p.run(id, "without-db", "unused", "unused"); code != 0 {
		p.t.Fatalf("client runtime received a database binding: exit %d: %s", code, out)
	}
}

func application(p *serviceRoutes, pg *postgres, allocation pgAllocation, artifact string) (string, string) {
	p.t.Helper()
	route := p.route()
	id := p.create(applicationSpec(p, pg, allocation, artifact, route))
	p.waitFor(id, []string{"dial", "unix", "/route/service.sock"})
	return id, route
}

func applicationSpec(p *serviceRoutes, pg *postgres, allocation pgAllocation, artifact, route string) *sandboxv1.SandboxSpec {
	spec := p.spec([]string{"application", "/upstream/service.sock", "/route/service.sock"}, route, false)
	spec.Arguments[2] = "/application/actor"
	spec.Volumes = append(spec.Volumes,
		&sandboxv1.Volume{Name: "application", Kind: sandboxv1.Volume_KIND_HOST, Source: artifact, Path: "/application", ReadOnly: true},
		&sandboxv1.Volume{Name: "upstream", Kind: sandboxv1.Volume_KIND_HOST, Source: pg.route, Path: "/upstream", ReadOnly: true})
	spec.Environment = allocation.environment()
	return spec
}

func (a pgAllocation) environment() map[string]string {
	return map[string]string{"PGHOST": "127.0.0.1", "PGPORT": "7000", "PGDATABASE": a.name,
		"PGUSER": a.name, "PGPASSWORD": a.password, "PGCONNECT_TIMEOUT": "3", "PGSSLMODE": "disable"}
}

func applicationClient(p *serviceRoutes, route string) string {
	p.t.Helper()
	id := p.make([]string{"proxy", "/route/service.sock", "127.0.0.1:7000"}, route, true)
	p.waitFor(id, []string{"dial", "tcp", "127.0.0.1:7000"})
	return id
}

func fetched(p *serviceRoutes, id, path string) (int32, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	answer, err := p.client.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{SandboxId: id,
		Arguments: []string{"gosu", "65534:65534", "wget", "-q", "-T", "5", "-O", "-", "http://127.0.0.1:7000" + path},
		Timeout:   durationpb.New(10 * time.Second)})
	if err != nil {
		return 0, "", err
	}
	return answer.GetExitCode(), string(answer.GetOutput()), nil
}

func expectHTTP(p *serviceRoutes, id, path, want string) {
	p.t.Helper()
	code, out, err := fetched(p, id, path)
	if err != nil || code != 0 || strings.TrimSpace(out) != want {
		p.t.Fatalf("HTTP %s: exit %d: %v: got %q, want %q", path, code, err, out, want)
	}
}

func ended(p *serviceRoutes, id string) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := p.client.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: id}); err != nil {
		p.t.Fatal(err)
	}
}

func buildApplication(t *testing.T) (string, string) {
	t.Helper()
	testgrid.Address(t)
	root := fixtureRoot(t)
	testpins.Go(t, root)
	// The probe imports the production relay. Give its isolated repository
	// that source too, under its real package path: a host build can see
	// the relay's own module, but the grid sees only this fixture, which
	// is a module one directory above where the relay's is.
	wrote(t, root, "app/go.mod", "module github.com/frontseat-dev/frontseat/common\n\ngo 1.27\n")
	source, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := os.ReadFile(filepath.Join(source, "common", "confine", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	wrote(t, root, "app/confine/routes.go", string(routes))
	wrote(t, root, "app/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: app\nspec:\n  type: service\n  lifecycle: experimental\n  owner: team\n")
	files, err := os.ReadDir("testdata/service-route")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join("testdata/service-route", file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		wrote(t, root, "app/"+file.Name(), string(contents))
	}
	// Change observable code, not a comment, so this compile must execute on
	// every proof run. Its artifact may still be reused by several instances.
	identity := unique()
	wrote(t, root, "app/stamp.go", "package main\nimport \"os\"\nfunc init() { os.Setenv(\"APPLICATION_BUILD\", \""+identity+"\") }\n")
	return buildArtifact(t, root, "//app:go.compile", "../../.frontseat/home/go/bin/common", "actor"), identity
}

// fixtureRoot is a new directory for a fixture repository, named "fixture":
// a workspace names a repository by the last element of its remote, which is
// the directory's name, and runArtifact asks for its targets by that name.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func wrote(t *testing.T, root, path, contents string) {
	t.Helper()
	path = filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runArtifact(t *testing.T, root, target string) (*buildv1.ActionResult, string) {
	t.Helper()
	address := artifactWorkspace(t, root)
	target = "//fixture/" + strings.TrimPrefix(target, "//")
	runs := buildv1alpha1connect.NewBuildServiceClient(connect.NewClient(connecthttp.NewTransport(testowner.Client(testowner.Token(t), testowner.Stack), address)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	created, err := runs.CreateBuild(ctx, &buildv1.CreateBuildRequest{Targets: []string{target}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetBuild().GetId()
	defer func() {
		ending, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if _, err := runs.CancelBuild(ending, &buildv1.CancelBuildRequest{BuildId: id}); err != nil {
			t.Error(err)
		}
	}()
	ended, err := runs.WaitBuild(ctx, &buildv1.WaitBuildRequest{BuildId: id})
	if err != nil {
		t.Fatal(err)
	}
	last := ended.GetBuild()
	actions, err := runs.ListBuildActions(ctx, &buildv1.ListBuildActionsRequest{BuildId: id})
	if err != nil {
		t.Fatal(err)
	}
	var action *buildv1.ActionResult
	for _, candidate := range actions.GetActions() {
		if candidate.GetLabel() == target {
			action = candidate
		}
	}
	if last == nil || last.GetState() != buildv1.Build_STATE_SUCCEEDED || last.GetFailure() != "" || last.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS {
		return nil, fmt.Sprintf("run=%v actions=%v", last, actions)
	}
	return action, fmt.Sprintf("run=%v actions=%v", last, actions)
}

// Build setup goes through the running control plane. The sandbox package
// does not import a build implementation or its test support (buildtest); an OpenShell
// workspace clones the fixture and serves its daemon through the public API.
func artifactWorkspace(t *testing.T, root string) string {
	t.Helper()
	testgrid.Address(t)
	plane := testowner.Stack
	image := testbox.Image(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	owner := testowner.Client(testowner.Token(t), plane)
	testforge.Committed(t, root)
	remote := testforge.Serve(t, filepath.Dir(root)) + "/" + filepath.Base(root)
	name := "service-route-" + unique()
	workspaces := workspacev1alpha1connect.NewWorkspaceServiceClient(connect.NewClient(connecthttp.NewTransport(owner, plane)))
	cleanup := true
	creationKnown := false
	// Register cleanup before creation: a failed call may have created its
	// workspace. Reconcile visible records, but do not treat NotFound after
	// a failed creation call as proof no late creation remains. The explicit
	// lifetime is the fallback for that uncertain case or a dead test process.
	t.Cleanup(func() {
		if !cleanup {
			return
		}
		ending, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		found, err := workspaces.DescribeWorkspace(ending, &workspacev1.DescribeWorkspaceRequest{Name: name})
		if connect.CodeOf(err) == connect.CodeNotFound {
			if !creationKnown {
				t.Errorf("artifact workspace %s creation outcome is uncertain; cleanup is unproven, with a 15-minute lifetime as fallback", name)
			}
			return
		}
		if err != nil {
			t.Error(err)
			return
		}
		spec := found.GetWorkspace().GetSpec()
		repositories := spec.GetRepositories()
		if spec.GetImage() != image || spec.GetLifetime().AsDuration() != 15*time.Minute || len(repositories) != 1 || repositories[0].GetRemote() != remote {
			t.Error("refusing to end an artifact workspace whose spec is not this probe's")
			return
		}
		if _, err := workspaces.DeleteWorkspace(ending, &workspacev1.DeleteWorkspaceRequest{Name: name}); err != nil && connect.CodeOf(err) != connect.CodeNotFound {
			t.Error(err)
			return
		}
		for {
			now, err := workspaces.DescribeWorkspace(ending, &workspacev1.DescribeWorkspaceRequest{Name: name})
			if connect.CodeOf(err) == connect.CodeNotFound {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			select {
			case <-ending.Done():
				t.Errorf("artifact workspace did not finish ending: %v; it was last %s: %q", ending.Err(), now.GetWorkspace().GetStatus().GetState(), now.GetWorkspace().GetStatus().GetFailure())
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	})
	created, err := workspaces.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: name, Spec: &workspacev1.WorkspaceSpec{
		Image: image, Lifetime: durationpb.New(15 * time.Minute),
		Repositories: []*workspacev1.RepositorySource{{Remote: remote}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	creationKnown = true
	if !created.GetCreated() {
		cleanup = false
		t.Fatal("artifact workspace already existed")
	}
	waited, err := workspaces.WaitWorkspace(ctx, &workspacev1.WaitWorkspaceRequest{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	ready := waited.GetWorkspace()
	if ready.GetStatus().GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		t.Fatalf("artifact workspace did not become ready: %v", ready)
	}
	t.Logf("artifact workspace %s uses OpenShell with image %s", name, image)
	return ready.GetStatus().GetBuildUri()
}

func buildArtifact(t *testing.T, root, target, output, name string) string {
	t.Helper()
	action, summary := runArtifact(t, root, target)
	if action == nil || action.GetOutcome() != buildv1.Outcome_OUTCOME_PASSED {
		t.Fatalf("artifact build failed: %s", summary)
	}
	if action.GetProvenance() != buildv1.Provenance_PROVENANCE_EXECUTED {
		t.Fatalf("artifact proof did not execute: %s", action.GetProvenance())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	grid, err := client.NewClient(ctx, "", client.DialParams{Service: testgrid.Address(t), NoSecurity: true})
	if err != nil {
		t.Fatal(err)
	}
	defer grid.Close()
	d := action.GetActionDigest()
	if d == nil {
		t.Fatal("artifact action has no digest")
	}
	result, err := grid.CheckActionCache(ctx, &repb.Digest{Hash: d.GetHash(), SizeBytes: d.GetSizeBytes()})
	if err != nil || result == nil {
		t.Fatalf("artifact result: %v", err)
	}
	outputs, err := grid.FlattenActionOutputs(ctx, result)
	if err != nil {
		t.Fatal(err)
	}
	file := outputs[output]
	if file == nil || !file.IsExecutable || file.SymlinkTarget != "" || file.IsEmptyDirectory {
		t.Fatalf("expected plain executable %s in grid outputs: %v", output, outputs)
	}
	if err := file.Digest.Validate(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// A fixed destination, never an output-provided path. The pinned REAPI
	// SDK verifies downloaded bytes against the expected digest.
	if _, err := grid.ReadBlobToFile(ctx, file.Digest, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, name), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s executed on grid; executable %s digest %s", target, output, file.Digest)
	return dir
}
