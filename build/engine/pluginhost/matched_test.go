package pluginhost_test

import (
	"slices"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
)

// A plugin is told of each root it is asked about the files its
// interests select directly in it, and not those beneath, nor those of a
// root it is not asked about; asked about none, the repository's root.
func TestARootIsToldTheFilesItsInterestsSelectDirectlyInIt(t *testing.T) {
	source := files{
		"catalog-info.yaml":             "",
		"a/catalog-info.yaml":           "",
		"a/package.json":                "",
		"a/README.md":                   "",
		"a/b/package.json":              "",
		"a/node_modules/x/package.json": "",
	}
	interests := []string{"**/catalog-info.yaml", "**/package.json", "!**/node_modules/**"}
	for roots, want := range map[string][]string{
		"a":   {"a/catalog-info.yaml", "a/package.json"},
		"a/b": {"a/b/package.json"},
		"":    {"catalog-info.yaml"},
	} {
		request := &pluginv1.AnalyzeRequest{}
		if roots != "" {
			request.Roots = []string{roots}
		}
		if err := pluginhost.Matched(t.Context(), source, interests, request); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(request.GetMatched(), want) {
			t.Errorf("root %q is told %q; want %q", roots, request.GetMatched(), want)
		}
	}
}
