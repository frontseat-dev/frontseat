package pluginhost_test

import (
	"google.golang.org/protobuf/types/known/durationpb"
	"testing"
	"time"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/conformance"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
)

// What the plugins that start are asked, and the whole of what each
// must answer, written here apart from the plugins.

const greeterImage = "docker.io/library/busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"

func greeter() *typev1.Service {
	return &typev1.Service{
		Image:     greeterImage,
		Arguments: []string{"nc", "-lk", "-p", "7000", "-e", "echo", "hello"},
		Ports:     map[string]int32{"greet": 7000},
		Ready:     []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Port{Port: "greet"}}},
		Exports:   map[string]string{"GREETER_ADDR": "{host}:{port.greet}"},
	}
}

func answered(service *typev1.Service) *pluginv1.ServiceResponse {
	return &pluginv1.ServiceResponse{Service: service}
}

func containerFixture(t testing.TB) conformance.Fixture {
	return conformance.Fixture{
		Name: "container",
		ABI:  26, Description: &pluginv1.DescribeResponse{Name: "container", Services: []string{"container"}},
		Services: []conformance.ServiceCase{
			// It adds nothing: what is written is what is started, and
			// nothing written is nothing started, which the host refuses.
			{Request: &pluginv1.ServiceRequest{Name: "container"}, Want: answered(&typev1.Service{})},
			{Request: &pluginv1.ServiceRequest{Name: "container", Written: greeter()}, Want: answered(greeter())},
			{Request: &pluginv1.ServiceRequest{Name: "postgres"}, Refused: "UNCLAIMED_SERVICE"},
		},
	}
}

const postgresImage = "docker.io/library/postgres:17-alpine@sha256:3540c0362c809df084ffac0ae69fe794515f9de92692f02178bcdce0ef0302ac"

func pgReady(user, database string) []*typev1.ServiceReady {
	return []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Command{Command: &typev1.ServiceCommandReady{
		Arguments: []string{"pg_isready", "-h", "{host}", "-p", "{port.sql}", "-U", user, "-d", database},
	}}}}
}

// pgWritable is what the server writes on every official image: its
// data and its socket's directory.
var pgWritable = []string{"/var/lib/postgresql", "/var/run/postgresql"}

