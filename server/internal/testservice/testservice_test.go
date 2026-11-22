package testservice_test

import (
	"google.golang.org/protobuf/types/known/durationpb"
	"strings"
	"testing"
	"time"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"

	"github.com/frontseat-dev/frontseat/server/internal/testservice"
)

const image = "docker.io/library/busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"

func whole() *typev1.Service {
	return &typev1.Service{
		Image:   image,
		Ports:   map[string]int32{"http": 8080},
		Ready:   []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Http{Http: &typev1.ServiceHttpReady{Port: "http", Path: "/healthz", Body: "ok|up"}}}},
		Exports: map[string]string{"URL": "http://{host}:{port.http}"},
	}
}

// What a service cannot be is refused by the key it is of, each beside
// the same service as it may be: this is what the daemon holds a catalog
// file and a plugin's answer to, and a runner what it is told to start.
func TestWhatAServiceCannotBeIsRefusedByItsKey(t *testing.T) {
	if err := testservice.Check(whole()); err != nil {
		t.Fatalf("a service that is whole was refused: %v", err)
	}
	for says, change := range map[string]func(*typev1.Service){
		"service.image":              func(l *typev1.Service) { l.Image = "--label=x@sha256:" + strings.Repeat("a", 64) },
		"does not name its registry": func(l *typev1.Service) { l.Image = strings.TrimPrefix(image, "docker.io/library/") },
		"service names no port": func(l *typev1.Service) {
			l.Ports, l.Ready = nil, []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Command{Command: &typev1.ServiceCommandReady{Arguments: []string{"true"}}}}}
		},
		"service.ports: a port is a number from 1 to 65535":                                                func(l *typev1.Service) { l.Ports["http"] = 0 },
		"a port is a number from 1 to 65535":                                                               func(l *typev1.Service) { l.Ports["http"] = 65536 },
		"service.ports: a port is named in lower-case letters, digits and dashes, beginning with a letter": func(l *typev1.Service) { l.Ports["HTTP"] = 80 },
		"says nothing of when it is ready":                                                                 func(l *typev1.Service) { l.Ready = nil },
		"is one of port, http or command":                                                                  func(l *typev1.Service) { l.Ready = []*typev1.ServiceReady{{}} },
		"a port it does not name":                                                                          func(l *typev1.Service) { l.Ready = []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Port{}}} },
		"service.ready[0] names a port it does not serve": func(l *typev1.Service) {
			l.Ready = []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Port{Port: "sql"}}}
		},
		"at no port or path":      func(l *typev1.Service) { l.Ready[0].GetHttp().Path = "@evil.example/" },
		"is a regular expression": func(l *typev1.Service) { l.Ready[0].GetHttp().Body = "(" },
		"a command that names nothing": func(l *typev1.Service) {
			l.Ready = []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Command{Command: &typev1.ServiceCommandReady{}}}}
		},
		"service.ready-timeout is a length": func(l *typev1.Service) { l.Timeout = durationpb.New(-1 * time.Second) },
		"more than the hour":                func(l *typev1.Service) { l.Timeout = durationpb.New(3601 * time.Second) },
		"service.exports: a name the shell can export is letters, digits and underscores": func(l *typev1.Service) { l.Exports = map[string]string{"LD-PRELOAD": "x"} },
		// What it starts with is named as what it exports is: each of
		// these would reach the runtime as --env <name>=<value>.
		"service.environment: a name the shell can export is letters":  func(l *typev1.Service) { l.Environment = map[string]string{"": "x"} },
		"service.environment: a name the shell can export is letters,": func(l *typev1.Service) { l.Environment = map[string]string{"A=B": "x"} },
		"service.environment: a name the shell can export is":          func(l *typev1.Service) { l.Environment = map[string]string{"A B": "x"} },
		"service.environment: a name the shell can export":             func(l *typev1.Service) { l.Environment = map[string]string{"A\nB": "x"} },
	} {
		l := whole()
		change(l)
		if err := testservice.Check(l); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("want a refusal saying %q, got %v", says, err)
		}
	}
	// Who it runs as and what it writes are refused by their key, and
	// never repeated: a value may hold anything.
	for _, refused := range []struct {
		value  string
		says   string
		change func(*typev1.Service)
	}{
		{"0", "service.user:", func(l *typev1.Service) { l.User = "0" }},
		{"postgres", "service.user:", func(l *typev1.Service) { l.User = "postgres" }},
		{"-1", "service.user:", func(l *typev1.Service) { l.User = "-1" }},
		{"/", "service.writable:", func(l *typev1.Service) { l.Writable = []string{"/"} }},
		{"var/lib", "service.writable:", func(l *typev1.Service) { l.Writable = []string{"var/lib"} }},
		{"/var/../etc", "service.writable:", func(l *typev1.Service) { l.Writable = []string{"/var/../etc"} }},
	} {
		l := whole()
		refused.change(l)
		err := testservice.Check(l)
		if err == nil || !strings.HasPrefix(err.Error(), refused.says) {
			t.Errorf("%q: want a refusal saying %q, got %v", refused.value, refused.says, err)
		} else if strings.Contains(err.Error(), `"`+refused.value+`"`) {
			t.Errorf("the refusal of %q repeats it: %v", refused.value, err)
		}
	}
	// A user by number, and the directories of its image it writes, are
	// allowed.
	runs := whole()
	runs.User, runs.Writable = "70", []string{"/var/lib/postgresql", "/var/run/postgresql"}
	if err := testservice.Check(runs); err != nil {
		t.Errorf("a service that runs as 70 and writes its data was refused: %v", err)
	}
	// An hour is the most, and is allowed.
	hour := whole()
	hour.Timeout = durationpb.New(3600 * time.Second)
	if err := testservice.Check(hour); err != nil {
		t.Errorf("an hour to be ready was refused: %v", err)
	}
}

