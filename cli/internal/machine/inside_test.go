package machine

import (
	"os"
	"path/filepath"
	"testing"
)

// The command line is inside a workspace's sandbox only where the lock
// names a workspace: a lock a daemon told no workspace left behind, an
// empty one, one that cannot be read, or none, is outside. The control is
// a lock that names one.
func TestOnlyALockThatNamesAWorkspaceIsInside(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for what, lock := range map[string]string{
		"none":             filepath.Join(dir, "none"),
		"a daemon of none": write("blank", "\n", 0o600),
		"unreadable":       write("unreadable", "payments\n", 0o000),
	} {
		if os.Geteuid() == 0 && what == "unreadable" {
			continue
		}
		if named(lock) {
			t.Errorf("%s was taken for inside a workspace's sandbox", what)
		}
	}
	if !named(write("named", "payments\n", 0o600)) {
		t.Error("a lock that names a workspace was taken for outside")
	}
}
