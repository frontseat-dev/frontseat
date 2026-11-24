package machine

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/common/record/manifest"
)

// owned is a file as a file system says it is a user's.
type owned struct{ uid uint32 }

func (owned) Name() string       { return "directory" }
func (owned) Size() int64        { return 0 }
func (owned) Mode() fs.FileMode  { return fs.ModeDir | 0o777 }
func (owned) ModTime() time.Time { return time.Time{} }
func (owned) IsDir() bool        { return true }
func (o owned) Sys() any         { return &syscall.Stat_t{Uid: o.uid} }

// unowned is a file of a system that gives a file no owner.
type unowned struct{ owned }

func (unowned) Sys() any { return nil }

// A directory is a user's by its owner's number, whatever its mode, as
// git holds a repository to its owner: another's is not, root's among
// them. Where a system gives a file no owner, or a process no user,
// there is nothing to hold it to. The control is the user's own.
func TestADirectoryIsAUsersByItsOwner(t *testing.T) {
	if !ownedBy(owned{uid: 501}, 501) {
		t.Fatal("the control: a directory a user owns is not theirs")
	}
	for name, another := range map[string]uint32{"another user's": 502, "root's": 0} {
		if ownedBy(owned{uid: another}, 501) {
			t.Errorf("a directory that is %s is taken for the user's own", name)
		}
	}
	if !ownedBy(unowned{}, 501) || !ownedBy(owned{uid: 502}, -1) {
		t.Error("where a file has no owner, or a process no user, a directory is not taken")
	}
	here, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !mine(here) {
		t.Error("a directory this test made is not this user's")
	}
}

// marks lays out a directory beneath a root, with a marker in it: a
// workspace's manifest or a repository's file.
func marks(t *testing.T, directory, marker string) {
	t.Helper()
	file := filepath.Join(directory, filepath.FromSlash(marker))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A marker in a directory that is another user's is not taken: whoever
// writes a directory others work beneath, as /tmp, would otherwise say
// what a command run there builds, and with which plugins. It is passed
// over, for the user's own above it, and where that leaves nothing the
// command says which directory was marked and not taken. The control is
// the same directories all the user's own, where the nearest is built.
func TestAMarkerInAnotherUsersDirectoryIsPassedOver(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "own", "shared")
	here := filepath.Join(shared, "checkout", "deep")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	marks(t, shared, manifest.File)
	t.Chdir(here)
	if got, err := Directory(false); err != nil || got != shared {
		t.Fatalf("the control: with every directory the user's own, the directory built is %q (%v); want %s", got, err, shared)
	}
	real := mine
	t.Cleanup(func() { mine = real })
	mine = func(directory fs.FileInfo) bool {
		theirs, err := os.Stat(shared)
		return err != nil || !os.SameFile(directory, theirs)
	}
	for _, inside := range []bool{false, true} {
		if got, err := Directory(inside); err == nil || !strings.Contains(err.Error(), shared) || !strings.Contains(err.Error(), "another user's") {
			t.Errorf("inside: %t, beneath a marker in another user's directory: %q, %v; want nothing built, naming %s as not taken", inside, got, err, shared)
		}
	}
	marks(t, filepath.Join(root, "own"), repositoryFile)
	if got, err := Directory(false); err != nil || got != filepath.Join(root, "own") {
		t.Errorf("with the user's own repository above it, the directory built is %q (%v); want that one, %s", got, err, filepath.Join(root, "own"))
	}
}

// The nearest marker above is the directory built, a workspace's or a
// repository's: a repository within a workspace is built from within
// it, and a workspace's manifest far above does not take every checkout
// beneath it. What cannot be asked of a directory above is no marker
// there, and fails nothing: a file where the manifest's directory would
// be, and a directory that may not be entered.
func TestTheNearestMarkerAboveIsTheDirectoryBuilt(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(root, "work", "payments")
	here := filepath.Join(repository, "deep")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)
	marks(t, root, manifest.File)
	if got, err := Directory(false); err != nil || got != root {
		t.Fatalf("the control: with a workspace alone above, the directory built is %q (%v); want %s", got, err, root)
	}
	marks(t, repository, repositoryFile)
	if got, err := Directory(false); err != nil || got != repository {
		t.Errorf("with a repository nearer than the workspace, the directory built is %q (%v); want the nearest, %s", got, err, repository)
	}
	// A file named as the manifest's directory is, between the two.
	if err := os.WriteFile(filepath.Join(root, "work", filepath.Dir(filepath.FromSlash(manifest.File))), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repository, repositoryFile)); err != nil {
		t.Fatal(err)
	}
	for _, inside := range []bool{false, true} {
		if got, err := Directory(inside); err != nil || got != root {
			t.Errorf("inside: %t, beneath a file named as the manifest's directory is, the directory built is %q (%v); want the workspace above, %s", inside, got, err, root)
		}
	}
	// A directory above that may not be entered.
	if os.Getuid() == 0 {
		return
	}
	if err := os.Chmod(filepath.Join(root, "work"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "work"), 0o755) })
	if got, err := Directory(false); err != nil || got != root {
		t.Errorf("beneath a directory that may not be entered, the directory built is %q (%v); want the workspace above, %s", got, err, root)
	}
}
