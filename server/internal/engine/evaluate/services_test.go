package evaluate

import (
	"errors"
	runnerv1 "github.com/frontseat-dev/frontseat/server/internal/runnerapi/frontseat/runner/v1alpha1"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/server/internal/engine/catalog"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
)

const digest = "docker.io/library/busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"

func resource(namespace, name string, ports map[string]int32) config.Service {
	return config.Service{File: "catalog-info.yaml", Entity: entity.New("resource", namespace, name), As: "container", Said: &typev1.Service{
		Image: digest, Ports: ports, Ready: []*typev1.ServiceReady{{Check: &typev1.ServiceReady_Port{Port: first(ports)}}},
	}}
}

// checkoutOfOrders is a checkout that consumes the API orders, which
// orders-service provides, and the database orders-db.
func checkoutOfOrders() (consumer, provider, api, database entity.Ref) {
	return entity.New("component", "default", "checkout"), entity.New("component", "default", "orders-service"), entity.New("api", "default", "orders"), ref("default", "orders-db")
}

// Catalog resolution alone: the services are already prepared here. The
// acceptance spec must separately prove building and starting the provider.
func TestAnAPIConsumersServicesComeFromItsProvider(t *testing.T) {
	consumer, provider, api, database := checkoutOfOrders()
	db := resource("default", "orders-db", map[string]int32{"sql": 5432})
	db.Said.Exports = map[string]string{"DATABASE": "{host}:{port.sql}"}
	app := resource("default", "orders-service", map[string]int32{"http": 8080})
	app.Entity.Kind = "component"
	app.Said.Apis = map[string]*typev1.ServiceApi{api.String(): {Port: "http", Exports: map[string]string{"ORDERS_URL": "http://{host}:{port.http}"}}}
	started := map[entity.Ref]config.Service{provider: app, database: db}
	for _, directResource := range []bool{true, false} {
		t.Run(map[bool]string{true: "Resource control", false: "API provider"}[directResource], func(t *testing.T) {
			edge := catalog.DeclaredRelation{Type: "consumesApi", InverseType: "apiConsumedBy", Target: api}
			if directResource {
				edge = catalog.DeclaredRelation{Type: "dependsOn", InverseType: "dependencyOf", Target: database}
			}
			out := serviceCatalog(t, consumer, []catalog.Descriptor{
				{Entity: catalog.Entity{Ref: consumer, Roots: []string{"checkout"}}, Relations: []catalog.DeclaredRelation{edge}},
				{Entity: catalog.Entity{Ref: api}},
				{Entity: catalog.Entity{Ref: provider, Roots: []string{"orders"}}, Relations: []catalog.DeclaredRelation{
					{Type: "providesApi", InverseType: "apiProvidedBy", Target: api},
					{Type: "dependsOn", InverseType: "dependencyOf", Target: database},
				}},
				{Entity: catalog.Entity{Ref: database}},
			})
			if err := services(&out, started); err != nil {
				t.Fatal(err)
			}
			beside := out.Actions[0].Beside
			if beside == nil {
				t.Fatal("consumer received no runtime services")
			}
			got := decoded(t, beside.Sandbox).GetServices()
			if directResource {
				if len(got) != 1 || got[0].GetEntity() != database.String() || beside.Environment["DATABASE"] != "127.0.0.1:5432" {
					t.Fatalf("direct Resource control: services=%v bindings=%v", got, beside.Environment)
				}
				return
			}
			if len(got) != 2 || got[0].GetEntity() != database.String() || got[1].GetEntity() != provider.String() {
				t.Fatalf("want database then its API provider, got %v", got)
			}
			if len(beside.Environment) != 1 || beside.Environment["ORDERS_URL"] != "http://127.0.0.1:8080" {
				t.Fatalf("API consumer must receive only its direct API binding: %v", beside.Environment)
			}
			if got[1].GetService().GetEnvironment()["DATABASE"] != "127.0.0.1:5432" {
				t.Fatalf("the API provider did not receive its database binding: %v", got[1].GetService().GetEnvironment())
			}
		})
	}
}

