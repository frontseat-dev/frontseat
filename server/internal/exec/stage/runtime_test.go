package stage

import (
	runnerv1 "github.com/frontseat-dev/frontseat/server/internal/runnerapi/frontseat/runner/v1alpha1"
	"strings"
	"testing"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/confine"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

func TestAServiceProgramCanRunAtItsRepositoryRoot(t *testing.T) {
	encoded, err := confine.Encode(confine.Spec{Home: ".frontseat/home", Temporary: ".frontseat/tmp", Program: true, Network: true})
	if err != nil {
		t.Fatal(err)
	}
	d := &runnerv1.RunnerSandbox{Services: []*runnerv1.RunnerService{{Name: "service-0", Service: &typev1.Service{}}}}
	program := exec.Action{Root: t.TempDir(), Platform: map[string]string{"container-image": "docker://docker.io/library/busybox@sha256:" + strings.Repeat("a", 64)}, Arguments: confine.Arguments(encoded, ".", []string{"probe"})}
	_, err = Runtimes(exec.Action{Written: map[string]exec.Written{}}, d, map[string]exec.Action{"service-0": program})
	if err != nil {
		t.Fatal(err)
	}
	if d.Services[0].Program.Directory != "." {
		t.Fatalf("repository root was not a valid runtime directory: %q", d.Services[0].Program.Directory)
	}
}
