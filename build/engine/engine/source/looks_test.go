package source_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
)

// looked is a look at a tree and at the files beside it, once every
// file in it has settled: one a later look is held against.
func looked(t *testing.T, tree source.Tree, beside ...string) *source.Looks {
	t.Helper()
	for {
		seen, err := source.Look(context.Background(), tree, beside)
		if err != nil {
			t.Fatal(err)
		}
		if seen.Settled() {
			return seen
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// watched is a repository of files a run lists and files it does not:
// one its ignore file names, one of its version control system's and
// one in a directory of a tool's.
func watched(t *testing.T) source.Tree {
	t.Helper()
	tree := repository(t)
	for name, content := range map[string]string{
		"frontseat.yaml":      "plugins: {}\n",
		"app/main.go":         "package main\n",
		"app/old.go":          "package main\n",
		"app/led.go":          "package main\n",
		source.IgnoreFile:     "/out/\n*.log\n",
		"out/built":           "built",
		"app/debug.log":       "logged",
		".git/index":          "index",
		".idea/workspace.xml": "<project/>",
	} {
		write(t, filepath.Join(tree.Root, filepath.FromSlash(name)), content)
	}
	if err := os.Symlink("led.go", filepath.Join(tree.Root, "app", "link.go")); err != nil {
		t.Fatal(err)
	}
	return tree
}

// A file written again with other content of the same size, and its
// times set back to what they were, does not look as it did: when
// anything of it last changed is the disk's own to say, and is part of
// how a file looks. It is the change a look at size and time of writing
// misses. Control: nothing written, nothing looks otherwise.
func TestAFileRewrittenAtItsSizeWithItsTimesSetBackLooksOtherwise(t *testing.T) {
	tree := watched(t)
	file := filepath.Join(tree.Root, "app", "main.go")
	before := looked(t, tree)
	was, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if again := looked(t, tree); len(again.Since(before)) != 0 {
		t.Fatalf("the control: with nothing written, %v look otherwise", again.Since(before))
	}

	write(t, file, "package MAIN\n")
	if err := os.Chtimes(file, was.ModTime(), was.ModTime()); err != nil {
		t.Fatal(err)
	}
	now, err := os.Stat(file)
	if err != nil || now.Size() != was.Size() || !now.ModTime().Equal(was.ModTime()) {
		t.Fatalf("the file is %d bytes written at %s (%v); the test wants it %d bytes written at %s", now.Size(), now.ModTime(), err, was.Size(), was.ModTime())
	}
	if changed := looked(t, tree).Since(before); !slices.Equal(changed, []string{"app/main.go"}) {
		t.Errorf("rewritten at its size with its times set back, %v look otherwise; want app/main.go", changed)
	}
}

// What a run would read otherwise looks otherwise, and is named: a file
// made, removed, moved (the name it had and the one it has), touched
// with nothing of it changed, and the repository's own frontseat.yaml.
// A file made and removed again between two looks, which a run that
// began between them may have read, is named by its directory, and a
// link led elsewhere and back by the link: nothing a run lists is as it
// was before, though every file is.
// What a run does not list does not: a file the ignore file names, one
// of the version control system's, one in a directory of a tool's.
// Each of those is the control of the others: the same write, to a file
// that is listed, is seen.
func TestWhatARunListsLooksOtherwiseWhenItChangesAndNothingElseDoes(t *testing.T) {
	at := func(tree source.Tree, name string) string {
		return filepath.Join(tree.Root, filepath.FromSlash(name))
	}
	for name, tc := range map[string]struct {
		change func(*testing.T, source.Tree)
		want   []string
	}{
		"a file made":    {func(t *testing.T, tree source.Tree) { write(t, at(tree, "app/new.go"), "package main\n") }, []string{"app/new.go"}},
		"a file removed": {func(t *testing.T, tree source.Tree) { remove(t, at(tree, "app/old.go")) }, []string{"app/old.go"}},
		"a file moved": {func(t *testing.T, tree source.Tree) {
			if err := os.Rename(at(tree, "app/old.go"), at(tree, "app/renamed.go")); err != nil {
				t.Fatal(err)
			}
		}, []string{"app/old.go", "app/renamed.go"}},
		"a file touched": {func(t *testing.T, tree source.Tree) {
			if err := os.Chtimes(at(tree, "app/main.go"), time.Time{}, time.Now().Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
		}, []string{"app/main.go"}},
		"a file made and removed again": {func(t *testing.T, tree source.Tree) {
			write(t, at(tree, "app/passing.go"), "package main\n")
			remove(t, at(tree, "app/passing.go"))
		}, []string{"app/"}},
		"a file made and removed again in the repository's own directory": {func(t *testing.T, tree source.Tree) {
			write(t, at(tree, "passing.go"), "package main\n")
			remove(t, at(tree, "passing.go"))
		}, []string{"./"}},
		"a link led elsewhere and back": {func(t *testing.T, tree source.Tree) {
			for _, to := range []string{"old.go", "led.go"} {
				remove(t, at(tree, "app/link.go"))
				if err := os.Symlink(to, at(tree, "app/link.go")); err != nil {
					t.Fatal(err)
				}
			}
		}, []string{"app/", "app/link.go"}},
		"the repository's frontseat.yaml":          {func(t *testing.T, tree source.Tree) { write(t, at(tree, "frontseat.yaml"), "plugins: {go: {}}\n") }, []string{"frontseat.yaml"}},
		"a file the ignore file names by its name": {func(t *testing.T, tree source.Tree) { write(t, at(tree, "app/debug.log"), "logged again") }, nil},
		"an ignored file":                          {func(t *testing.T, tree source.Tree) { write(t, at(tree, "out/built"), "built again") }, nil},
		"an ignored file made":                     {func(t *testing.T, tree source.Tree) { write(t, at(tree, "out/more"), "more") }, nil},
		"a file of git's":                          {func(t *testing.T, tree source.Tree) { write(t, at(tree, ".git/index"), "another index") }, nil},
		"a file of a tool's directory":             {func(t *testing.T, tree source.Tree) { remove(t, at(tree, ".idea/workspace.xml")) }, nil},
	} {
		t.Run(name, func(t *testing.T) {
			tree := watched(t)
			before := looked(t, tree)
			tc.change(t, tree)
			if changed := looked(t, tree).Since(before); !slices.Equal(changed, tc.want) {
				t.Errorf("%v look otherwise; want %v", changed, tc.want)
			}
		})
	}
}

// remove removes a file of a test's own repository.
func remove(t *testing.T, file string) {
	t.Helper()
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
}

// A file named beside the tree is looked at where it is, wherever that
// is: written again, made and removed, it looks otherwise, by the name
// it was given. A repository of a workspace of several is named with
// its files. Control: the file beside that was left alone is not named.
func TestFilesBesideATreeAndTheRepositoriesOfAWorkspaceAreNamed(t *testing.T) {
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, "one", "a.go"), "package a\n")
	write(t, filepath.Join(workspace, "two", "b.go"), "package b\n")
	tree := source.Tree{Root: workspace, Paths: map[string]string{"one": "one", "two": "two"}}
	elsewhere := t.TempDir()
	settings, plugin, absent, alone := filepath.Join(elsewhere, "build.yaml"), filepath.Join(elsewhere, "go.wasm"), filepath.Join(elsewhere, "ca.pem"), filepath.Join(elsewhere, "left alone")
	for _, file := range []string{settings, plugin, alone} {
		write(t, file, "as it was")
	}
	beside := []string{settings, plugin, absent, alone}
	before := looked(t, tree, beside...)

	write(t, settings, "as it is")
	remove(t, plugin)
	write(t, absent, "made")
	write(t, filepath.Join(workspace, "two", "b.go"), "package b // more\n")
	want := []string{absent, settings, plugin, "two/b.go"}
	slices.Sort(want)
	if changed := looked(t, tree, beside...).Since(before); !slices.Equal(changed, want) {
		t.Errorf("%v look otherwise; want %v", changed, want)
	}
}
