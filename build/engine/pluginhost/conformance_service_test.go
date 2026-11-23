package pluginhost_test

import (
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost/conformance"
)

// release strategies and services share one count of "asked
// for what it does not serve, and refused", so a fixture of one kind
// meets the other's requirement, and no mutation shows a service fixture
// that proves too little refused.
func TestAServiceFixtureThatProvesTooLittleIsRefused(t *testing.T) {
	valid := func(name string, f conformance.Fixture) error {
		return conformance.ValidateFixtures([]string{name}, map[string]conformance.Fixture{name: f})
	}
	if err := valid("postgres", postgresFixture(t)); err != nil {
		t.Fatalf("the sound fixture: %v", err)
	}
	// A service fixture never asked a name it does not claim, with a
	// branch refusal standing in for it.
	f := postgresFixture(t)
	var claimed []conformance.ServiceCase
	for _, c := range f.Services {
		if c.Request.GetName() == "postgres" {
			claimed = append(claimed, c)
		}
	}
	f.Services = claimed
	if err := valid("postgres", f); err == nil {
		t.Fatal("control: a service fixture with no unclaimed name was accepted")
	}
	f.Branches = []conformance.BranchCase{{Request: &pluginv1.BranchRequest{Strategy: "x"}, Refused: "ANYTHING"}}
	if err := valid("postgres", f); err == nil {
		t.Error("a service fixture never asked an unclaimed name was accepted for a refused branch")
	}
	// A release fixture never asked a strategy it does not serve, with a
	// service refusal standing in for it.
	r := releaseFixture()
	var branches []conformance.BranchCase
	for _, c := range r.Branches {
		if c.Request.GetStrategy() == "release-flow" {
			branches = append(branches, c)
		}
	}
	var versions []conformance.VersionCase
	for _, c := range r.Versions {
		if c.Request.GetStrategy() == "conventional-commits" {
			versions = append(versions, c)
		}
	}
	r.Branches, r.Versions = branches, versions
	if err := valid("release", r); err == nil {
		t.Fatal("control: a release fixture with no unserved strategy was accepted")
	}
	r.Services = []conformance.ServiceCase{{Request: &pluginv1.ServiceRequest{Name: "x"}, Refused: "ANYTHING"}}
	if err := valid("release", r); err == nil {
		t.Error("a release fixture never asked an unserved strategy was accepted for a refused service")
	}
	// A service fixture need hold no refusal of what was written at all,
	// though versioning must: postgres's refusals can all be deleted.
	g := postgresFixture(t)
	var kept []conformance.ServiceCase
	for _, c := range g.Services {
		if c.Refused == "" || c.Request.GetName() != "postgres" {
			kept = append(kept, c)
		}
	}
	g.Services = kept
	if err := valid("postgres", g); err == nil {
		t.Log("note: a service fixture with none of its plugin's refusals is accepted (the harness cannot know a plugin refuses)")
	}
}
