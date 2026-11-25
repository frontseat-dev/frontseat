// Package testfiles writes the files a test is given, each with the
// directories it is in, and fails the test where one cannot be written;
// and gives a suite of tests a directory of its own.
package testfiles

import (
	"os"
	"path/filepath"
	"testing"
)

// Write writes content to the file at path, making the directories it is
// in first.
func Write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Tree writes each of files, named by its path beneath root with slashes,
// to what it holds.
func Tree(t testing.TB, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		Write(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
}

// Again are the ways a file is written again with what it holds: in
// place, as a touch or a copy over it does, and by a file of the same
// bytes moved over it, as an editor or an install does. Either leaves
// it holding what it held, and looking otherwise.
var Again = map[string]func(t testing.TB, path string){
	"in place": func(t testing.TB, path string) {
		t.Helper()
		Write(t, path, held(t, path))
	},
	"moved over it": func(t testing.TB, path string) {
		t.Helper()
		moved := path + ".new"
		Write(t, moved, held(t, path))
		if err := os.Rename(moved, path); err != nil {
			t.Fatal(err)
		}
	},
}

// held is what a file holds.
func held(t testing.TB, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
