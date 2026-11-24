package machine

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// holding holds a lock alone, as a sandbox's supervisor holds its own,
// and returns what lets go of it.
func holding(t *testing.T, lock string) (release func()) {
	t.Helper()
	file, err := os.OpenFile(lock, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		t.Fatalf("the lock %s is held by another, or what asked after it did not let go of it: %v", lock, err)
	}
	return func() { _ = file.Close() }
}

// The command line is inside a workspace's sandbox only where the lock
// names a workspace and a supervisor holds it now. A lock an earlier
// program left behind, which names a workspace and which no one holds,
// is outside, and so is it again once its supervisor has let go; so are
// a lock held that names none, one that cannot be read, and none. The
// control is the same lock, held: inside. Asking leaves the lock to
// whoever would hold it.
func TestOnlyALockThatNamesAWorkspaceAndIsHeldIsInside(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	left := write("left", "payments\n", 0o600)
	blank := write("blank", "\n", 0o600)
	defer holding(t, blank)()
	for what, lock := range map[string]string{
		"none":                          filepath.Join(dir, "none"),
		"one left behind, held by none": left,
		"one held that names none":      blank,
		"one that cannot be read":       write("unreadable", "payments\n", 0o000),
	} {
		if os.Geteuid() == 0 && what == "one that cannot be read" {
			continue
		}
		if supervised(lock) {
			t.Errorf("%s was taken for inside a workspace's sandbox", what)
		}
	}
	// The control: the lock that was left behind, held now. Taking it
	// shows too that asking after it held nothing of it.
	release := holding(t, left)
	if !supervised(left) {
		t.Error("a lock that names a workspace and is held was taken for outside")
	}
	release()
	if supervised(left) {
		t.Error("a lock its supervisor let go of was taken for inside")
	}
	holding(t, left)()
}
