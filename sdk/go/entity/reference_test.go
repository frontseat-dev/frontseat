package entity

import (
	"strings"
	"testing"
)

func TestCatalogIdentityDoesNotRequireAProviderSchema(t *testing.T) {
	ref := New("service-blueprint", "tenant.scope", strings.Repeat("a", 100))
	if err := ref.Validate(); err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(ref.String())
	if err != nil || parsed != ref {
		t.Fatalf("generic identity did not round trip: %v, %v", parsed, err)
	}
}

func TestAReferenceIsReadAsAPersonWritesOne(t *testing.T) {
	for written, want := range map[string]string{
		"gateway":                   "component:default/gateway",
		"component:gateway":         "component:default/gateway",
		"Component:default/gateway": "component:default/gateway",
		"system:storefront":         "system:default/storefront",
		"group:edge/team-edge":      "group:edge/team-edge",
		"edge/gateway":              "component:edge/gateway",
		"Component:EDGE/Gateway":    "component:edge/gateway",
	} {
		got, err := Parse(written)
		if err != nil || got.String() != want {
			t.Errorf("Parse(%q) = %q, %v, want %q", written, got, err, want)
		}
	}
}

func TestANamespaceLeftOutInAnEntitysFileIsThatEntitysOwn(t *testing.T) {
	for written, want := range map[string]string{
		"greet":                 "component:edge/greet",
		"component:greet":       "component:edge/greet",
		"component:other/greet": "component:other/greet",
		"resource:orders-db":    "resource:edge/orders-db",
	} {
		got, err := ParseIn(written, "edge")
		if err != nil || got.String() != want {
			t.Errorf("ParseIn(%q, edge) = %q, %v, want %q", written, got, err, want)
		}
	}
}

func TestWhatNamesNoEntityIsRefused(t *testing.T) {
	for _, written := range []string{"", ":gateway", "component:", "component:/gateway", "component:default/", "a/b/c", "component:default/a:b", "a b", "../a", "component:default/-name", strings.Repeat("a", 256), strings.Repeat("a", 256) + ":name", strings.Repeat("a", 256) + "/name"} {
		if got, err := Parse(written); err == nil {
			t.Errorf("Parse(%q) = %q, want it refused", written, got)
		}
	}
}

func TestAReferenceReadsBackAsItIsWritten(t *testing.T) {
	want := New("System", "", "storefront")
	text, _ := want.MarshalText()
	var got Ref
	if err := got.UnmarshalText(text); err != nil || got != want || string(text) != "system:default/storefront" {
		t.Errorf("%q read back as %v, %v, want %v", text, got, err, want)
	}
}

// A reference is said on a line without what every component of the
// default namespace has, and with whatever else tells it apart.
func TestAReferenceIsSaidShortWithWhatTellsItApart(t *testing.T) {
	for want, ref := range map[string]Ref{
		"gateway":                   New("component", "default", "gateway"),
		"system:gateway":            New("system", "default", "gateway"),
		"frontseat/gateway":         New("component", "frontseat", "gateway"),
		"api:frontseat/gateway-api": New("api", "frontseat", "gateway-api"),
	} {
		if got := ref.Short(); got != want {
			t.Errorf("%v is said %q, want %q", ref, got, want)
		}
	}
}
