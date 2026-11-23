package pattern

import (
	"encoding/json"
	"github.com/frontseat-dev/frontseat/testsupport/testmodule"
	"os"
	"path/filepath"
	"testing"
)

// TestTheRustTestHostSelectsAsTheHostDoes holds the matcher of the Rust
// plugin testkit (sdk/plugin/test) to this one: both run the same table
// of cases, and a case either disagrees on fails here or there. Each is
// asked as the host globs for a plugin, its patterns held to the
// contract's language first (Spelling). A case
// with a directory is asked of Within, as the host's glob is; one without
// of Select.
func TestTheRustTestHostSelectsAsTheHostDoes(t *testing.T) {
	// The testkit's, beside its crate: its Component's catalog file gives
	// this package's tests it.
	root, err := testmodule.Root()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "sdk", "plugin-test", "testdata", "select.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Patterns  []string `json:"patterns"`
		Path      string   `json:"path"`
		Directory *string  `json:"directory"`
		Selected  bool     `json:"selected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("the table holds no case")
	}
	for _, c := range cases {
		// As the host globs for a plugin: what the contract's language
		// leaves out is no pattern, and selects nothing.
		spelled := Spelling(c.Patterns)
		got, in := Select(spelled, c.Path), "the root"
		if c.Directory != nil {
			got, in = Within(*c.Directory, c.Path, spelled), *c.Directory
		}
		if got != c.Selected {
			t.Errorf("%q beneath %q selects %q: %v, the table says %v", c.Patterns, in, c.Path, got, c.Selected)
		}
	}
}
