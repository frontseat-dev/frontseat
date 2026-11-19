package runner

import (
	runnerv1 "github.com/frontseat-dev/frontseat/server/internal/runnerapi/frontseat/runner/v1alpha1"
	"google.golang.org/protobuf/types/known/durationpb"
	"runtime"
	"testing"
	"time"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/confine"
	"github.com/frontseat-dev/frontseat/server/internal/testservice"
	"google.golang.org/protobuf/proto"
)

func apiDescription(t *testing.T) *runnerv1.RunnerSandbox {
	t.Helper()
	encoded, err := confine.Encode(confine.Spec{Home: ".frontseat/home", Temporary: ".frontseat/tmp", Program: true, Network: true})
	if err != nil {
		t.Fatal(err)
	}
	return &runnerv1.RunnerSandbox{
		Services: []*runnerv1.RunnerService{{Name: "app", Entity: "component:default/app",
			Service: &typev1.Service{Timeout: durationpb.New(60 * time.Second), Ports: map[string]int32{"http": 8080, "admin": 9000}, Ready: []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Port{Port: "http"}}}, Apis: map[string]*typev1.ServiceApi{
				"api:default/public": {Port: "http", Exports: map[string]string{"URL": "{host}:{port.http}"}},
				"api:default/admin":  {Port: "admin", Exports: map[string]string{"ADMIN": "{host}:{port.admin}"}},
			}},
			Program: &runnerv1.RunnerProgram{Root: testservice.RuntimeDirectory + "/app", Directory: ".", Arguments: confine.Arguments(encoded, ".", []string{"./binary"}), Image: busybox, Platform: map[string]string{"container-image": "docker://" + busybox, "OSFamily": "linux", "ISA": map[string]string{"arm64": "aarch64", "amd64": "x86-64"}[runtime.GOARCH]}},
		}},
		Routes: []*runnerv1.RunnerRoute{{Service: "app", Port: "http", LocalPort: 8080, Api: "api:default/public"}},
	}
}

func TestAnAPIRouteGrantsOnlyItsMappedEndpoint(t *testing.T) {
	r := &runner{image: busybox}
	control := apiDescription(t)
	if err := r.checkDescription(control); err != nil {
		t.Fatalf("valid API grant refused: %v", err)
	}
	for name, change := range map[string]func(*runnerv1.RunnerSandbox){
		"another API's endpoint": func(d *runnerv1.RunnerSandbox) { d.Routes[0].Port = "admin" },
		"no API identity":        func(d *runnerv1.RunnerSandbox) { d.Routes[0].Api = "" },
		"unknown API":            func(d *runnerv1.RunnerSandbox) { d.Routes[0].Api = "api:default/missing" },
		"unknown provider":       func(d *runnerv1.RunnerSandbox) { d.Routes[0].Service = "missing" },
		"an invalid local port":  func(d *runnerv1.RunnerSandbox) { d.Routes[0].LocalPort = 65536 },
		"a repeated grant": func(d *runnerv1.RunnerSandbox) {
			d.Routes = append(d.Routes, proto.Clone(d.Routes[0]).(*runnerv1.RunnerRoute))
		},
		"a provider depending on itself":        func(d *runnerv1.RunnerSandbox) { d.Services[0].Routes = d.Routes },
		"a Resource pretending to own an API":   func(d *runnerv1.RunnerSandbox) { d.Services[0].Entity = "resource:default/app" },
		"a program root outside its allocation": func(d *runnerv1.RunnerSandbox) { d.Services[0].Program.Root = "../app" },
		"a working directory outside its root":  func(d *runnerv1.RunnerSandbox) { d.Services[0].Program.Directory = ".." },
		"a confiner climbing above its root":    func(d *runnerv1.RunnerSandbox) { d.Services[0].Program.Arguments[2] = ".." },
		"a different preparation image":         func(d *runnerv1.RunnerSandbox) { d.Services[0].Program.Image = "another" },
		"an unserved platform property":         func(d *runnerv1.RunnerSandbox) { d.Services[0].Program.Platform["acceleration"] = "gpu" },
		"a network other than egress":           func(d *runnerv1.RunnerSandbox) { d.Services[0].Program.Platform["network"] = "anywhere" },
	} {
		t.Run(name, func(t *testing.T) {
			d := apiDescription(t)
			change(d)
			if err := r.checkDescription(d); err == nil {
				t.Fatal("malformed runtime grant was accepted")
			}
		})
	} // A program sent where the network is reached, as every program is
	// to be run, is a service all the same: its sandbox reaches nothing.
	reaching := apiDescription(t)
	reaching.Services[0].Program.Platform["network"] = "egress"
	if err := r.checkDescription(reaching); err != nil {
		t.Errorf("a program sent where the network is reached was refused: %v", err)
	}
}
