// Package sourcetest gives a test a repository of the files it names, as
// a run that begins now sees it, alone or in a workspace beside others.
package sourcetest

import (
	"context"
	"testing"
	"time"

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

// Ran is a run of a directory that is one repository: it lists the
// directory, as a run that begins now lists it, and reads each of named
// by its path beneath the directory, with slashes, as a plugin reads a
// file, there or not. It returns how to wait, as a build daemon waits,
// for what the run listed and read to change (source.Looks.Await),
// looking every while.
func Ran(ctx context.Context, directory string, every time.Duration, named ...string) (func(context.Context) ([]string, error), error) {
	snapshot, err := new(source.Keeper).Take(ctx, source.Tree{Root: directory, Paths: map[string]string{"": "."}})
	if err != nil {
		return nil, err
	}
	for _, name := range named {
		if _, _, err := snapshot.Read(ctx, "", name); err != nil {
			return nil, err
		}
	}
	looks := snapshot.Looks()
	return func(ctx context.Context) ([]string, error) { return looks.Await(ctx, every) }, nil
}
