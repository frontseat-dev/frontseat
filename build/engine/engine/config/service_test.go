package config_test

import (
	"google.golang.org/protobuf/types/known/durationpb"
	"slices"
	"strings"
	"testing"
	"time"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/build/engine/engine/config"
)

func resourceWith(service string) files {
	return files{
		"frontseat.yaml": "plugins: {backstage: {}}\n",
		"catalog-info.yaml": `apiVersion: backstage.io/v1alpha1
kind: Resource
metadata: {name: db}
spec:
  type: database
  owner: team
  frontseat:
    service:
` + service,
	}
}

// A service is read into the one shape a service has, each thing written
// where that shape holds it, and nothing that was not written.
func TestAServiceIsReadIntoTheShapeAServiceHas(t *testing.T) {
	read, err := config.Read(t.Context(), "repo", resourceWith(`      as: container
      image: docker.io/library/redis:7@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec
      arguments: [--port, "{port.redis}"]
      ports: {redis: 6379}
      environment: {MODE: test}
      ready:
        - port: redis
        - http: {port: redis, path: /health, body: ok}
        - command: [redis-cli, ping]
      ready-timeout: 1h
      exports: {REDIS: "{host}:{port.redis}"}
      user: "999"
      writable: [/data]
`))
	if err != nil {
		t.Fatal(err)
	}
	services := read.Services()
	if len(services) != 1 || services[0].As != "container" || services[0].Entity.String() != "resource:default/db" {
		t.Fatalf("read %+v", services)
	}
	want := &typev1.Service{
		Image:       "docker.io/library/redis:7@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec",
		Arguments:   []string{"--port", "{port.redis}"},
		Ports:       map[string]int32{"redis": 6379},
		Environment: map[string]string{"MODE": "test"},
		Ready: []*typev1.ServiceReady{
			{Check: &typev1.ServiceReady_Port{Port: "redis"}},
			{Check: &typev1.ServiceReady_Http{Http: &typev1.ServiceHttpReady{Port: "redis", Path: "/health", Body: "ok"}}},
			{Check: &typev1.ServiceReady_Command{Command: &typev1.ServiceCommandReady{Arguments: []string{"redis-cli", "ping"}}}},
		},
		Timeout:  durationpb.New(3600 * time.Second),
		Exports:  map[string]string{"REDIS": "{host}:{port.redis}"},
		User:     "999",
		Writable: []string{"/data"},
	}
	if !proto.Equal(services[0].Said, want) {
		t.Fatalf("read\n %v\nwant\n %v", services[0].Said, want)
	}
	// With only what it is started as, nothing else is said.
	bare, err := config.Read(t.Context(), "repo", resourceWith("      as: postgres\n"))
	if err != nil || proto.Size(bare.Services()[0].Said) != 0 {
		t.Fatalf("a service that names only what it is started as: %v, %v", bare, err)
	}
}

// What the shape of a service cannot carry is refused where the file is
// read, by its key: a number the shape cannot hold is refused by what
// reads the file, so none is ever cut short on its way to a plugin,
// a readiness check is of one kind, and a key written to say nothing is
// no way to say the plugin's own.
func TestWhatAServiceCannotCarryIsRefusedWhereItIsRead(t *testing.T) {
	for says, service := range map[string]string{
		"does not say what it is: write as":                                          "      image: x\n",
		"service takes no key privileged":                                            "      as: container\n      privileged: true\n",
		"service.ports is not names, each with its port, a whole number":             "      as: postgres\n      ports: {sql: 4294974296}\n",
		"service.ports: a port is a number from 1 to 65535":                          "      as: postgres\n      ports: {sql: 65536}\n",
		"a port is a number from 1 to 65535":                                         "      as: postgres\n      ports: {sql: 0}\n",
		"service takes no key timeout; it takes":                                     "      as: postgres\n      timeout: 120\n",
		"service.ready-timeout is not a length of time":                              "      as: postgres\n      ready-timeout: 120\n",
		"service.ready-timeout is not a length of time, written":                     "      as: postgres\n      ready-timeout: 2 minutes\n",
		"service.ready-timeout is a length of time more than zero":                   "      as: postgres\n      ready-timeout: -5s\n",
		"service.ready-timeout is a length of time more than zero, in whole seconds": "      as: postgres\n      ready-timeout: 1500ms\n",
		"service.ready-timeout is more than the hour":                                "      as: postgres\n      ready-timeout: 61m\n",
		"service.ready[0] is one of port, http or command":                           "      as: postgres\n      ready: [{port: sql, command: [\"true\"]}]\n",
		"service.ready[1] is one of port, http or command":                           "      as: postgres\n      ready: [{port: sql}, {}]\n",
		"service.exports is written and says nothing":                                "      as: postgres\n      exports: {}\n",
		"service.arguments is written and says nothing":                              "      as: postgres\n      arguments: []\n",
		"service.environment is written and says nothing":                            "      as: postgres\n      environment: {}\n",
		"service.ports is written and says nothing":                                  "      as: postgres\n      ports: {}\n",
		"service.ready is written and says nothing":                                  "      as: postgres\n      ready: []\n",
		"service.ready-timeout is written and says nothing":                          "      as: postgres\n      ready-timeout: 0s\n",
		"service.image is written and says nothing":                                  "      as: postgres\n      image: \"\"\n",
	} {
		_, err := config.Read(t.Context(), "repo", resourceWith(service))
		if err == nil || !strings.Contains(err.Error(), says) || !strings.Contains(err.Error(), "catalog-info.yaml") {
			t.Errorf("want a mistake in catalog-info.yaml saying %q, got %v", says, err)
		}
	}
}

