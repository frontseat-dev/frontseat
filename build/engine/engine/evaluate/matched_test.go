package evaluate

import (
	"slices"
	"testing"
)

// A plugin is told which of the files it looks for are at each root it is
// asked about (AnalyzeRequest.matched), and needs no read to learn it: the
// Backstage plugin finds the install at a root whose package.json matched
// its interests, and reads no package.json where only a catalog file did.
// The control is that root, a catalog file's alone, of no project.
func TestAPluginIsToldWhatItsInterestsMatchedAtEachRoot(t *testing.T) {
	root := t.TempDir()
	retainedWrite(t, root, "frontseat.yaml", "plugins:\n  backstage: {}\n")
	retainedWrite(t, root, "package.json", `{"name":"root","private":true,"workspaces":["plugins/*"],"devDependencies":{"@backstage/cli":"^0.36.6"}}`)
	retainedWrite(t, root, "yarn.lock", "__metadata:\n  version: 10\n  cacheKey: 10c0\n")
	retainedWrite(t, root, "catalog/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: catalog}\nspec: {type: service, lifecycle: production, owner: team}\n")
	r := newReadiness(t, root, "backstage")
	got, err := r.fresh()
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, project := range got.Projects {
		found = append(found, project.Root+" "+project.Kind)
	}
	if !slices.Equal(found, []string{" backstage-repository"}) {
		t.Errorf("projects %q; want the install at the root alone, which its package.json matched", found)
	}
}
