// Package sourcetest gives a test a repository of the files it names, as
// a run that begins now sees it, alone or in a workspace beside others.
package sourcetest

import (
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// Taken writes each of files, named by its path beneath the repository
// with slashes, to a directory of the test's own, and returns the
// snapshot a run that begins now takes of that repository.
func Taken(t testing.TB, files map[string]string) *source.Snapshot {
	t.Helper()
	return Workspace(t, map[string]string{"": "."}, files)
}

// Workspace writes each of files, named by its path beneath the
// workspace's directory with slashes, to a directory of the test's own,
// and returns the snapshot a run that begins now takes of the
// repositories paths names, each by the workspace's name for it and the
// directory it is in there.
func Workspace(t testing.TB, paths map[string]string, files map[string]string) *source.Snapshot {
	t.Helper()
	root := t.TempDir()
	testfiles.Tree(t, root, files)
	snapshot, err := new(source.Keeper).Take(t.Context(), source.Tree{Root: root, Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