// Where a service asks for text, text is what it takes: a number or a
// switch written there is refused by its key, as a test's environment
// and an action's command refuse one, and is never turned into its
// letters. The control is each written as text.
func TestAServiceTakesOnlyTextWhereItAsksForText(t *testing.T) {
	for key, service := range map[string]string{
		"as":          "      as: 1\n",
		"image":       "      as: container\n      image: 1.5\n",
		"arguments":   "      as: container\n      arguments: [1]\n",
		"environment": "      as: container\n      environment: {DEBUG: true}\n",
		"exports":     "      as: container\n      exports: {A: 1}\n",
		"endpoints":   "      as: container\n      endpoints: {a: 1}\n",
		"user":        "      as: container\n      user: 70\n",
		"writable":    "      as: container\n      writable: [1]\n",
		"ready":       "      as: container\n      ready: [{command: [sleep, 1]}]\n",
		"ports":       "      as: container\n      ports: {a: \"80\"}\n",
		"program":     "      program: true\n",
		"ready port":  "      as: container\n      ready: [{port: 80}]\n",
		"ready http":  "      as: container\n      ready: [{http: {port: 80, path: /}}]\n",
	} {
		key, _, _ = strings.Cut(key, " ")
		_, err := config.Read(t.Context(), "repo", resourceWith(service))
		if err == nil || !strings.Contains(err.Error(), "service."+key+" is not ") {
			t.Errorf("%s: want it refused by its key, got %v", key, err)
		}
	}
	if _, err := config.Read(t.Context(), "repo", resourceWith(`      as: "1"
      image: "1.5"
      arguments: ["1"]
      environment: {DEBUG: "true"}
      exports: {A: "1"}
      user: "70"
      writable: ["/1"]
      ready: [{command: [sleep, "1"]}, {http: {port: a, path: /, body: "1"}}, {port: a}]
      ports: {a: 80}
      endpoints: {a: "{host}:{port.a}"}
`)); err != nil {
		t.Errorf("control, each written as text: %v", err)
	}
	// A Component's service is a program and the APIs it provides, each
	// text too.
	component := func(service string) files {
		repository := resourceWith(service)
		repository["catalog-info.yaml"] = strings.Replace(repository["catalog-info.yaml"], "kind: Resource", "kind: Component", 1)
		return repository
	}
	for key, service := range map[string]string{
		"an API's port":   "      program: \":serve\"\n      ports: {a: 80}\n      apis: {\"api:default/a\": {port: 80, exports: {A: \"{host}\"}}}\n",
		"an API's export": "      program: \":serve\"\n      ports: {a: 80}\n      apis: {\"api:default/a\": {port: a, exports: {A: 1}}}\n",
	} {
		if _, err := config.Read(t.Context(), "repo", component(service)); err == nil || !strings.Contains(err.Error(), "service.apis is not ") {
			t.Errorf("%s: want it refused by its key, got %v", key, err)
		}
	}
	if _, err := config.Read(t.Context(), "repo", component("      program: \":serve\"\n      ports: {a: 80}\n      ready: [{port: a}]\n      apis: {\"api:default/a\": {port: a, exports: {A: \"{host}:{port.a}\"}}}\n")); err != nil {
		t.Errorf("control, a program and its API written as text: %v", err)
	}
	// The user is asked for in quotes by the refusal itself: a number is
	// what YAML reads of one written bare.
	if _, err := config.Read(t.Context(), "repo", resourceWith("      as: container\n      user: 70\n")); err == nil || !strings.Contains(err.Error(), `written in quotes, as "70"`) {
		t.Errorf("a user written bare: %v", err)
	}
}

// What a service refuses of a file it does not repeat: the kind of an
// entity that is none started, a name no shell exports, a port that is
// none.
func TestWhatAServiceRefusesIsNotRepeated(t *testing.T) {
	for service, says := range map[string]string{
		"      as: postgres\n      exports: {SECRET-A: x}\n":     "service.exports: a name the shell can export",
		"      as: postgres\n      environment: {SECRET-A: x}\n": "service.environment: a name the shell can export",
		"      as: postgres\n      ports: {SECRET: 5432}\n":      "service.ports: a port is named in lower-case letters, digits and dashes, beginning with a letter",
	} {
		_, err := config.Read(t.Context(), "repo", resourceWith(service))
		if err == nil || !strings.Contains(err.Error(), says) || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("want a mistake saying %q and nothing of what was written, got %v", says, err)
		}
	}
	system := resourceWith("      as: postgres\n")
	system["catalog-info.yaml"] = strings.Replace(system["catalog-info.yaml"], "kind: Resource", "kind: Secret", 1)
	if _, err := config.Read(t.Context(), "repo", system); err == nil || !strings.Contains(err.Error(), "only a Resource or Component is started as a service") || strings.Contains(err.Error(), "Secret") {
		t.Errorf("a service said by another kind: %v", err)
	}
}

