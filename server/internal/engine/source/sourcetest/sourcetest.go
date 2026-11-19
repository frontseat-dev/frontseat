// Package sourcetest gives a test a repository of the files it names, as
// a run that begins now sees it.
package sourcetest

import (
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
)

// Taken writes each of files, named by its path beneath the repository
// with slashes, to a directory of the test's own, and returns the
// snapshot a run that begins now takes of that repository.
func Taken(t testing.TB, files map[string]string) *source.Snapshot {
	t.Helper()
	root := t.TempDir()
	testfiles.Tree(t, root, files)
	snapshot, err := new(source.Keeper).Take(t.Context(), source.Tree{Root: root, Paths: map[string]string{"": "."}})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
