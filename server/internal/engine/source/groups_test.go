package source_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
)

func TestSourceGroupsKeepLiteralBasesAndIndependentExclusions(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"parent[1]/native.txt", "parent[1]/data.txt", "parent1/neighbour.txt", "parent[1]/nested/own.txt"} {
		write(t, filepath.Join(root, name), name)
	}
	snapshot := taken(t, source.Tree{Root: root, Paths: map[string]string{"": "."}})
	groups := []pattern.Group{
		{Directory: "parent[1]", Patterns: []string{"native.txt"}},
		{Directory: "parent[1]", Patterns: []string{"*.txt", "!native.txt"}},
		{Directory: "parent[1]/nested", Patterns: []string{"own.txt"}},
		{Directory: "parent[1]", Patterns: []string{"data.txt"}},
	}
	files, err := snapshot.Select(t.Context(), "", groups)
	want := []string{"parent[1]/data.txt", "parent[1]/native.txt", "parent[1]/nested/own.txt"}
	if err != nil || !slices.Equal(files, want) {
		t.Fatalf("group union: %v, %v; want %v", files, err, want)
	}
}
