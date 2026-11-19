package sandbox_test

import (
	"context"
	"errors"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
	"net/http"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/sandbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"google.golang.org/protobuf/types/known/durationpb"
)

func codeOf(err error) connect.Code {
	var e *connect.Error
	if errors.As(err, &e) {
		return e.Code()
	}
	return 0
}

// What a caller names of a sandbox's containers and of a volume's subpath
// is checked before anything reaches a driver.
func TestWhatASpecNamesOfContainersAndSubpathsIsChecked(t *testing.T) {
	client, _ := serve(t)
	refused := map[string]*sandboxv1.SandboxSpec{
		"a subpath leaving its volume":                    {Image: "box", Volumes: []*sandboxv1.Volume{{Name: "w", Path: "/w", Kind: sandboxv1.Volume_KIND_HOST, Source: "/tmp", Subpath: "../other"}}},
		"an absolute subpath":                             {Image: "box", Volumes: []*sandboxv1.Volume{{Name: "w", Path: "/w", Kind: sandboxv1.Volume_KIND_HOST, Source: "/tmp", Subpath: "/etc"}}},
		"a subpath of no plain form":                      {Image: "box", Volumes: []*sandboxv1.Volume{{Name: "w", Path: "/w", Kind: sandboxv1.Volume_KIND_HOST, Source: "/tmp", Subpath: "a/./b"}}},
		"a subpath of an empty volume":                    {Image: "box", Volumes: []*sandboxv1.Volume{{Name: "w", Path: "/w", Subpath: "a"}}},
		"a container named main":                          {Image: "box", Containers: []*sandboxv1.Container{{Name: "main", Image: "box"}}},
		"two containers of one name":                      {Image: "box", Containers: []*sandboxv1.Container{{Name: "db", Image: "box"}, {Name: "db", Image: "box"}}},
		"a container named as a path":                     {Image: "box", Containers: []*sandboxv1.Container{{Name: "../db", Image: "box"}}},
		"a container of no image":                         {Image: "box", Containers: []*sandboxv1.Container{{Name: "db"}}},
		"a container image that is an option of docker's": {Image: "box", Containers: []*sandboxv1.Container{{Name: "db", Image: "--privileged"}}},
		"a sandbox image that is an option of docker's":   {Image: "--volume=/:/host"},
		"an image of no reference's form":                 {Image: "box", Containers: []*sandboxv1.Container{{Name: "db", Image: "Box With Spaces"}}},
	}
	for what, spec := range refused {
		if _, err := client.CreateSandbox(context.Background(), &sandboxv1.CreateSandboxRequest{Spec: spec}); codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v, want it refused as an invalid argument", what, err)
		}
	}
	// The control: a plain subpath and a plainly named container are no
	// mistake; this driver cannot give them, and says so.
	_, err := client.CreateSandbox(context.Background(), &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{Image: "box", Containers: []*sandboxv1.Container{{Name: "db", Image: "box"}}}})
	if codeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "further containers") {
		t.Errorf("further containers of a driver that has none: %v, want a failed precondition naming them", err)
	}
}

func TestADriverThatCannotRunACommandInASandboxSaysSo(t *testing.T) {
	client, _ := serve(t)
	made := create(t, client, &sandboxv1.SandboxSpec{Image: "box"})
	_, err := client.ExecSandbox(context.Background(), &sandboxv1.ExecSandboxRequest{SandboxId: made.GetId(), Arguments: []string{"true"}})
	if codeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("exec where the driver cannot: %v, want a failed precondition", err)
	}
	if _, err := client.ExecSandbox(context.Background(), &sandboxv1.ExecSandboxRequest{SandboxId: made.GetId()}); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("exec of no command: %v, want an invalid argument", err)
	}
}

// Over the container driver: a command run in a sandbox's container, as
// a runner sandbox's service is asked whether it is ready.
func TestACommandIsRunInASandboxsContainer(t *testing.T) {
	testrequired.Stack(t)
	image := testbox.Plain(t)
	client, _ := serving(t, func(address string) (http.Handler, error) { return sandbox.Runner(address, nil, t.TempDir()) })
	ctx := context.Background()
	made, err := client.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{
		Image: image, Arguments: []string{"sleep", "600"}, Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK},
		Containers: []*sandboxv1.Container{{Name: "web", Image: image, Arguments: []string{"python3", "-m", "http.server", "8000", "--bind", "127.0.0.1"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := made.GetSandbox().GetId()
	t.Cleanup(func() { client.DeleteSandbox(context.Background(), &sandboxv1.DeleteSandboxRequest{SandboxId: id}) })
	deadline := time.Now().Add(time.Minute)
	for {
		got, err := client.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{SandboxId: id, Container: "web", Arguments: []string{"sh", "-c", "echo from web; exit 4"}})
		if err == nil {
			if got.GetExitCode() != 4 || !strings.Contains(string(got.GetOutput()), "from web") {
				t.Errorf("exec in the web container: exit %d, %q; want 4 and what it printed", got.GetExitCode(), got.GetOutput())
			}
			break
		}
		if codeOf(err) != connect.CodeFailedPrecondition || time.Now().After(deadline) {
			t.Fatalf("exec in the web container: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := client.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{SandboxId: id, Container: "db", Arguments: []string{"true"}}); codeOf(err) != connect.CodeNotFound {
		t.Errorf("exec in a container the sandbox was not made with: %v, want not found", err)
	}
	if _, err := client.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{SandboxId: "sbx_none", Arguments: []string{"true"}}); codeOf(err) != connect.CodeNotFound {
		t.Errorf("exec in no sandbox: %v, want not found", err)
	}
	got, err := client.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{SandboxId: id, Arguments: []string{"sleep", "5"}, Timeout: durationpb.New(500 * time.Millisecond)})
	if codeOf(err) != connect.CodeDeadlineExceeded {
		t.Errorf("a command past its timeout: %v %v, want deadline exceeded", got, err)
	}
}

// The container runtime keeps nothing outside a sandbox, so the sandbox
// API over it, the grid's runner's, takes no credential and makes no
// sandbox whose rule attaches one. The gateway's takes both
// (TestASandboxReachesWhatItsRulesSayAndNoMore).
func TestOverTheContainerRuntimeNoCredentialIsKept(t *testing.T) {
	testrequired.Stack(t)
	client, _ := serving(t, func(address string) (http.Handler, error) { return sandbox.Runner(address, nil, t.TempDir()) })
	ctx := context.Background()
	const says = "a credential kept outside its sandboxes"
	if _, err := client.SetCredential(ctx, &sandboxv1.SetCredentialRequest{Credential: &sandboxv1.Credential{Name: "a-credential", Host: "forge.example", Secret: "a-made-up-secret"}}); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), says) {
		t.Errorf("a credential given: %v, want it refused saying %q", err, says)
	}
	made, err := client.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{
		Image:  "docker.io/library/busybox:1.37",
		Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_LISTED, Rules: []*sandboxv1.EgressRule{{Host: "forge.example", Credential: "a-credential", Requests: []*sandboxv1.EgressRequest{{Method: "GET", Path: "/code/refs"}}}}},
	}})
	if err == nil {
		// Made after all: it is not left behind.
		_, _ = client.DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: made.GetSandbox().GetId()})
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a sandbox whose rule attaches a credential: %v, want it refused by name", err)
	}
}
