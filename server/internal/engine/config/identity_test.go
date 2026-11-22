package config_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
)

// An entity that says spec.frontseat is read one way, whatever key it
// says: its kind as Backstage spells it, and its name and namespace as
// an entity reference takes them, never folded into one by Unicode's
// rules. Each refusal has its control, the same key said by the entity
// written as Backstage writes it.
func TestAnEntityIsReadOneWayForEveryKey(t *testing.T) {
	entity := func(kind, name, frontseat string) files {
		return files{config.File: "plugins: {go: {}, backstage: {}}\n", "app/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: " + kind + "\nmetadata: {name: \"" + name + "\"}\nspec:\n  frontseat:\n" + frontseat}
	}
	keys := map[string]struct{ kind, frontseat string }{
		"release":   {"Component", "    release: {tag: \"{unit}@{version}\"}\n"},
		"service":   {"Resource", "    service: {as: postgres}\n"},
		"test":      {"Component", "    test: {tools: [buf]}\n"},
		"platforms": {"Component", "    platforms: [linux/amd64]\n"},
		"plugins":   {"Component", "    plugins: {go: {test-shards: 2}}\n"},
		"generated": {"Component", "    generated: [{directory: api, by: contracts}]\n"},
	}
	for key, c := range keys {
		if _, err := config.Read(t.Context(), "", entity(c.kind, "app", c.frontseat)); err != nil {
			t.Errorf("%s, control: %v", key, err)
		}
		lower := strings.ToLower(c.kind)
		if _, err := config.Read(t.Context(), "", entity(lower, "app", c.frontseat)); err == nil || !strings.Contains(err.Error(), "kind is written as Backstage writes it: "+c.kind) {
			t.Errorf("%s said by kind %s: %v", key, lower, err)
		}
		nameless := entity(c.kind, "app", c.frontseat)
		nameless["app/catalog-info.yaml"] = strings.Replace(nameless["app/catalog-info.yaml"], "metadata: {name: \"app\"}", "metadata: {title: app}", 1)
		if _, err := config.Read(t.Context(), "", nameless); err == nil || !strings.Contains(err.Error(), "spec.frontseat is said by an entity, which has a kind and a name") {
			t.Errorf("%s said by an entity of no name: %v", key, err)
		}
		// The Kelvin sign, which Unicode folds to k.
		if _, err := config.Read(t.Context(), "", entity(c.kind, "Kache", c.frontseat)); err == nil || !strings.Contains(err.Error(), "is said by an entity whose reference is none") || strings.Contains(err.Error(), "ache") {
			t.Errorf("%s said by a name Unicode folds: %v", key, err)
		}
	}
}
