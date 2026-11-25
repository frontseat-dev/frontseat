package source_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
)

// A workspace in which one repository's directory is within another's,
// or is another's, is refused by name, naming both: a place of the
// workspace is in one repository's directory or in none. The controls
// are a repository alone at the workspace's top, and two whose
// directories begin alike and are apart.
func TestARepositoryWithinAnothersIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		paths   map[string]string
		refused string
	}{
		"one at the top, another within it":        {map[string]string{"top": ".", "lib": "deps/lib"}, `"lib" in deps/lib, which is within .`},
		"one within another's directory":           {map[string]string{"app": "code", "lib": "code/deps/lib"}, `"lib" in code/deps/lib, which is within code`},
		"two in one directory":                     {map[string]string{"app": "code", "lib": "code/"}, "which is within code"},
		"the control: one alone at the top":        {map[string]string{"": "."}, ""},
		"the control: two that begin alike, apart": {map[string]string{"app": "code", "lib": "code-lib"}, ""},
	} {
		root := t.TempDir()
		for _, folder := range c.paths {
			if err := os.MkdirAll(filepath.Join(root, folder), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		_, err := new(source.Keeper).Take(t.Context(), source.Tree{Root: root, Paths: c.paths})
		switch {
		case c.refused == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)):
			t.Errorf("%s: %v, want it refused saying %s", name, err, c.refused)
		}
	}
}