// An API a Component's service provides is written as every other
// reference of its catalog file is, bare and read in the entity's
// namespace, the kind api where none is written, and held in full. The
// whole reference is read as before. Refused: a key of another kind, a
// key that names no entity, and one API written twice in two spellings.
func TestAServicesAPIIsWrittenAsAReferenceIsWritten(t *testing.T) {
	component := func(namespace, apis string) files {
		repository := resourceWith("      program: \":serve\"\n      ports: {http: 80}\n      ready: [{port: http}]\n      apis:\n" + apis)
		repository["catalog-info.yaml"] = strings.Replace(repository["catalog-info.yaml"], "kind: Resource\nmetadata: {name: db}", "kind: Component\nmetadata: {name: orders, namespace: "+namespace+"}", 1)
		return repository
	}
	export := "{port: http, exports: {ORDERS_URL: \"http://{host}:{port.http}\"}}"
	for written, want := range map[string]string{
		"orders-api":             "api:shop/orders-api",
		"api:orders-api":         "api:shop/orders-api",
		"api:shop/orders-api":    "api:shop/orders-api",
		"api:default/orders-api": "api:default/orders-api",
		"default/orders-api":     "api:default/orders-api",
		"API:Shop/Orders-Api":    "api:shop/orders-api",
	} {
		read, err := config.Read(t.Context(), "repo", component("shop", "        \""+written+"\": "+export+"\n"))
		if err != nil {
			t.Errorf("%s: %v", written, err)
			continue
		}
		apis := read.Services()[0].Said.GetApis()
		if _, held := apis[want]; !held || len(apis) != 1 {
			t.Errorf("%s: held as %v, want %s", written, apis, want)
		}
	}
	for written, says := range map[string]string{
		"        component:orders-api: " + export + "\n":                                    "service.apis: an API is named as a reference is written",
		"        \"api:\": " + export + "\n":                                                "service.apis: an API is named as a reference is written",
		"        orders-api: " + export + "\n        api:shop/orders-api: " + export + "\n": "service.apis names one API twice",
	} {
		if _, err := config.Read(t.Context(), "repo", component("shop", written)); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("want %q, got %v", says, err)
		}
	}
}

// One word is one thing in one shape: a service's variables are its
// environment, a map, as a test's are; its arguments are its arguments;
// and the names an action that publishes inherits from where the daemon
// runs are its inherit-environment, a list. The abbreviations, and an
// action's environment written as a list, are refused by the keys
// taken. The control is each written by its word.
func TestOneWordIsOneThingInOneShape(t *testing.T) {
	publishing := func(keys string) files {
		return files{config.File: "plugins: {}\n", "catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: app}\nspec:\n  frontseat:\n    actions:\n      release:\n        command: [gh, release, create]\n        kind: publish\n" + keys}
	}
	for _, c := range []struct {
		refused, says string
		allowed       files
	}{
		{"      as: container\n      env: {MODE: test}\n", "service takes no key env; it takes", resourceWith("      as: container\n      environment: {MODE: test}\n")},
		{"      as: container\n      args: [--port, \"1\"]\n", "service takes no key args; it takes", resourceWith("      as: container\n      arguments: [--port, \"1\"]\n")},
	} {
		if _, err := config.Read(t.Context(), "repo", resourceWith(c.refused)); err == nil || !strings.Contains(err.Error(), c.says) || !strings.Contains(err.Error(), "arguments") || !strings.Contains(err.Error(), "environment") {
			t.Errorf("%q: want %q and the keys taken, got %v", c.refused, c.says, err)
		}
		if _, err := config.Read(t.Context(), "repo", c.allowed); err != nil {
			t.Errorf("control: %v", err)
		}
	}
	if _, err := config.Read(t.Context(), "repo", publishing("        environment: [GH_TOKEN]\n")); err == nil || !strings.Contains(err.Error(), "an action takes no key environment; it takes") || !strings.Contains(err.Error(), "inherit-environment") {
		t.Errorf("an action's environment written as a list: %v", err)
	}
	read, err := config.Read(t.Context(), "repo", publishing("        inherit-environment: [GH_TOKEN]\n"))
	if err != nil || len(read.Actions()) != 1 || !slices.Equal(read.Actions()[0].InheritEnvironment, []string{"GH_TOKEN"}) {
		t.Errorf("control, inherit-environment: %v, %v", read, err)
	}
}
