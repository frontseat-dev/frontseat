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
	"github.com/frontseat-dev/frontseat/common/told"
	sourcev1 "github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1/sourcev1alpha1connect"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/testsupport/testrun"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testcontainer"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

func TestTheContainerDriverConforms(t *testing.T) {
	image := testbox.Image(t)
	drivertest.Conforms(t, testcontainer.Driver{}, drivertest.Labelled(driver.Sandbox{ID: testrun.ID(t, "frontseat-conformance"), Image: image, Ports: []int{ports.Source}, Arguments: append(testgrid.InSandbox(t).Arguments(), "--name=a-conformance-workspace")}))
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

// A workspace in a sandbox is filled from outside it and built from
// inside it: the box's supervisor is told what to hold on its source
// port, as the control plane tells it, and a command in the box builds
// what it holds, through the build daemon it starts there, told the grid
// the supervisor wrote. The build's run is the daemon's to list after.
func TestAWorkspaceInASandboxIsFilledFromOutsideAndBuiltFromInside(t *testing.T) {
	testrequired.Stack(t)
	image := testbox.Image(t)
	ctx := context.Background()
	d := testcontainer.Driver{}

	workspace := filepath.Join(t.TempDir(), "code")
	testforge.Hello(t, workspace)

	record := driver.Sandbox{
		ID:    testrun.ID(t, "frontseat-box-slice"),
		Image: image,
		Ports: []int{ports.Source},
		// Told the name of its workspace, as the control plane tells a
		// box's supervisor: the command line in the box knows by it that
		// it runs inside one.
		Arguments: append(testgrid.InSandbox(t).Arguments(), "--name=a-container-workspace"),
	}
	drivertest.Running(t, d, record)

	// The supervisor clones its source, as from any remote, when it is
	// told to on its source port, as the control plane tells it: the
	// sandbox is given no directory of this machine.
	telling := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.Dial(ctx, record.ID, ports.Source)
		},
	}}
	held := sourcev1alpha1connect.NewSourceServiceClient(connect.NewClient(connecthttp.NewTransport(telling, "http://sandbox")))
	answering(t, held)
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

	// From inside: the command line in the box, and the build daemon it
	// starts there, on the loopback.
	inside, err := exec.Command("docker", "exec", record.ID, "frontseat", "build").CombinedOutput()
	if err != nil || !strings.Contains(string(inside), "SUCCESS") {
		t.Fatalf("from inside, frontseat build: %v\n%s", err, inside)
	}
	listed, err := exec.Command("docker", "exec", record.ID, "frontseat", "build", "list").CombinedOutput()
	if err != nil || strings.Count(string(listed), "build_") != 1 {
		t.Errorf("the build daemon of the box lists %q (%v), want the one build made from inside", listed, err)
	}
}

// answering waits for the supervisor in a sandbox to answer on its
// source port.
func answering(t *testing.T, held sourcev1alpha1connect.SourceServiceClient) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := held.WaitSource(context.Background(), &sourcev1.WaitSourceRequest{})
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the supervisor does not answer: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// write writes a file, named with slashes beneath root.
func write(t *testing.T, root, name, content string) {
	t.Helper()
	testfiles.Write(t, filepath.Join(root, filepath.FromSlash(name)), content)
}