// A catalog may contain declarations that are not runnable: contracts,
// external providers and identities with the same name in another namespace.
// None of those is permission to choose a different local runtime.
func TestAPIServiceResolutionKeepsCatalogIdentitiesAndOwnership(t *testing.T) {
	consumer := entity.New("component", "shop", "checkout")
	provider := entity.New("component", "shop", "orders")
	other := entity.New("component", "other", "orders")
	api := entity.New("api", "shop", "orders")
	second := entity.New("api", "shop", "health")
	database := ref("shop", "db")
	for _, test := range []struct {
		name      string
		apis      []entity.Ref
		otherAPI  entity.Ref
		prepared  []entity.Ref
		want      []entity.Ref
		ambiguous bool
	}{
		{name: "one local provider", apis: []entity.Ref{api}, prepared: []entity.Ref{provider}, want: []entity.Ref{provider}},
		{name: "two APIs of one provider", apis: []entity.Ref{api, second}, prepared: []entity.Ref{provider}, want: []entity.Ref{provider}},
		{name: "same name in another namespace", apis: []entity.Ref{entity.New("api", "other", "orders")}, prepared: []entity.Ref{provider}},
		{name: "no prepared provider", apis: []entity.Ref{api}},
		{name: "unprepared alternative", apis: []entity.Ref{api}, otherAPI: api, prepared: []entity.Ref{provider}, want: []entity.Ref{provider}},
		{name: "two prepared providers", apis: []entity.Ref{api}, otherAPI: api, prepared: []entity.Ref{provider, other}, ambiguous: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			consumerDescriptor := catalog.Descriptor{Entity: catalog.Entity{Ref: consumer, Repository: "shop", Manifest: "checkout/catalog-info.yaml", Roots: []string{"checkout"}}}
			for _, consumed := range test.apis {
				consumerDescriptor.Relations = append(consumerDescriptor.Relations, catalog.DeclaredRelation{Type: "consumesApi", InverseType: "apiConsumedBy", Target: consumed})
			}
			descriptors := []catalog.Descriptor{
				consumerDescriptor,
				{Entity: catalog.Entity{Ref: provider}, Relations: []catalog.DeclaredRelation{
					{Type: "providesApi", InverseType: "apiProvidedBy", Target: api},
					{Type: "providesApi", InverseType: "apiProvidedBy", Target: second},
					{Type: "dependsOn", InverseType: "dependencyOf", Target: database},
				}},
				{Entity: catalog.Entity{Ref: api}, Relations: []catalog.DeclaredRelation{
					// Even an adapter emitting an infrastructure edge on the
					// API cannot turn the contract into a runtime owner.
					{Type: "dependsOn", InverseType: "dependencyOf", Target: database},
				}},
				{Entity: catalog.Entity{Ref: second}},
				{Entity: catalog.Entity{Ref: database}},
			}
			if test.otherAPI.Kind != "" {
				descriptors = append(descriptors, catalog.Descriptor{Entity: catalog.Entity{Ref: other}, Relations: []catalog.DeclaredRelation{{Type: "providesApi", InverseType: "apiProvidedBy", Target: test.otherAPI}}})
			}
			out := serviceCatalog(t, consumer, descriptors)
			started := map[entity.Ref]config.Service{}
			for _, ref := range test.prepared {
				started[ref] = resource(ref.Namespace, ref.Name, map[string]int32{"http": 8080})
			}
			got, _, err := serviceRelationships(out.Catalog.Nodes(nil), out.descriptors, started)
			if test.ambiguous {
				var mistake *model.Mistake
				if !errors.As(err, &mistake) || !strings.Contains(err.Error(), "more than one prepared service provider") {
					t.Fatalf("want an ambiguous-provider diagnostic, got %v", err)
				}
				if mistake.File != "shop/checkout/catalog-info.yaml" {
					t.Fatalf("diagnostic points outside the consumer's repository: %s", mistake.File)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got[consumer], test.want) {
				t.Fatalf("consumer runtime dependencies: got %v, want %v", got[consumer], test.want)
			}
			if len(got[api]) != 0 || !reflect.DeepEqual(got[provider], []entity.Ref{database}) {
				t.Fatalf("infrastructure belongs to the provider, not its API: %v", got)
			}
		})
	}
}

