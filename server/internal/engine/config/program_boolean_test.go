package config_test

import (
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
)

func TestProductionReviewProgramBooleanSpellings(t *testing.T) {
	for _, item := range []struct {
		written string
		want    bool
	}{{"true", true}, {"True", true}, {"TRUE", true}, {"false", false}, {"False", false}, {"FALSE", false}} {
		t.Run(item.written, func(t *testing.T) {
			read, err := config.Read(t.Context(), "", files{
				config.File:         "plugins: {}\n",
				"catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: app}\nspec:\n  frontseat:\n    actions:\n      serve:\n        program: " + item.written + "\n        tools: [caddy]\n        command: [caddy, run]\n",
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(read.Actions()) != 1 || read.Actions()[0].Program != item.want {
				t.Fatalf("program: %s has the wrong program marker: %+v", item.written, read.Actions())
			}
		})
	}
}
