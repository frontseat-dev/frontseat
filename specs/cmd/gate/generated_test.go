package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
)

// What is committed is held to what is generated file by file: one that
// differs, one generated no more and one not committed are each named,
// and the control, a directory alike, names none.
func TestGeneratedCodeIsComparedFileByFile(t *testing.T) {
	written := t.TempDir()
	testfiles.Tree(t, written, map[string]string{"a/a.pb.go": "a", "b.pb.go": "b", "new.pb.go": "new"})
	alike := t.TempDir()
	testfiles.Tree(t, alike, map[string]string{"a/a.pb.go": "a", "b.pb.go": "b", "new.pb.go": "new"})
	if differ, err := compared(alike, written); err != nil || len(differ) != 0 {
		t.Fatalf("a directory alike: %v, %v", differ, err)
	}
	committed := t.TempDir()
	testfiles.Tree(t, committed, map[string]string{"a/a.pb.go": "edited", "b.pb.go": "b", "old.pb.go": "old"})
	differ, err := compared(committed, written)
	slices.Sort(differ)
	if want := []string{filepath.Join("a", "a.pb.go"), "new.pb.go", "old.pb.go"}; err != nil || !slices.Equal(differ, want) {
		t.Fatalf("named %v (%v), want %v", differ, err, want)
	}
	if differ, err := compared(filepath.Join(committed, "absent"), written); err != nil || len(differ) != 3 {
		t.Fatalf("nothing committed: %v, %v; want every file generated", differ, err)
	}
}
