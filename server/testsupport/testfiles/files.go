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
