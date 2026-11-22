package config_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
)

func runtimeCatalog(body string) files {
	return files{
		"frontseat.yaml":           "plugins: {}\n",
		"parent/catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: app}\nspec:\n  frontseat:\n    test:\n" + body,
	}
}

func TestATestRuntimeDeclaresDataAndNeverAReplacementCommand(t *testing.T) {
	read, err := config.Read(t.Context(), "repo", runtimeCatalog("      inputs: [fixtures/**, '//producer:out']\n      tools: [node]\n      environment: {MODE: test, EMPTY: ''}\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := read.TestRuntimes()
	if len(got) != 1 || got[0].Directory != "parent" || got[0].Repository != "repo" || got[0].File != "parent/catalog-info.yaml" || got[0].Entity.String() != "component:default/app" || len(got[0].Inputs) != 2 || len(got[0].Tools) != 1 || got[0].Environment["MODE"] != "test" {
		t.Fatalf("runtime declaration lost its origin or data: %+v", got)
	}
	for _, body := range []string{
		"      command: [true]\n", "      needs: [compile]\n", "      shards: [first]\n",
		"      inputs: fixture\n", "      inputs: [true]\n", "      inputs: [null]\n", "      inputs: ['']\n",
		"      tools: [3]\n", "      tools: [null]\n", "      environment: []\n",
		"      environment: {MODE: true}\n", "      environment: {MODE: null}\n", "      environment: {'BAD=NAME': text}\n",
		"      environment: {MODE: \"with\\0nul\"}\n", "      environment: {MODE: one, MODE: two}\n",
	} {
		if _, err := config.Read(t.Context(), "repo", runtimeCatalog(body)); err == nil || !strings.Contains(err.Error(), "parent/catalog-info.yaml") {
			t.Errorf("invalid runtime must be refused at its source: %q: %v", body, err)
		}
	}
	for _, kind := range []string{"API", "Resource", "System"} {
		f := runtimeCatalog("      inputs: []\n")
		f["parent/catalog-info.yaml"] = strings.Replace(f["parent/catalog-info.yaml"], "kind: Component", "kind: "+kind, 1)
		if _, err := config.Read(t.Context(), "repo", f); err == nil || !strings.Contains(err.Error(), "Component") {
			t.Errorf("%s acquired test ownership: %v", kind, err)
		}
	}
}

func TestRuntimeDeclarationsCannotSelectByDocumentOrder(t *testing.T) {
	f := runtimeCatalog("      inputs: []\n")
	f["parent/catalog-info.yaml"] += "---\n" + f["parent/catalog-info.yaml"]
	if _, err := config.Read(t.Context(), "", f); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate test runtime declarations were accepted: %v", err)
	}
}
