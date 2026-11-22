package testworkload_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testworkload"
)

// Every tool is given the same source: a comparison of tools that build
// different things says nothing.
func TestEveryToolIsGivenTheSameSource(t *testing.T) {
	shape := testworkload.Shape{Modules: 12, Group: 5}
	written := map[string]string{}
	frontseat := func(dir string, shape testworkload.Shape) error { return testworkload.Frontseat(dir, shape, "compare") }
	bazel := func(dir string, shape testworkload.Shape) error { return testworkload.Bazel(dir, shape, "", nil) }
	for tool, write := range map[string]func(string, testworkload.Shape) error{"frontseat": frontseat, "bazel": bazel, "buck2": testworkload.Buck2} {
		dir := t.TempDir()
		if err := write(dir, shape); err != nil {
			t.Fatal(err)
		}
		written[tool] = dir
	}
	for i := range shape.Modules {
		for _, file := range testworkload.Sources {
			name := filepath.Join(testworkload.Module(i), file)
			want, err := os.ReadFile(filepath.Join(written["frontseat"], name))
			if err != nil {
				t.Fatal(err)
			}
			for tool, dir := range written {
				if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(got) != string(want) {
					t.Errorf("%s is given %q for %s, err %v; want %q", tool, got, name, err, want)
				}
			}
		}
	}
}

// The first module of a group is needed by the others of it, and needs
// none.
func TestTheFirstOfAGroupIsNeededByTheRest(t *testing.T) {
	shape := testworkload.Shape{Modules: 12, Group: 5}
	for i, want := range []int{-1, 0, 0, 0, 0, -1, 5, 5, 5, 5, -1, 10} {
		if got := shape.Needs(i); got != want {
			t.Errorf("module %d needs %d; want %d", i, got, want)
		}
	}
}

// The workspace says which Bazel builds it where a version is given, and
// says nothing of one where none is.
func TestABazelWorkspaceSaysTheVersionItIsGiven(t *testing.T) {
	shape := testworkload.Shape{Modules: 2, Group: 2}
	given := t.TempDir()
	if err := testworkload.Bazel(given, shape, "8.4.2", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(given, ".bazelversion")); err != nil || string(got) != "8.4.2\n" {
		t.Errorf("a workspace given 8.4.2 says %q, err %v; want %q", got, err, "8.4.2\n")
	}
	none := t.TempDir()
	if err := testworkload.Bazel(none, shape, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(none, ".bazelversion")); !os.IsNotExist(err) {
		t.Errorf("a workspace given no version has a .bazelversion, err %v; want none", err)
	}
}