func postgresFixture(t testing.TB) conformance.Fixture {
	written := &typev1.Service{
		Image:       "docker.io/library/postgres:16-alpine@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Ports:       map[string]int32{"sql": 6543},
		Environment: map[string]string{"POSTGRES_USER": "app", "POSTGRES_PASSWORD": "secret", "POSTGRES_DB": "orders"},
		Exports:     map[string]string{"DATABASE": "{host}:{port.sql}"},
	}
	asked := func(written *typev1.Service) *pluginv1.ServiceRequest {
		return &pluginv1.ServiceRequest{Name: "postgres", Written: written}
	}
	// defaults is what a server told nothing is started as, with the
	// arguments it was given.
	defaults := func(arguments []string) *typev1.Service {
		return &typev1.Service{
			Image:       postgresImage,
			Arguments:   arguments,
			Ports:       map[string]int32{"sql": 5432},
			Environment: map[string]string{"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "postgres", "POSTGRES_DB": "postgres"},
			Ready:       pgReady("postgres", "postgres"),
			Exports:     map[string]string{"PGHOST": "{host}", "PGPORT": "{port.sql}", "PGUSER": "postgres", "PGPASSWORD": "postgres", "PGDATABASE": "postgres"},
			Endpoints:   map[string]string{"sql": "{host}:{port.sql}"},
			User:        "70",
			Writable:    pgWritable,
		}
	}
	return conformance.Fixture{
		Name: "postgres",
		ABI:  26, Description: &pluginv1.DescribeResponse{Name: "postgres", Services: []string{"postgres"}},
		Services: []conformance.ServiceCase{
			{Request: asked(nil), Want: answered(defaults(nil))},
			// What is written is kept, and the rest fits it: the server is
			// told the port, and asked on it for the database written.
			{Request: asked(written), Want: answered(&typev1.Service{
				Image:       written.Image,
				Arguments:   []string{"-p", "6543"},
				Ports:       written.Ports,
				Environment: written.Environment,
				Ready:       pgReady("app", "orders"),
				Exports:     written.Exports,
				Endpoints:   map[string]string{"sql": "{host}:{port.sql}"},
				// An image written is told its user by what is written.
				Writable: pgWritable,
			})},
			// An endpoint written is kept as it is written.
			{Request: asked(&typev1.Service{Endpoints: map[string]string{"sql": "postgres://{host}:{port.sql}/postgres"}}), Want: answered(func() *typev1.Service {
				d := defaults(nil)
				d.Endpoints = map[string]string{"sql": "postgres://{host}:{port.sql}/postgres"}
				return d
			}())},
			// Arguments that say no port are kept, on the port the server
			// serves on where it is told none.
			{Request: asked(&typev1.Service{Arguments: []string{"-c", "fsync=off"}}), Want: answered(defaults([]string{"-c", "fsync=off"}))},
			// A server that asks no password is told to trust, and a test
			// is then told none.
			{Request: asked(&typev1.Service{Environment: map[string]string{"POSTGRES_HOST_AUTH_METHOD": "trust"}}), Want: answered(&typev1.Service{
				Image:       postgresImage,
				Ports:       map[string]int32{"sql": 5432},
				Environment: map[string]string{"POSTGRES_HOST_AUTH_METHOD": "trust"},
				Ready:       pgReady("postgres", "postgres"),
				Exports:     map[string]string{"PGHOST": "{host}", "PGPORT": "{port.sql}", "PGUSER": "postgres", "PGDATABASE": "postgres"},
				Endpoints:   map[string]string{"sql": "{host}:{port.sql}"},
				User:        "70",
				Writable:    pgWritable,
			})},
			// What cannot be made to fit is a server that never becomes
			// ready, and is refused by name instead.
			{Request: asked(&typev1.Service{Ports: map[string]int32{"http": 80}}), Refused: "UNNAMED_PORT"},
			{Request: asked(&typev1.Service{Environment: map[string]string{"TZ": "UTC"}}), Refused: "NO_PASSWORD"},
			{Request: asked(&typev1.Service{Environment: map[string]string{"POSTGRES_PASSWORD": ""}}), Refused: "NO_PASSWORD"},
			{Request: asked(&typev1.Service{Environment: map[string]string{"POSTGRES_HOST_AUTH_METHOD": "scram-sha-256"}}), Refused: "NO_PASSWORD"},
			{Request: asked(&typev1.Service{Environment: map[string]string{"POSTGRES_PASSWORD": "x", "PGPORT": "6543"}}), Refused: "PORT_IN_ENVIRONMENT"},
			{Request: asked(&typev1.Service{Ports: map[string]int32{"sql": 6543}, Arguments: []string{"-c", "fsync=off"}}), Refused: "PORT_BESIDE_ARGUMENTS"},
			{Request: asked(&typev1.Service{Arguments: []string{"-p", "6543"}}), Refused: "PORT_IN_ARGUMENTS"},
			{Request: asked(&typev1.Service{Arguments: []string{"--port=6543"}}), Refused: "PORT_IN_ARGUMENTS"},
			{Request: asked(&typev1.Service{Arguments: []string{"-c", "port=6543"}}), Refused: "PORT_IN_ARGUMENTS"},
			{Request: asked(&typev1.Service{Ports: map[string]int32{"sql": 6543}, Arguments: []string{"-p", "{port.sql}"}}), Refused: "PORT_IN_ARGUMENTS"},
			{Request: &pluginv1.ServiceRequest{Name: "container"}, Refused: "UNCLAIMED_SERVICE"},
		},
	}
}

// A plugin's answer is trusted no further than a person's word: one that
// changed what was written is refused, each thing by its name, beside
// the same answer with it kept.
func TestAPluginThatChangesWhatWasWrittenOfAServiceIsRefused(t *testing.T) {
	request := &pluginv1.ServiceRequest{Name: "container", Written: greeter()}
	if err := pluginhost.CheckService("container", request, &pluginv1.ServiceResponse{Service: greeter()}); err != nil {
		t.Fatalf("what was written, kept, was refused: %v", err)
	}
	if err := pluginhost.CheckService("container", request, &pluginv1.ServiceResponse{}); err == nil {
		t.Fatal("no answer was taken for one")
	}
	for what, change := range map[string]func(*typev1.Service){
		"image": func(l *typev1.Service) {
			l.Image = "docker.io/library/other@sha256:" + greeterImage[len(greeterImage)-64:]
		},
		"arguments":   func(l *typev1.Service) { l.Arguments = append(l.Arguments, "--privileged") },
		"ports":       func(l *typev1.Service) { l.Ports["debug"] = 9229 },
		"environment": func(l *typev1.Service) { l.Environment = map[string]string{"LD_PRELOAD": "/x.so"} },
		"exports":     func(l *typev1.Service) { l.Exports["LD_PRELOAD"] = "/x.so" },
		"ready":       func(l *typev1.Service) { l.Ready = nil },
		"apis": func(l *typev1.Service) {
			l.Apis = map[string]*typev1.ServiceApi{"api:default/other": {Port: "greeting", Exports: map[string]string{"OTHER": "{host}"}}}
		},
		"timeout":   func(l *typev1.Service) { l.Timeout = durationpb.New(1 * time.Second) },
		"endpoints": func(l *typev1.Service) { l.Endpoints["greet"] = "http://{host}:{port.greet}/" },
		"user":      func(l *typev1.Service) { l.User = "1" },
		"writable":  func(l *typev1.Service) { l.Writable = append(l.Writable, "/etc") },
	} {
		written := greeter()
		written.Environment, written.Timeout = map[string]string{"A": "b"}, durationpb.New(30*time.Second)
		written.User, written.Writable = "70", []string{"/var/lib/data"}
		written.Apis = map[string]*typev1.ServiceApi{"api:default/greet": {Port: "greet", Exports: map[string]string{"GREET": "{host}:{port.greet}"}}}
		written.Endpoints = map[string]string{"greet": "{host}:{port.greet}"}
		answered := greeter()
		answered.Environment, answered.Timeout = map[string]string{"A": "b"}, durationpb.New(30*time.Second)
		answered.User, answered.Writable = "70", []string{"/var/lib/data"}
		answered.Apis = map[string]*typev1.ServiceApi{"api:default/greet": {Port: "greet", Exports: map[string]string{"GREET": "{host}:{port.greet}"}}}
		answered.Endpoints = map[string]string{"greet": "{host}:{port.greet}"}
		change(answered)
		err := pluginhost.CheckService("container", &pluginv1.ServiceRequest{Name: "container", Written: written}, &pluginv1.ServiceResponse{Service: answered})
		if err == nil {
			t.Errorf("a changed %s was kept", what)
		}
	}
}

// CheckService names each thing of a service that a person may write. A
// field added to the shape and not to it would be one a plugin could
// change unseen: this fails then, and says where to add it.
func TestEveryFieldOfAServiceIsOneAPluginMustKeep(t *testing.T) {
	kept := map[string]bool{"image": true, "arguments": true, "ports": true, "environment": true, "ready": true, "timeout": true, "exports": true, "apis": true, "endpoints": true, "user": true, "writable": true}
	fields := (&typev1.Service{}).ProtoReflect().Descriptor().Fields()
	for i := range fields.Len() {
		if name := string(fields.Get(i).Name()); !kept[name] {
			t.Errorf("Service.%s is held by nothing: add it to pluginhost.CheckService, to its test, and here", name)
		}
	}
	if fields.Len() != len(kept) {
		t.Errorf("Service has %d fields and CheckService keeps %d", fields.Len(), len(kept))
	}
}
