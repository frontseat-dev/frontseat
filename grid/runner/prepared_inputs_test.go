package runner

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	runnerv1 "github.com/frontseat-dev/frontseat/common/runner/runnerapi/frontseat/runner/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/runner/testservice"
	"github.com/frontseat-dev/frontseat/sandbox"
	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1/sandboxv1alpha1connect"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testcontainer"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

func TestAPreparedProgramCannotSetItsConfinerEnvironment(t *testing.T) {
	r := &runner{image: busybox}
	control := apiDescription(t)
	if err := r.checkDescription(control); err != nil {
		t.Fatal(err)
	}
	control.Services[0].Service.Environment = map[string]string{"LD_PRELOAD": "/runtime/root/inject.so"}
	if err := r.checkDescription(control); err == nil {
		t.Fatal("prepared program environment accepted outside its confinement command")
	}
}

func TestPreparedMountPathsRefuseLinksBesidePlainControl(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.MkdirAll("plain/child", 0755); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("plain", "alias"); err != nil {
		t.Fatal(err)
	}
	if err := plainDirectory(root, "plain/child"); err != nil {
		t.Fatal(err)
	}
	for _, at := range []string{"alias", "alias/child", "../outside", "plain/../plain", "/plain"} {
		if err := plainDirectory(root, at); err == nil {
			t.Errorf("mount path %q accepted", at)
		}
	}
}

// openRoot opens a directory as a root the test closes when it ends.
func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

// preparedHome writes, beneath root, the runtime input of a program's
// preparation with a home that holds one file, config, and returns the
// input's directory.
func preparedHome(t *testing.T, root *os.Root, p *runnerv1.RunnerService) string {
	t.Helper()
	input := filepath.Join("action/root", testservice.RuntimeDirectory, p.Name)
	if err := root.MkdirAll(filepath.Join(input, ".frontseat/home"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(filepath.Join(input, ".frontseat/home/config"), []byte("prepared"), 0644); err != nil {
		t.Fatal(err)
	}
	return input
}

func TestPreparedHomeFilesBecomeReadOnlyFileBinds(t *testing.T) {
	base := t.TempDir()
	root := openRoot(t, base)
	p := apiDescription(t).Services[0]
	input := preparedHome(t, root, p)
	a := &attempt{r: &runner{host: base, bb: base}, mine: t.TempDir(), hostPath: "owned", paths: paths{root: "action/root"}}
	volumes, err := a.programVolumes(root, p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range volumes {
		if v.Name == "prepared-0" {
			found = v.ReadOnly && v.Path == "/runtime/root/.frontseat/home/config" && v.Subpath == filepath.ToSlash(filepath.Join(input, ".frontseat/home/config"))
		}
	}
	if !found {
		t.Fatalf("prepared home file was not retained read-only: %v", volumes)
	}
}

func TestPreparedHomeFileBindsWorkInTheRealRuntime(t *testing.T) {
	testrequired.Stack(t)
	base := t.TempDir()
	for _, dir := range []string{"build", "owned", "bb"} {
		if err := os.Mkdir(filepath.Join(base, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	root := openRoot(t, filepath.Join(base, "build"))
	p := apiDescription(t).Services[0]
	input := preparedHome(t, root, p)
	if err := root.MkdirAll(filepath.Join(input, ".frontseat/tmp"), 0755); err != nil {
		t.Fatal(err)
	}
	a := &attempt{r: &runner{host: base, bb: filepath.Join(base, "bb")}, mine: filepath.Join(base, "owned"), hostPath: "owned", paths: paths{root: "action/root"}}
	volumes, err := a.programVolumes(root, p)
	if err != nil {
		t.Fatal(err)
	}
	served, err := sandbox.New(sandbox.Options{Driver: testcontainer.Driver{}, PublicURL: "http://runtime-review", State: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(served.Handler())
	t.Cleanup(server.Close)
	client := sandboxv1alpha1connect.NewSandboxServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	made, err := client.CreateSandbox(ctx, &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{Image: testbox.Plain(t), Arguments: []string{"sleep", "600"}, Volumes: volumes, Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_BLOCK}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := client.DeleteSandbox(context.Background(), &sandboxv1.DeleteSandboxRequest{SandboxId: made.Sandbox.Id})
		if err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	for _, c := range []struct {
		args   []string
		code   int
		output string
	}{
		{[]string{"cat", "/runtime/root/.frontseat/home/config"}, 0, "prepared"},
		{[]string{"touch", "/runtime/root/.frontseat/home/fresh"}, 0, ""},
		{[]string{"sh", "-c", "printf changed > /runtime/root/.frontseat/home/config"}, 1, ""},
	} {
		answer, err := client.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{SandboxId: made.Sandbox.Id, Arguments: c.args})
		if err != nil || answer.ExitCode == 0 && c.code != 0 || answer.ExitCode != 0 && c.code == 0 || c.output != "" && string(answer.Output) != c.output {
			t.Fatalf("file-bind command %v: %v %v", c.args, answer, err)
		}
	}
}
