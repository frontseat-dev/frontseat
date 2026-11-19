package backstage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/testsupport/backstagetest"
)

const held = `apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  annotations: {backstage.io/source-location: "url:https://example.com/acme/services/tree/main/hello/"}
  name: hello
spec:
  dependsOn: [component:greet, resource:billing/ledger, resource:queue]
  owner: team
  system: storefront
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: greet
  annotations:
    backstage.io/source-location: url:https://example.com/acme/services/tree/main/greet
spec:
  owner: team
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: ledger
  namespace: billing
  annotations:
    backstage.io/source-location: url:https://example.com/acme/ledger/tree/release
spec:
  system: payments
---
apiVersion: backstage.io/v1alpha1
kind: Resource
metadata:
  name: ledger
  namespace: billing
  annotations:
    backstage.io/source-location: url:https://example.com/acme/ledger/tree/release
spec:
  system: payments
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: ledger
  namespace: shop
  annotations:
    backstage.io/source-location: url:https://example.com/acme/shop/tree/main/ledger
---
apiVersion: backstage.io/v1alpha1
kind: System
metadata:
  name: storefront
---
apiVersion: backstage.io/v1alpha1
kind: System
metadata:
  name: warehouse
---
apiVersion: backstage.io/v1alpha1
kind: Group
metadata:
  name: team
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: unplaced
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: borrowed
  annotations:
    backstage.io/source-location: url:https://someone:a-password@example.com/acme/services/tree/main/hello/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: escapes
  annotations:
    backstage.io/source-location: url:https://example.com/acme/services/tree/main/../../etc
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: needy
  annotations:
    backstage.io/source-location: url:https://example.com/acme/services/tree/main/needy
spec:
  dependsOn: [component:gone]
`

// served is a catalog of the test's over a fake Backstage holding what
// is given.
func served(t *testing.T, documents string) (*Catalog, backstagetest.Served) {
	t.Helper()
	fake := backstagetest.Serve(t, documents)
	c, err := New(Config{Base: fake.URL, Token: fake.Token, Loopback: true})
	if err != nil {
		t.Fatal(err)
	}
	return c, fake
}

// typed reads names as a person types them.
func typed(t *testing.T, written ...string) []entity.Ref {
	t.Helper()
	var out []entity.Ref
	for _, each := range written {
		ref, err := entity.Typed(each)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ref)
	}
	return out
}

