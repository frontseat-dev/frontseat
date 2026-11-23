package evaluate

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/catalog"
	"github.com/frontseat-dev/frontseat/build/engine/engine/config"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// A prepared Component receives its dependency bindings; a Resource's
// service keeps its explicit environment and address substitutions.
func TestResourceDependenciesKeepTheirExplicitEnvironment(t *testing.T) {
	db := resource("default", "db", map[string]int32{"sql": 5432})
	db.Said.Exports = map[string]string{"TOKEN": "database-token"}
	idp := resource("default", "idp", map[string]int32{"http": 8080})
	idp.Said.Environment = map[string]string{"DB": "{db.port.sql}"}
	declared := map[entity.Ref][]entity.Ref{component: {ref("default", "idp")}, ref("default", "idp"): {ref("default", "db")}}
	started := map[entity.Ref]config.Service{ref("default", "db"): db, ref("default", "idp"): idp}
	beside, _, err := besideOf(component, manifest{file: "app/catalog-info.yaml"}, declared, started, nil)
	if err != nil {
		t.Fatal(err)
	}
	env := decoded(t, beside.Sandbox).GetServices()[1].GetService().GetEnvironment()
	if env["DB"] != "5432" {
		t.Fatal("Resource address substitution failed")
	}
	if _, found := env["TOKEN"]; found {
		t.Error("Resource unexpectedly received its dependency's export TOKEN")
	}
	idp.Said.Environment["TOKEN"] = "idp-token"
	if _, _, err := besideOf(component, manifest{file: "app/catalog-info.yaml"}, declared, started, nil); err != nil {
		t.Fatalf("explicit Resource environment was refused: %v", err)
	}
}

func TestAnInverseResourceRelationCannotStartItForAComponent(t *testing.T) {
	db := ref("default", "db")
	for _, direct := range []bool{true, false} {
		descriptors := []catalog.Descriptor{
			{Entity: catalog.Entity{Ref: component, Roots: []string{"app"}}},
			{Entity: catalog.Entity{Ref: db}},
		}
		if direct {
			descriptors[0].Relations = []catalog.DeclaredRelation{{Type: "dependsOn", InverseType: "dependencyOf", Target: db}}
		} else {
			descriptors[1].Relations = []catalog.DeclaredRelation{{Type: "dependencyOf", InverseType: "dependsOn", Target: component}}
		}
		out := serviceCatalog(t, component, descriptors)
		if err := services(&out, map[entity.Ref]config.Service{db: resource("default", "db", map[string]int32{"sql": 5432})}); err != nil {
			t.Fatal(err)
		}
		if direct && out.Actions[0].Beside == nil {
			t.Fatal("declared dependsOn control did not start Resource")
		}
		if !direct && out.Actions[0].Beside != nil {
			t.Fatal("Resource dependencyOf starts a service for a Component that declares no dependsOn")
		}
	}
}

func TestAProviderBindingConflictNamesTheSameVariableEachTime(t *testing.T) {
	provider := entity.New("component", "default", "provider")
	db := resource("default", "db", map[string]int32{"sql": 5432})
	db.Said.Exports = map[string]string{"A": "a", "Z": "z"}
	app := resource("default", "provider", map[string]int32{"http": 8080})
	app.Said.Environment = map[string]string{"A": "override-a", "Z": "override-z"}
	declared := map[entity.Ref][]entity.Ref{component: {provider}, provider: {ref("default", "db")}}
	started := map[entity.Ref]config.Service{ref("default", "db"): db, provider: app}
	for range 256 {
		_, _, err := besideOf(component, manifest{file: "app/catalog-info.yaml"}, declared, started, nil)
		if err == nil || !strings.Contains(err.Error(), "sets A, which a dependency exports") {
			t.Fatalf("the first conflicting binding was not named: %v", err)
		}
	}
}

// Of two APIs a Component maps and does not provide, the one named is
// the first by name each time, whatever order a map gives them in. The
// control is a Component that maps none.
func TestAnAPIMappedAndNotProvidedIsNamedTheSameEachTime(t *testing.T) {
	provider := entity.New("component", "default", "provider")
	app := resource("default", "provider", map[string]int32{"http": 8080})
	app.Entity.Kind = "component"
	out := serviceCatalog(t, component, []catalog.Descriptor{{Entity: catalog.Entity{Ref: provider, Roots: []string{"provider"}}}})
	if _, err := servicePrograms(&out, map[entity.Ref]config.Service{provider: app}); err != nil {
		t.Fatalf("a Component that maps no API: %v", err)
	}
	app.Said.Apis = map[string]*typev1.ServiceApi{"api:default/a": {Port: "http"}, "api:default/z": {Port: "http"}}
	for range 256 {
		_, err := servicePrograms(&out, map[entity.Ref]config.Service{provider: app})
		if err == nil || !strings.Contains(err.Error(), "maps API api:default/a,") {
			t.Fatalf("the first API by name was not named: %v", err)
		}
	}
}
