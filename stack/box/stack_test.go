package box

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// The stack's image is given the portal as its repository holds it:
// what its .gitignore names is not laid out, what a build or an install
// left and what a person keeps beside it (a secret among it), and a file
// that runs there runs in the image. The control: what the .gitignore
// keeps of Yarn's own directory, and a file whose name only begins as an
// ignored one does, are laid out all the same.
func TestTheStacksImageIsGivenThePortalAsItsRepositoryHoldsIt(t *testing.T) {
	portal := filepath.Join(t.TempDir(), "portal")
	testfiles.Tree(t, portal, map[string]string{
		".gitignore":                       "node_modules/\n.yarn/*\n!.yarn/releases\ndist\ndist-types\n.env\n*.local.yaml\n",
		".env":                             "GITHUB_TOKEN=secret\n",
		"app-config.local.yaml":            "token: secret\n",
		"package.json":                     "{}\n",
		"yarn.lock":                        "\n",
		".yarnrc.yml":                      "nodeLinker: node-modules\n",
		".yarn/releases/yarn.cjs":          "release\n",
		".yarn/cache/a.zip":                "cached\n",
		".yarn/install-state.gz":           "state\n",
		"node_modules/a/index.js":          "installed\n",
		"dist-types/index.d.ts":            "built\n",
		"packages/backend/dist/bundle.tgz": "built\n",
		"packages/backend/node_modules/b":  "installed\n",
		"packages/backend/src/index.ts":    "source\n",
		"packages/backend/src/distance.ts": "source\n",
		"packages/app/public/index.html":   "<html></html>\n",
	})
	if err := os.Chmod(filepath.Join(portal, ".yarn/releases/yarn.cjs"), 0o755); err != nil {
		t.Fatal(err)
	}
	laid := filepath.Join(t.TempDir(), "portal")
	if err := layPortal(portal, laid); err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := filepath.WalkDir(laid, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(laid, path)
		got = append(got, filepath.ToSlash(rel))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", ".yarn/releases/yarn.cjs", ".yarnrc.yml", "package.json", "packages/app/public/index.html", "packages/backend/src/distance.ts", "packages/backend/src/index.ts", "yarn.lock"}
	if !slices.Equal(got, want) {
		t.Errorf("laid out %q, want %q", got, want)
	}
	if info, err := os.Stat(filepath.Join(laid, ".yarn/releases/yarn.cjs")); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("a file that runs in the repository is laid out %v, %v; want 0755", info.Mode(), err)
	}
}

// A link in the portal is refused, not followed out of it.
func TestALinkInThePortalIsRefused(t *testing.T) {
	portal := filepath.Join(t.TempDir(), "portal")
	testfiles.Tree(t, portal, map[string]string{"package.json": "{}\n"})
	if err := os.Symlink("/etc/passwd", filepath.Join(portal, "passwd")); err != nil {
		t.Fatal(err)
	}
	if err := layPortal(portal, filepath.Join(t.TempDir(), "portal")); err == nil {
		t.Error("a link in the portal was laid out")
	}
}
