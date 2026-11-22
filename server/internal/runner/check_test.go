package runner

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	runnerv1 "github.com/frontseat-dev/frontseat/server/internal/runnerapi/frontseat/runner/v1alpha1"
)

const busybox = "docker.io/library/busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"

func service(change func(*runnerv1.RunnerService)) *runnerv1.RunnerService {
	s := &runnerv1.RunnerService{Name: "db", Entity: "resource:default/db", Service: &typev1.Service{
		Image: busybox, Timeout: durationpb.New(60 * time.Second), Ports: map[string]int32{"sql": 5432},
		Ready: []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Port{Port: "sql"}}},
	}}
	if change != nil {
		change(s)
	}
	return s
}

// What a runner sandbox file says is held to what the daemon allows,
// whatever wrote it: it is an input of the action, which any client of
// the grid may give.
func TestWhatTheActionsRootSaysToStartIsChecked(t *testing.T) {
	r := &runner{registries: map[string]string{"docker.io": "localhost:8081/dockerhub", "ghcr.io": "localhost:8081/ghcr"}}

	// The control: the daemon's own, named as the proxy serves it.
	good := service(nil)
	if err := r.checkDescription(&runnerv1.RunnerSandbox{Services: []*runnerv1.RunnerService{good}}); err != nil {
		t.Fatalf("a service the daemon would write: %v", err)
	}
	if want := "localhost:8081/dockerhub/library/busybox@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"; good.GetService().GetImage() != want {
		t.Errorf("its image as pulled: %s, want %s", good.GetService().GetImage(), want)
	}

	refused := map[string][]*runnerv1.RunnerService{
		"an image that is an option of docker's": {service(func(s *runnerv1.RunnerService) {
			s.Service.Image = "--label=a.b/x@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"
		})},
		"an image of a registry the proxy is not in front of": {service(func(s *runnerv1.RunnerService) {
			s.Service.Image = "evil.example/data@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"
		})},
		"an image named without its registry": {service(func(s *runnerv1.RunnerService) {
			s.Service.Image = "busybox@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"
		})},
		"an image named by a tag alone": {service(func(s *runnerv1.RunnerService) { s.Service.Image = "docker.io/library/busybox:1.37" })},
		"a service named main":          {service(func(s *runnerv1.RunnerService) { s.Name = "main" })},
		"a service named as a path":     {service(func(s *runnerv1.RunnerService) { s.Name = "../db" })},
		"two services of one name":      {service(nil), service(nil)},
		"no time to be ready":           {service(func(s *runnerv1.RunnerService) { s.Service.Timeout = durationpb.New(0 * time.Second) })},
		"more than an hour to be ready": {service(func(s *runnerv1.RunnerService) { s.Service.Timeout = durationpb.New(7200 * time.Second) })},
		"a port that is none":           {service(func(s *runnerv1.RunnerService) { s.Service.Ports["sql"] = 70000 })},
		"ready by a port it does not serve": {service(func(s *runnerv1.RunnerService) {
			s.Service.Ready[0].Check = &typev1.ServiceReady_Port{Port: "http"}
		})},
		"a body that is no regular expression": {service(func(s *runnerv1.RunnerService) {
			s.Service.Ready[0].Check = &typev1.ServiceReady_Http{Http: &typev1.ServiceHttpReady{Port: "sql", Path: "/", Body: "("}}
		})},
		"an HTTP check of no path": {service(func(s *runnerv1.RunnerService) {
			s.Service.Ready[0].Check = &typev1.ServiceReady_Http{Http: &typev1.ServiceHttpReady{Port: "sql", Path: "@evil"}}
		})},
		"a command that names nothing": {service(func(s *runnerv1.RunnerService) {
			s.Service.Ready[0].Check = &typev1.ServiceReady_Command{Command: &typev1.ServiceCommandReady{}}
		})},
		"a check of no kind":        {service(func(s *runnerv1.RunnerService) { s.Service.Ready[0].Check = nil })},
		"a service with no service": {service(func(s *runnerv1.RunnerService) { s.Service = nil })},
		"exports, which are none of a runner's": {service(func(s *runnerv1.RunnerService) {
			s.Service.Exports = map[string]string{"LD_PRELOAD": "/x.so"}
		})},
	}
	for what, services := range refused {
		if err := r.checkDescription(&runnerv1.RunnerSandbox{Services: services}); err == nil {
			t.Errorf("%s: allowed; want refused", what)
		} else if what == "an image that is an option of docker's" && !strings.Contains(err.Error(), "not an image's reference") {
			t.Errorf("%s: refused for %v; want it refused as no image", what, err)
		}
	}
}