func TestAServiceCannotReplaceItsDependencyBinding(t *testing.T) {
	provider := entity.New("component", "default", "provider")
	db := resource("default", "db", map[string]int32{"sql": 5432})
	db.Said.Exports = map[string]string{"DATABASE": "{host}:{port.sql}"}
	app := resource("default", "provider", map[string]int32{"http": 8080})
	declared := map[entity.Ref][]entity.Ref{
		component: {provider},
		provider:  {ref("default", "db")},
	}
	started := map[entity.Ref]config.Service{ref("default", "db"): db, provider: app}
	if _, _, err := besideOf(component, "app/catalog-info.yaml", declared, started, nil); err != nil {
		t.Fatalf("binding without an override is allowed: %v", err)
	}
	app.Said.Environment = map[string]string{"DATABASE": "another database"}
	if _, _, err := besideOf(component, "app/catalog-info.yaml", declared, started, nil); err == nil || !strings.Contains(err.Error(), "sets DATABASE") {
		t.Fatalf("service silently replaced its dependency binding: %v", err)
	}
}

func TestRecursiveAPIProvidersHaveDirectBindingsAndRejectStartupCycles(t *testing.T) {
	consumer := entity.New("component", "default", "test")
	proxy := entity.New("component", "default", "proxy")
	app := entity.New("component", "default", "app")
	public := entity.New("api", "default", "public")
	internal := entity.New("api", "default", "internal")
	db := ref("default", "db")
	descriptors := []catalog.Descriptor{
		{Entity: catalog.Entity{Ref: consumer, Roots: []string{"test"}}, Relations: []catalog.DeclaredRelation{{Type: "consumesApi", InverseType: "apiConsumedBy", Target: public}}},
		{Entity: catalog.Entity{Ref: proxy}, Relations: []catalog.DeclaredRelation{
			{Type: "providesApi", InverseType: "apiProvidedBy", Target: public},
			{Type: "consumesApi", InverseType: "apiConsumedBy", Target: internal},
		}},
		{Entity: catalog.Entity{Ref: app}, Relations: []catalog.DeclaredRelation{
			{Type: "providesApi", InverseType: "apiProvidedBy", Target: internal},
			{Type: "dependsOn", InverseType: "dependencyOf", Target: db},
		}},
		{Entity: catalog.Entity{Ref: public}},
		{Entity: catalog.Entity{Ref: internal}},
		{Entity: catalog.Entity{Ref: db}},
	}
	started := map[entity.Ref]config.Service{}
	for _, item := range []struct {
		ref    entity.Ref
		port   int32
		export string
	}{{db, 5432, "DATABASE"}, {app, 8080, "APP_URL"}, {proxy, 9090, "PUBLIC_URL"}} {
		l := resource(item.ref.Namespace, item.ref.Name, map[string]int32{"serve": item.port})
		l.Said.Exports = map[string]string{item.export: "{host}:{port.serve}"}
		if item.ref.Kind == "component" {
			l.Entity.Kind = "component"
			api := internal
			if item.ref == proxy {
				api = public
			}
			l.Said.Apis = map[string]*typev1.ServiceApi{api.String(): {Port: "serve", Exports: l.Said.Exports}}
			l.Said.Exports = nil
		}
		started[item.ref] = l
	}
	out := serviceCatalog(t, consumer, descriptors)
	if err := services(&out, started); err != nil {
		t.Fatal(err)
	}
	beside := out.Actions[0].Beside
	got := decoded(t, beside.Sandbox).GetServices()
	if len(got) != 3 || got[0].GetEntity() != db.String() || got[1].GetEntity() != app.String() || got[2].GetEntity() != proxy.String() {
		t.Fatalf("runtime dependencies are not ordered database, app, proxy: %v", got)
	}
	for i, want := range []map[string]string{nil, {"DATABASE": "127.0.0.1:5432"}, {"APP_URL": "127.0.0.1:8080"}} {
		if !reflect.DeepEqual(got[i].GetService().GetEnvironment(), want) {
			t.Fatalf("%s receives transitive or missing bindings: %v", got[i].GetName(), got[i].GetService().GetEnvironment())
		}
	}
	if !reflect.DeepEqual(beside.Environment, map[string]string{"PUBLIC_URL": "127.0.0.1:9090"}) {
		t.Fatalf("test receives transitive or missing bindings: %v", beside.Environment)
	}
	// The catalog accepts a cycle. Runtime startup, which currently
	// requires dependencies to be ready first, diagnoses it separately.
	descriptors[2].Relations = append(descriptors[2].Relations, catalog.DeclaredRelation{Type: "consumesApi", InverseType: "apiConsumedBy", Target: public})
	cyclic := serviceCatalog(t, consumer, descriptors)
	if err := services(&cyclic, started); err == nil || !strings.Contains(err.Error(), "runtime service startup cycle") {
		t.Fatalf("want a runtime cycle diagnostic, got %v", err)
	}
}