// An entity resolves to the repository and directory that hold it, and
// those of the components it depends on, by the relations the catalog
// holds; what else it depends on brings no source.
func TestAnEntityResolvesToWhereItAndTheComponentsItDependsOnAre(t *testing.T) {
	c, _ := served(t, held)
	got, err := c.Resolve(context.Background(), "local", typed(t, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Checkouts) != 1 || got.Checkouts[0].Remote != "https://example.com/acme/services" || got.Checkouts[0].Ref != "main" || strings.Join(got.Checkouts[0].Roots, ",") != "greet,hello" {
		t.Errorf("resolved %+v, want services at main with greet and hello", got.Checkouts)
	}
	var located []string
	for _, l := range got.Located {
		located = append(located, l.Ref.String()+"@"+l.Root)
	}
	if strings.Join(located, " ") != "component:default/hello@hello component:default/greet@greet" {
		t.Errorf("located %v", located)
	}
}

// A system brings its components, and a group what it owns; one with
// nothing in it is refused by name.
func TestASystemAndAGroupBringTheirComponents(t *testing.T) {
	c, _ := served(t, held)
	for written, want := range map[string]string{"system:storefront": "greet,hello", "group:team": "greet,hello", "system:billing/payments": ""} {
		got, err := c.Resolve(context.Background(), "local", typed(t, written))
		if err != nil {
			t.Errorf("%s: %v", written, err)
			continue
		}
		if roots := strings.Join(got.Checkouts[0].Roots, ","); roots != want {
			t.Errorf("%s brought %+v, want %q", written, got.Checkouts, want)
		}
	}
	_, err := c.Resolve(context.Background(), "local", typed(t, "system:warehouse"))
	refusedAs(t, "an empty system", err, NotFound)
	if err == nil || !strings.Contains(err.Error(), "nothing is in system:default/warehouse") {
		t.Errorf("an empty system: %v", err)
	}
}

// A typed name is found in whatever namespace holds it; one two hold is
// refused naming both, and the control, written in full, resolves. One
// the catalog does not hold is refused naming it, and so is one a
// component depends on, with what depends on it.
func TestATypedNameIsFoundOnceOrRefusedByName(t *testing.T) {
	c, _ := served(t, held)
	ctx := context.Background()
	_, err := c.Resolve(ctx, "local", typed(t, "ledger"))
	refusedAs(t, "a name two namespaces hold", err, Ambiguous)
	if err == nil || !strings.Contains(err.Error(), "component:billing/ledger") || !strings.Contains(err.Error(), "component:shop/ledger") {
		t.Errorf("an ambiguous name: %v, want both named", err)
	}
	if got, err := c.Resolve(ctx, "local", typed(t, "component:shop/ledger")); err != nil || got.Checkouts[0].Remote != "https://example.com/acme/shop" {
		t.Errorf("the control, written in full: %+v, %v", got, err)
	}
	_, err = c.Resolve(ctx, "local", typed(t, "nobody"))
	refusedAs(t, "a name no one holds", err, NotFound)
	if err == nil || !strings.Contains(err.Error(), "component:nobody") {
		t.Errorf("a name no one holds: %v", err)
	}
	_, err = c.Resolve(ctx, "local", typed(t, "needy"))
	refusedAs(t, "a dependency no one holds", err, NotFound)
	if err == nil || !strings.Contains(err.Error(), "component:default/gone") || !strings.Contains(err.Error(), "component:default/needy depends on it") {
		t.Errorf("a dependency no one holds: %v", err)
	}
}

// An entity whose source the catalog cannot say, or says as no
// workspace can hold it, is refused by name, never repeating what it
// said; an API or a Resource asked for brings no source and is refused.
func TestAnEntityWithoutASourceItCanHoldIsRefused(t *testing.T) {
	c, _ := served(t, held)
	for written, says := range map[string]string{
		"unplaced":                "backstage.io/source-location",
		"borrowed":                "a user or a password",
		"escapes":                 "outside",
		"resource:billing/ledger": "brings no source",
	} {
		_, err := c.Resolve(context.Background(), "local", typed(t, written))
		refusedAs(t, written, err, Invalid)
		if err == nil || !strings.Contains(err.Error(), says) || strings.Contains(err.Error(), "a-password") {
			t.Errorf("%s: %v, want it refused saying %q and not the password", written, err, says)
		}
	}
}

// No more than so many entities are taken in one resolution.
func TestAResolutionTakesSoManyEntitiesAtMost(t *testing.T) {
	c, _ := served(t, held)
	// hello is found by its name, then asked for, and greet, which it
	// depends on: three.
	c.limits.entities = 2
	_, err := c.Resolve(context.Background(), "local", typed(t, "hello"))
	refusedAs(t, "more entities than its limit", err, Unavailable)
	c.limits.entities = 3
	if _, err := c.Resolve(context.Background(), "local", typed(t, "hello")); err != nil {
		t.Errorf("the control, within its limit: %v", err)
	}
}

// A typed name is asked for by a query that pages, as Backstage's does.
func TestATypedNameIsAskedForByQueryAPageAtATime(t *testing.T) {
	c, fake := served(t, held)
	c.limits.page = 1
	_, err := c.Resolve(context.Background(), "local", typed(t, "ledger"))
	if !errors.As(err, new(*Refused)) {
		t.Fatalf("an ambiguous name across pages: %v", err)
	}
	pages := 0
	for _, asked := range fake.Asked() {
		if strings.HasPrefix(asked, "/api/catalog/entities/by-query") {
			pages++
		}
	}
	if pages < 2 {
		t.Errorf("the query was asked in %d pages, want more than one: %v", pages, fake.Asked())
	}
}
