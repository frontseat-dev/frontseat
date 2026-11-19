package testmodule

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The modules a workspace uses are read as go.work writes them, in a
// block or one by one, comments and all, and none is made up.
func TestTheModulesAWorkspaceUsesAreReadFromItsGoWork(t *testing.T) {
	root := t.TempDir()
	work := "go 1.27\n\n// The modules.\nuse (\n\t./api/gen/go // generated\n\t\t./client\t// tabs\n  // a comment alone\n)\n\nuse ./specs\nuse \"./quoted\"\n\nreplace example.com/x v0.0.0 => ./x\n"
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte(work), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"api/gen/go", "client", "specs", "quoted"}; !slices.Equal(got, want) {
		t.Errorf("read %q; want %q", got, want)
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Workspace(root); err == nil {
		t.Errorf("a go.work that uses no module was read as %q", got)
	}
}
