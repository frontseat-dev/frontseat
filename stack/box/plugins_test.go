package box

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The plugins of a directory are its <name>.wasm files; what a desktop or
// a writer leaves beside them, a name beginning with a dot, is not one.
func TestThePluginsOfADirectoryAreItsModulesAlone(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"go.wasm", "cargo.wasm", ".x.wasm", ".go.wasm123", ".DS_Store", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "dir.wasm"), 0o755); err != nil {
		t.Fatal(err)
	}
	modules, err := PluginsIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(modules)); !slices.Equal(got, []string{"cargo", "go"}) {
		t.Errorf("the plugins are %q, want cargo and go", got)
	}
	if _, err := PluginsIn(t.TempDir()); err == nil {
		t.Error("a directory of no plugin was taken")
	}
}
