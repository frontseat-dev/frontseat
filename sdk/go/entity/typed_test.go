package entity

import (
	"strings"
	"testing"
)

func TestATypedReferenceLeavesOutTheNamespaceItDoesNotSay(t *testing.T) {
	for written, want := range map[string]Ref{
		"daemon":                     {Kind: "component", Name: "daemon"},
		"system:payments":            {Kind: "system", Name: "payments"},
		"frontseat/daemon":           {Kind: "component", Namespace: "frontseat", Name: "daemon"},
		"Component:Frontseat/Daemon": {Kind: "component", Namespace: "frontseat", Name: "daemon"},
	} {
		got, err := Typed(written)
		if err != nil || got != want {
			t.Errorf("Typed(%q) = %v, %v; want %v", written, got, err, want)
		}
		if again, _ := Typed(got.Written()); again != want {
			t.Errorf("%q written again reads as %v", got.Written(), again)
		}
	}
	for _, refused := range []string{"", ":daemon", "/daemon", "component:/daemon", "a/b/c", "Dæmon"} {
		if _, err := Typed(refused); err == nil || strings.Count(err.Error(), "names no entity") != 1 {
			t.Errorf("Typed(%q): %v, want it refused, saying so once", refused, err)
		}
	}
	for _, refused := range []Ref{{Kind: "component"}, {Kind: "component", Name: "a b"}, {Kind: "component", Namespace: "A B", Name: "a"}} {
		if err := refused.ValidateTyped(); err == nil {
			t.Errorf("%v was taken", refused)
		}
	}
}

func TestATypedNameFindsItsEntityInAnyNamespaceAndAnAmbiguousOneIsRefused(t *testing.T) {
	held := []Ref{New("component", "frontseat", "daemon"), New("component", "shop", "payments"), New("system", "shop", "payments"), New("component", "frontseat", "local"), New("component", "other", "local")}
	for written, want := range map[string]Ref{
		"daemon":                     New("component", "frontseat", "daemon"),
		"payments":                   New("component", "shop", "payments"),
		"system:payments":            New("system", "shop", "payments"),
		"component:frontseat/daemon": New("component", "frontseat", "daemon"),
		"other/local":                New("component", "other", "local"),
	} {
		typed, _ := Typed(written)
		got, found, err := Find(typed, held)
		if err != nil || !found || got != want {
			t.Errorf("Find(%q) = %v, %v, %v; want %v", written, got, found, err, want)
		}
	}
	// The control: a name two namespaces hold is refused, naming both;
	// one nothing holds is not found, and refused nothing.
	typed, _ := Typed("local")
	_, found, err := Find(typed, held)
	if err == nil || found || !strings.Contains(err.Error(), "component:frontseat/local") || !strings.Contains(err.Error(), "component:other/local") {
		t.Errorf("Find(local) = %v %v, want it refused naming both", found, err)
	}
	typed, _ = Typed("component:default/daemon")
	if _, found, err := Find(typed, held); found || err != nil {
		t.Errorf("a namespace that holds no such entity found one: %v %v", found, err)
	}
}
