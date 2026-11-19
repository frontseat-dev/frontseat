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
		"service.ports: http 0":            func(l *typev1.Service) { l.Ports["http"] = 0 },
		"service.ports: http 65536":        func(l *typev1.Service) { l.Ports["http"] = 65536 },
		"service.ports: HTTP 80":           func(l *typev1.Service) { l.Ports["HTTP"] = 80 },
		"says nothing of when it is ready": func(l *typev1.Service) { l.Ready = nil },
		"is one of port, http or command":  func(l *typev1.Service) { l.Ready = []*typev1.ServiceReady{{}} },
		"a port it does not name":          func(l *typev1.Service) { l.Ready = []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Port{}}} },
		"names a port sql it does not serve": func(l *typev1.Service) {
			l.Ready = []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Port{Port: "sql"}}}
		},
		"at no port or path":    func(l *typev1.Service) { l.Ready[0].GetHttp().Path = "@evil.example/" },
		"no regular expression": func(l *typev1.Service) { l.Ready[0].GetHttp().Body = "(" },
		"a command that names nothing": func(l *typev1.Service) {
			l.Ready = []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Command{Command: &typev1.ServiceCommandReady{}}}}
		},
		"service.timeout is a number":     func(l *typev1.Service) { l.Timeout = durationpb.New(-1 * time.Second) },
		"more than the hour":              func(l *typev1.Service) { l.Timeout = durationpb.New(3601 * time.Second) },
		"not a name the shell can export": func(l *typev1.Service) { l.Exports = map[string]string{"LD-PRELOAD": "x"} },
		// What it starts with is named as what it exports is: each of
		// these would reach the runtime as --env <name>=<value>.
		`service.env: "" is not a name`:     func(l *typev1.Service) { l.Environment = map[string]string{"": "x"} },
		`service.env: "A=B" is not a name`:  func(l *typev1.Service) { l.Environment = map[string]string{"A=B": "x"} },
		`service.env: "A B" is not a name`:  func(l *typev1.Service) { l.Environment = map[string]string{"A B": "x"} },
		`service.env: "A\nB" is not a name`: func(l *typev1.Service) { l.Environment = map[string]string{"A\nB": "x"} },
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
