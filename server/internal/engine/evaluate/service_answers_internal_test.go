package evaluate

import (
	"google.golang.org/protobuf/types/known/durationpb"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
)

// loadedPlugin is the plugin of this checkout by that name, built to
// wasm and loaded in a host of its own.
func loadedPlugin(t *testing.T, name string) *pluginhost.Plugin {
	t.Helper()
	module, err := testmodule.Plugin(name)
	if err != nil {
		t.Fatal(err)
	}
	host, err := pluginhost.New(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := host.Load(t.Context(), module)
	if err != nil {
		t.Fatal(err)
	}
	return plugin
}

// What a service cannot say is refused once its plugin has answered,
// however the service came to be: asked of the real container plugin,
// which answers what it was told.
func TestWhatAServiceCannotSayIsRefusedWhoeverBuiltIt(t *testing.T) {
	claimed := map[string]*pluginhost.Plugin{"container": loadedPlugin(t, "container")}
	valid := func() config.Service {
		return config.Service{
			File: "catalog-info.yaml", Line: 9, Namespace: "default", Name: "greeter", As: "container",
			Said: &typev1.Service{
				Image: "docker.io/library/busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec",
				Ports: map[string]int32{"greet": 7000},
				Ready: []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Port{Port: "greet"}}},
			},
		}
	}
	if _, err := startedAs(t.Context(), &Answers{}, claimed, valid(), &Evaluation{}); err != nil {
		t.Fatalf("control: a valid service was refused: %v", err)
	}
	// The shape a service has carries no number that is cut short and no
	// readiness check of two kinds: those are refused where the file is
	// read (config). What the shape can still say wrongly is refused
	// here, whoever built it.
	for what, change := range map[string]func(*typev1.Service){
		"a port above 65535":       func(l *typev1.Service) { l.Ports["greet"] = 70000 },
		"a port of nothing":        func(l *typev1.Service) { l.Ports["greet"] = 0 },
		"a negative timeout":       func(l *typev1.Service) { l.Timeout = durationpb.New(-5 * time.Second) },
		"a timeout over an hour":   func(l *typev1.Service) { l.Timeout = durationpb.New(3601 * time.Second) },
		"ready by a check of none": func(l *typev1.Service) { l.Ready = []*typev1.ServiceReady{{}} },
		"ready by a port not served": func(l *typev1.Service) {
			l.Ready = []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Http{Http: &typev1.ServiceHttpReady{Port: "nosuch", Path: "/"}}}}
		},
		"an export no shell exports": func(l *typev1.Service) { l.Exports = map[string]string{"A-B": "x"} },
		"an image that is an option": func(l *typev1.Service) { l.Image = "--privileged" },
	} {
		written := valid()
		change(written.Said)
		if got, err := startedAs(t.Context(), &Answers{}, claimed, written, &Evaluation{}); err == nil {
			t.Errorf("%s was accepted: %v", what, got.Said)
		}
	}
}

// How something is started is asked of its plugin once, and kept: a
// build where nothing changed asks no plugin again.
func TestHowSomethingIsStartedIsAskedOnce(t *testing.T) {
	claimed := map[string]*pluginhost.Plugin{"postgres": loadedPlugin(t, "postgres")}
	answers := &Answers{}
	written := func() config.Service {
		return config.Service{File: "catalog-info.yaml", Line: 9, Namespace: "default", Name: "db", As: "postgres", Said: &typev1.Service{}}
	}
	first, second := &Evaluation{}, &Evaluation{}
	one, err := startedAs(t.Context(), answers, claimed, written(), first)
	if err != nil {
		t.Fatal(err)
	}
	two, err := startedAs(t.Context(), answers, claimed, written(), second)
	if err != nil {
		t.Fatal(err)
	}
	if first.Asked != 1 || first.Again != 0 || second.Asked != 0 || second.Again != 1 {
		t.Errorf("asked %d and given again %d the first time, %d and %d the second: want asked once, then given again", first.Asked, first.Again, second.Asked, second.Again)
	}
	if !proto.Equal(one.Said, two.Said) {
		t.Errorf("what was kept is not what was answered")
	}
	// Something else written is another question, and is asked.
	other := written()
	other.Said.Timeout = durationpb.New(30 * time.Second)
	third := &Evaluation{}
	if _, err := startedAs(t.Context(), answers, claimed, other, third); err != nil || third.Asked != 1 {
		t.Errorf("another question: asked %d, %v", third.Asked, err)
	}
}
