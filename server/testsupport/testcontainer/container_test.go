package testcontainer_test

import (
	"context"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/source/told"
	sourcev1 "github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrun"

	"github.com/frontseat-dev/frontseat/local/ports"
	"github.com/frontseat-dev/frontseat/server/testsupport/testcontainer"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
)

// The port the daemon in a box serves the build API on.
const buildPort = ports.Daemon

func TestTheContainerDriverConforms(t *testing.T) {
	image := testbox.Image(t)
	drivertest.Conforms(t, testcontainer.Driver{}, drivertest.Labelled(driver.Sandbox{ID: testrun.ID(t, "frontseat-conformance"), Image: image, Ports: []int{buildPort}, Arguments: testgrid.InSandbox(t).Arguments()}))
}

// The runtime holds every container on the machine. The driver deletes
// only the ones it made.
func TestAContainerTheDriverDidNotMakeIsNotDeleted(t *testing.T) {
	testrequired.Stack(t)
	image := testbox.Image(t)
	name := testrun.ID(t, "frontseat-test-somebody-elses")
	if out, err := exec.Command("docker", "create", "--name", name, "--label", testrun.Label+"="+testrun.This(), image).CombinedOutput(); err != nil {
		t.Fatalf("docker create: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", name).Run() })

	if err := (testcontainer.Driver{}).Delete(context.Background(), name); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("docker", "inspect", "--format", "{{.Name}}", name).CombinedOutput(); err != nil {
		t.Errorf("a container the driver did not make is gone: %v: %s", err, out)
	}
}

func TestAClientBuildsInASandboxFromOutsideItAndFromInside(t *testing.T) {
	testrequired.Stack(t)
	image := testbox.Image(t)
	ctx := context.Background()
	d := testcontainer.Driver{}

	workspace := filepath.Join(t.TempDir(), "code")
	testforge.Hello(t, workspace)

	record := driver.Sandbox{
		ID:    testrun.ID(t, "frontseat-box-slice"),
		Image: image,
		Ports: []int{buildPort, ports.Source},
		// Told the name of its workspace, as the control plane tells a
		// workspace's daemon: the command line in the box knows by it
		// that it runs inside one.
		Arguments: append(testgrid.InSandbox(t).Arguments(), "--name=a-container-workspace"),
	}
	drivertest.Running(t, d, record)

	// From outside: the build API is a port of the sandbox, reached
	// through the driver.
	through := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.Dial(ctx, record.ID, buildPort)
		},
	}}
	runs := buildv1alpha1connect.NewBuildServiceClient(connect.NewClient(connecthttp.NewTransport(through, "http://sandbox")))
	answering(t, runs)

	// The daemon clones its source, as from any remote, when it is told
	// to on its source port, as the control plane tells it: the sandbox
	// is given no directory of this machine.
	telling := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.Dial(ctx, record.ID, ports.Source)
		},
	}}
	held := sourcev1alpha1connect.NewSourceServiceClient(connect.NewClient(connecthttp.NewTransport(telling, "http://sandbox")))
	if _, err := held.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: told.Spec(&workspacev1.WorkspaceSpec{Repositories: []*workspacev1.RepositorySource{{Remote: testforge.Serve(t, filepath.Dir(workspace)) + "/code"}}}, 1, told.Resolved{}, func(string) string { return "code" })}); err != nil {
		t.Fatal(err)
	}
	waited, err := held.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if populated := told.Status(waited.GetSource()); populated.GetState() != workspacev1.WorkspaceStatus_STATE_READY {
		t.Fatalf("the workspace could not be populated: %s", populated.GetFailure())
	}

	created, err := runs.CreateBuild(ctx, &buildv1.CreateBuildRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ended := wait(t, runs, created.GetBuild().GetId())
	if ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || ended.GetCounts().GetPassed() != 3 {
		actions, _ := runs.ListBuildActions(ctx, &buildv1.ListBuildActionsRequest{BuildId: ended.GetId()})
		t.Fatalf("from outside, the run ended %s with %d actions passed, want SUCCESS and 3: %v", ended.GetExitCode(), ended.GetCounts().GetPassed(), actions.GetActions())
	}

	// From inside: the client in the box, over loopback.
	inside, err := exec.Command("docker", "exec", record.ID, "frontseat", "build").CombinedOutput()
	if err != nil || !strings.Contains(string(inside), "SUCCESS") {
		t.Errorf("from inside, frontseat build: %v\n%s", err, inside)
	}

	listed, err := runs.ListBuilds(ctx, &buildv1.ListBuildsRequest{})
	if err != nil || len(listed.GetBuilds()) != 2 {
		t.Errorf("the daemon holds %d runs (%v), want the one from outside and the one from inside", len(listed.GetBuilds()), err)
	}
}

// answering waits for the daemon in a sandbox to answer.
func answering(t *testing.T, runs buildv1alpha1connect.BuildServiceClient) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := runs.ListBuilds(context.Background(), &buildv1.ListBuildsRequest{})
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon does not answer: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func wait(t *testing.T, runs buildv1alpha1connect.BuildServiceClient, id string) *buildv1.Build {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ended, err := runs.WaitBuild(ctx, &buildv1.WaitBuildRequest{BuildId: id})
	if err != nil {
		t.Fatal(err)
	}
	return ended.GetBuild()
}

// write writes a file, named with slashes beneath root.
func write(t *testing.T, root, name, content string) {
	t.Helper()
	testfiles.Write(t, filepath.Join(root, filepath.FromSlash(name)), content)
}