func serviceCatalog(t *testing.T, consumer entity.Ref, descriptors []catalog.Descriptor) Evaluation {
	t.Helper()
	g, err := catalog.Assemble(nil, descriptors)
	if err != nil {
		t.Fatal(err)
	}
	out := Evaluation{Catalog: g, descriptors: descriptors}
	for _, entity := range g.Entities() {
		for _, root := range entity.Roots {
			out.Entities = append(out.Entities, record.Located{Ref: entity.Ref, Repository: entity.Repository, Root: root})
			if entity.Ref == consumer {
				out.Actions = append(out.Actions, plan.Action{Label: model.Label{Path: root, Plugin: "go", Name: "test"},
					Declared: &pluginv1.Action{Name: "test", Test: true}})
			}
		}
	}
	return out
}

func first(ports map[string]int32) string {
	for name := range ports {
		return name
	}
	return ""
}

func ref(namespace, name string) entity.Ref { return entity.New("resource", namespace, name) }

var component = entity.New("component", "default", "app")

func decoded(t *testing.T, raw []byte) *runnerv1.RunnerSandbox {
	t.Helper()
	var s runnerv1.RunnerSandbox
	if err := proto.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

func TestServicesStartAfterWhatTheyDependOnAndAreToldItsAddress(t *testing.T) {
	db := resource("default", "db", map[string]int32{"sql": 5432})
	idp := resource("default", "idp", map[string]int32{"http": 8080})
	idp.Said.Environment = map[string]string{"DB": "{host}:{db.port.sql}", "SELF": "{port.http}", "LITERAL": "{json}"}
	idp.Said.Arguments = []string{"--port={port.http}"}
	idp.Said.Exports = map[string]string{"IDP_URL": "http://{host}:{port.http}"}
	started := map[entity.Ref]config.Service{ref("default", "db"): db, ref("default", "idp"): idp}
	declared := map[entity.Ref][]entity.Ref{
		component:             {ref("default", "idp"), ref("default", "db")},
		ref("default", "idp"): {ref("default", "db")},
	}
	beside, _, err := besideOf(component, "app/catalog-info.yaml", declared, started, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded(t, beside.Sandbox)
	if len(got.Services) != 2 || got.Services[0].Entity != "resource:default/db" || got.Services[1].Entity != "resource:default/idp" {
		t.Fatalf("services %v, want db then idp, each once", got.Services)
	}
	env := got.Services[1].GetService().GetEnvironment()
	if env["DB"] != "127.0.0.1:5432" || env["SELF"] != "8080" || env["LITERAL"] != "{json}" {
		t.Errorf("idp's environment %v: want its dependency's address and its own port, and other braces as written", env)
	}
	if args := got.Services[1].GetService().GetArguments(); len(args) != 1 || args[0] != "--port=8080" {
		t.Errorf("idp's arguments %v, want its port filled in", args)
	}
	if told := got.Services[1].GetService(); len(told.GetExports()) != 0 || told.GetTimeout().AsDuration() != 120*time.Second {
		t.Errorf("the runner is told %v exported and %d seconds: want nothing exported, and the time a service is given where it names none", told.GetExports(), told.GetTimeout().GetSeconds())
	}
	if beside.Environment["IDP_URL"] != "http://127.0.0.1:8080" {
		t.Errorf("what the tests are told: %v", beside.Environment)
	}
}

func TestWhatAComponentsServicesCannotBeIsRefused(t *testing.T) {
	db := resource("default", "db", map[string]int32{"sql": 5432})
	cases := map[string]struct {
		started  map[entity.Ref]config.Service
		declared map[entity.Ref][]entity.Ref
		says     string
	}{
		"a cycle": {
			map[entity.Ref]config.Service{ref("default", "a"): resource("default", "a", map[string]int32{"p": 1}), ref("default", "b"): resource("default", "b", map[string]int32{"p": 2})},
			map[entity.Ref][]entity.Ref{component: {ref("default", "a")}, ref("default", "a"): {ref("default", "b")}, ref("default", "b"): {ref("default", "a")}},
			"depends on itself",
		},
		"an address of a Resource not depended on": {
			map[entity.Ref]config.Service{ref("default", "db"): db, ref("default", "app2"): func() config.Service {
				l := resource("default", "app2", map[string]int32{"http": 80})
				l.Said.Environment = map[string]string{"X": "{db.port.sql}"}
				return l
			}()},
			map[entity.Ref][]entity.Ref{component: {ref("default", "db"), ref("default", "app2")}},
			"not an unambiguous granted port",
		},
		"a port not served": {
			map[entity.Ref]config.Service{ref("default", "db"): func() config.Service {
				l := resource("default", "db", map[string]int32{"sql": 5432})
				l.Said.Exports = map[string]string{"URL": "{port.http}"}
				return l
			}()},
			map[entity.Ref][]entity.Ref{component: {ref("default", "db")}},
			"not an unambiguous granted port",
		},
		"one name exported twice": {
			map[entity.Ref]config.Service{
				ref("default", "a"): func() config.Service {
					l := resource("default", "a", map[string]int32{"p": 1})
					l.Said.Exports = map[string]string{"URL": "x"}
					return l
				}(),
				ref("default", "b"): func() config.Service {
					l := resource("default", "b", map[string]int32{"p": 2})
					l.Said.Exports = map[string]string{"URL": "y"}
					return l
				}(),
			},
			map[entity.Ref][]entity.Ref{component: {ref("default", "a"), ref("default", "b")}},
			"both export URL",
		},
	}
	for name, c := range cases {
		if _, _, err := besideOf(component, "app/catalog-info.yaml", c.declared, c.started, nil); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want a mistake saying %q", name, err, c.says)
		}
	}
	// The control: a Resource that is not started gives the tests
	// nothing, and is no mistake.
	if beside, _, err := besideOf(component, "app/catalog-info.yaml", map[entity.Ref][]entity.Ref{component: {ref("default", "prod-db")}}, map[entity.Ref]config.Service{ref("default", "db"): db}, nil); err != nil || beside != nil {
		t.Errorf("a Resource that is not started: %v %v, want nothing", beside, err)
	}
}

// A test is told where it reaches each service it is given, in one
// variable, by the service's entity: a Resource's endpoints at the ports
// the test is granted, and a Component's only at the port of an API the
// test consumes. The control, the same services with no endpoint, tells
// the test nothing of them.
func TestATestIsToldWhereItReachesEachServiceAndNoMore(t *testing.T) {
	consumer, provider, api, database := checkoutOfOrders()
	descriptors := []catalog.Descriptor{
		{Entity: catalog.Entity{Ref: consumer, Roots: []string{"checkout"}}, Relations: []catalog.DeclaredRelation{
			{Type: "consumesApi", InverseType: "apiConsumedBy", Target: api},
			{Type: "dependsOn", InverseType: "dependencyOf", Target: database},
		}},
		{Entity: catalog.Entity{Ref: api}},
		{Entity: catalog.Entity{Ref: provider, Roots: []string{"orders"}}, Relations: []catalog.DeclaredRelation{
			{Type: "providesApi", InverseType: "apiProvidedBy", Target: api},
		}},
		{Entity: catalog.Entity{Ref: database}},
	}
	given := func(endpoints bool) map[entity.Ref]config.Service {
		db := resource("default", "orders-db", map[string]int32{"sql": 5432})
		app := resource("default", "orders-service", map[string]int32{"http": 8080, "admin": 9000})
		app.Entity.Kind = "component"
		app.Said.Apis = map[string]*typev1.ServiceApi{api.String(): {Port: "http", Exports: map[string]string{"ORDERS_URL": "http://{host}:{port.http}"}}}
		if endpoints {
			db.Said.Endpoints = map[string]string{"sql": "postgres://{host}:{port.sql}/orders"}
			app.Said.Endpoints = map[string]string{"web": "http://{host}:{port.http}/", "admin": "http://{host}:{port.admin}/"}
		}
		return map[entity.Ref]config.Service{provider: app, database: db}
	}
	control := serviceCatalog(t, consumer, descriptors)
	if err := services(&control, given(false)); err != nil {
		t.Fatal(err)
	}
	if told, found := control.Actions[0].Beside.Environment[endpointsVariable]; found {
		t.Fatalf("services with no endpoint told the test %s=%s", endpointsVariable, told)
	}
	out := serviceCatalog(t, consumer, descriptors)
	if err := services(&out, given(true)); err != nil {
		t.Fatal(err)
	}
	want := `{"component:default/orders-service":{"web":"http://127.0.0.1:8080/"},"resource:default/orders-db":{"sql":"postgres://127.0.0.1:5432/orders"}}`
	if told := out.Actions[0].Beside.Environment[endpointsVariable]; told != want {
		t.Fatalf("the test was told\n %s\nwant\n %s", told, want)
	}
	for _, service := range decoded(t, out.Actions[0].Beside.Sandbox).GetServices() {
		if len(service.GetService().GetEndpoints()) > 0 {
			t.Errorf("the runner is told the endpoints of %s, which are the test's alone", service.GetEntity())
		}
	}
}

// No export may be named where the endpoints are told, endpoints or
// none; the control, the same export named otherwise, is given.
func TestNoExportIsNamedWhereEndpointsAreTold(t *testing.T) {
	consumer, _, _, database := checkoutOfOrders()
	descriptors := []catalog.Descriptor{
		{Entity: catalog.Entity{Ref: consumer, Roots: []string{"checkout"}}, Relations: []catalog.DeclaredRelation{{Type: "dependsOn", InverseType: "dependencyOf", Target: database}}},
		{Entity: catalog.Entity{Ref: database}},
	}
	for _, c := range []struct {
		export    string
		endpoints bool
		refused   bool
	}{{endpointsVariable, false, true}, {endpointsVariable, true, true}, {"DATABASE", true, false}} {
		db := resource("default", "orders-db", map[string]int32{"sql": 5432})
		db.Said.Exports = map[string]string{c.export: "{host}:{port.sql}"}
		if c.endpoints {
			db.Said.Endpoints = map[string]string{"sql": "{host}:{port.sql}"}
		}
		out := serviceCatalog(t, consumer, descriptors)
		err := services(&out, map[entity.Ref]config.Service{database: db})
		if refused := err != nil && strings.Contains(err.Error(), endpointsVariable); refused != c.refused {
			t.Errorf("an export %s, endpoints %v: %v", c.export, c.endpoints, err)
		}
	}
}

// What a catalog file says wrongly of a test's services is the mistake of
// that file, never the daemon's failure: more services than a test is
// given, a port that is none, an export named as the variable a test is
// told its endpoints in. The control is a Resource given soundly.
func TestWhatIsWrongInATestsServicesIsItsFilesMistake(t *testing.T) {
	db := ref("default", "db")
	sound := resource("default", "db", map[string]int32{"sql": 5432})
	many, every := map[entity.Ref]config.Service{}, []entity.Ref{}
	for i := range 33 {
		name := "db-" + strconv.Itoa(i)
		many[ref("default", name)] = resource("default", name, map[string]int32{"sql": 5432})
		every = append(every, ref("default", name))
	}
	unserved := resource("default", "db", map[string]int32{"sql": 70000})
	exporting := resource("default", "db", map[string]int32{"sql": 5432})
	exporting.Said.Exports = map[string]string{endpointsVariable: "{host}:{port.sql}"}
	for name, c := range map[string]struct {
		started  map[entity.Ref]config.Service
		declared []entity.Ref
		file     string
	}{
		"more services than a test is given": {many, every, "app/catalog-info.yaml"},
		"a port that is none":                {map[entity.Ref]config.Service{db: unserved}, []entity.Ref{db}, "catalog-info.yaml"},
		"an export named as the endpoints":   {map[entity.Ref]config.Service{db: exporting}, []entity.Ref{db}, "app/catalog-info.yaml"},
	} {
		_, _, err := besideOf(component, "app/catalog-info.yaml", map[entity.Ref][]entity.Ref{component: c.declared}, c.started, nil)
		var mistake *model.Mistake
		if !errors.As(err, &mistake) || mistake.File != c.file {
			t.Errorf("%s: %v, want a mistake of %s", name, err, c.file)
		}
	}
	if beside, _, err := besideOf(component, "app/catalog-info.yaml", map[entity.Ref][]entity.Ref{component: {db}}, map[entity.Ref]config.Service{db: sound}, nil); err != nil || beside == nil {
		t.Errorf("a Resource given soundly: %v %v, want its services", beside, err)
	}
}

// A mistake in what a Component's tests are given is said of the catalog
// file the Component is written in, as services finds it in the catalog;
// one with no file is a mistake of none. The control is the same
// Component given a sound service.
func TestAMistakeInATestsServicesIsSaidOfItsComponentsFile(t *testing.T) {
	db := ref("default", "db")
	exporting := resource("default", "db", map[string]int32{"sql": 5432})
	exporting.Said.Exports = map[string]string{endpointsVariable: "{host}:{port.sql}"}
	catalogOf := func(manifest string) Evaluation {
		return serviceCatalog(t, component, []catalog.Descriptor{
			{Entity: catalog.Entity{Ref: component, Manifest: manifest, Roots: []string{"app"}}, Relations: []catalog.DeclaredRelation{{Type: "dependsOn", InverseType: "dependencyOf", Target: db}}},
			{Entity: catalog.Entity{Ref: db}},
		})
	}
	sound := catalogOf("app/catalog-info.yaml")
	if err := services(&sound, map[entity.Ref]config.Service{db: resource("default", "db", map[string]int32{"sql": 5432})}); err != nil {
		t.Fatalf("a sound service: %v", err)
	}
	for _, manifest := range []string{"app/catalog-info.yaml", ""} {
		out := catalogOf(manifest)
		err := services(&out, map[entity.Ref]config.Service{db: exporting})
		var mistake *model.Mistake
		if !errors.As(err, &mistake) || mistake.File != manifest {
			t.Errorf("described in %q: %v, want a mistake of that file", manifest, err)
		}
	}
}

// Of two endpoints a Resource's service cannot say, the one named is
// the first by name each time.
func TestAnEndpointThatCannotBeSaidIsNamedTheSameEachTime(t *testing.T) {
	db := ref("default", "db")
	unsaid := resource("default", "db", map[string]int32{"sql": 5432})
	if _, _, err := besideOf(component, "app/catalog-info.yaml", map[entity.Ref][]entity.Ref{component: {db}}, map[entity.Ref]config.Service{db: unsaid}, nil); err != nil {
		t.Fatalf("a Resource with no endpoints: %v", err)
	}
	unsaid.Said.Endpoints = map[string]string{"a": "{host}:{port.first}", "z": "{host}:{port.last}"}
	for range 64 {
		_, _, err := besideOf(component, "app/catalog-info.yaml", map[entity.Ref][]entity.Ref{component: {db}}, map[entity.Ref]config.Service{db: unsaid}, nil)
		if err == nil || !strings.Contains(err.Error(), "{port.first}") {
			t.Fatalf("%v, want the endpoint a named", err)
		}
	}
}

// Of two repositories whose catalog files consent to what is not theirs
// to give, the one named is the first by name each time.
func TestAConsentNotItsToGiveIsNamedTheSameEachTime(t *testing.T) {
	said := map[string][]config.Generated{}
	for _, repository := range []string{"a", "z"} {
		said[repository] = []config.Generated{{File: repository + "/catalog-info.yaml", Directory: "gen", Entity: component, By: component}}
	}
	if err := consented(Evaluation{}, nil); err != nil {
		t.Fatalf("no consent: %v", err)
	}
	for range 64 {
		err := consented(Evaluation{}, said)
		var mistake *model.Mistake
		if !errors.As(err, &mistake) || mistake.File != "a/catalog-info.yaml" {
			t.Fatalf("%v, want the first repository's consent named", err)
		}
	}
}