// What is yet to be said is no mistake of shape: a service a plugin is
// still to complete is told to it, and refused only once whole.
func TestWhatIsNotYetSaidIsNoMistakeOfShape(t *testing.T) {
	if err := testservice.Shaped(&typev1.Service{}); err != nil {
		t.Fatalf("nothing said was refused as misshapen: %v", err)
	}
	if err := testservice.Check(&typev1.Service{}); err == nil {
		t.Fatal("nothing said was taken for a service")
	}
}

// What a service refuses it does not repeat, whoever wrote it: a name,
// a port, a body, an endpoint or an API's reference may hold anything.
// The control is the whole service, which is allowed.
func TestWhatAServiceRefusesIsNotRepeated(t *testing.T) {
	if err := testservice.Check(whole()); err != nil {
		t.Fatalf("control, a service that is whole: %v", err)
	}
	for name, change := range map[string]func(*typev1.Service){
		"an export":          func(l *typev1.Service) { l.Exports = map[string]string{"SECRET-A": "x"} },
		"a variable":         func(l *typev1.Service) { l.Environment = map[string]string{"SECRET-A": "x"} },
		"a port's name":      func(l *typev1.Service) { l.Ports["SECRET"] = 80 },
		"a port":             func(l *typev1.Service) { l.Ports["http"] = 5346738 },
		"a body":             func(l *typev1.Service) { l.Ready[0].GetHttp().Body = "SECRET(" },
		"an endpoint's name": func(l *typev1.Service) { l.Endpoints = map[string]string{"SECRET": "{host}:{port.http}"} },
		"an API":             func(l *typev1.Service) { l.Apis = map[string]*typev1.ServiceApi{"SECRET": {Port: "http"}} },
		"an API's export": func(l *typev1.Service) {
			l.Apis = map[string]*typev1.ServiceApi{"api:default/orders": {Port: "http", Exports: map[string]string{"SECRET-A": "x"}}}
		},
		"a readiness check's port": func(l *typev1.Service) {
			l.Ready = []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Port{Port: "SECRET"}}}
		},
		"an image":                 func(l *typev1.Service) { l.Image = "https://user:SECRET@registry.example/x" },
		"an image with no digest":  func(l *typev1.Service) { l.Image = "registry.example/SECRET" },
		"an image the build makes": func(l *typev1.Service) { l.Image = "//SECRET:image" },
		"a time to be ready":       func(l *typev1.Service) { l.Timeout = durationpb.New(5346738 * time.Second) },
	} {
		l := whole()
		change(l)
		if err := testservice.Check(l); err == nil || !strings.HasPrefix(err.Error(), "service.") || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "5346738") {
			t.Errorf("%s: want it refused by its key and not repeated, got %v", name, err)
		}
	}
}

// Of several things wrong under one key, the first by name is the one
// refused, every time.
func TestWhichEndpointIsRefusedDoesNotChangeBetweenChecks(t *testing.T) {
	for range 50 {
		l := whole()
		l.Endpoints = map[string]string{"zeta": "{host}", "alpha": "{port.http}", "mid": "{host}:{port.http}:{port.other}"}
		if err := testservice.Check(l); err == nil || !strings.Contains(err.Error(), "service.endpoints: alpha does not say {host}") {
			t.Fatalf("the endpoint refused: %v", err)
		}
	}
}
