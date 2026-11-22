package exec_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// What a link leads to is what is read or run: a path is beneath a
// directory where it is so once every link is followed, and not where
// its spelling alone is.
func TestAPathIsWithinADirectoryOnlyWhereItsLinksLead(t *testing.T) {
	base := t.TempDir()
	directory, outside := filepath.Join(base, "brought"), filepath.Join(base, "outside")
	for _, dir := range []string{filepath.Join(directory, "bin"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(directory, "bin", "tool"), filepath.Join(outside, "tool")} {
		if err := os.WriteFile(file, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"inside": filepath.Join(directory, "bin", "tool"), "leaving": filepath.Join(outside, "tool")} {
		if err := os.Symlink(target, filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := filepath.EvalSymlinks(filepath.Join(directory, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}

	for _, at := range []string{filepath.Join(directory, "bin", "tool"), filepath.Join(directory, "inside")} {
		if led, err := exec.Within(directory, at); err != nil || led != kept {
			t.Errorf("%s is %q, %v; want %s", at, led, err, kept)
		}
	}
	if _, err := exec.Within(directory, directory); err != nil {
		t.Errorf("the directory itself: %v", err)
	}
	for _, at := range []string{filepath.Join(directory, "leaving"), filepath.Join(directory, "..", "outside", "tool")} {
		if _, err := exec.Within(directory, at); !errors.Is(err, exec.ErrOutside) {
			t.Errorf("%s was taken as within the directory: %v", at, err)
		}
	}
	if _, err := exec.Within(directory, filepath.Join(directory, "absent")); err == nil || errors.Is(err, exec.ErrOutside) {
		t.Errorf("a path that is not there: %v", err)
	}
}
