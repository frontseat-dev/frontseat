package source

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// dating is a file system that dates a file's changes itself, exfat one
// that is known not to, and unnamed one the system does not name.
func dating(string) (string, bool)  { return "apfs", true }
func exfat(string) (string, bool)   { return "exfat", false }
func unnamed(string) (string, bool) { return "", false }

// What a daemon saved is used by the daemon after it only for a
// repository on a file system known to date a file's changes itself,
// and is saved only of one: on any other, known not to or not known,
// the daemon started again reads the file, whichever of the two daemons
// found it so, and a daemon that ends on one saves nothing. The control
// is the file system that dates changes itself, where the daemon
// started again reads nothing.
func TestWhatADaemonKnewIsKeptOnlyWhereTheFileSystemDatesAFilesChangesItself(t *testing.T) {
	for name, c := range map[string]struct {
		ended, started func(string) (string, bool)
		saved          bool
		read           int
	}{
		"dating changes itself for both":             {dating, dating, true, 0},
		"known not to, for both":                     {exfat, exfat, false, 1},
		"not named by the system, for both":          {unnamed, unnamed, false, 1},
		"known not to, for the daemon that ended":    {exfat, dating, false, 1},
		"known not to, for the daemon started again": {dating, exfat, true, 1},
		"not named, for the daemon started again":    {dating, unnamed, true, 1},
	} {
		t.Run(name, func(t *testing.T) {
			tree, file := one(t)
			at := stopped(t, tree, c.ended, file)
			if _, err := os.Stat(at); (err == nil) != c.saved {
				t.Errorf("the daemon that ended saved a file: %t (%v); want %t", err == nil, err, c.saved)
			}
			if read := startedAgain(t, at, tree, c.started, file); read != c.read {
				t.Errorf("the daemon started again read %d files; want %d", read, c.read)
			}
		})
	}
}

// A daemon that ends with every repository on a file system it does not
// trust writes nothing, and leaves what a daemon before it saved as it
// was: it is not this daemon's to remove, and the daemon that reads it
// next uses it only where it trusts the file system then, of a file
// that looks as it did, as if no daemon had run between. The control is
// the same daemon on a file system it trusts, which saves over it.
func TestWhatWasSavedBeforeIsLeftAsItWasByADaemonThatSavesNothing(t *testing.T) {
	for name, c := range map[string]struct {
		on   func(string) (string, bool)
		left bool
	}{
		"on a file system known not to date changes": {exfat, true},
		"on one that does":                           {dating, false},
	} {
		t.Run(name, func(t *testing.T) {
			tree, file := one(t)
			at := stopped(t, tree, dating, file)
			before, err := os.ReadFile(at)
			if err != nil {
				t.Fatal(err)
			}
			next := OpenKeeper(at, "")
			next.fileSystem = c.on
			reads(t, next, tree, []string{file})
			if err := next.Save(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(at)
			if err != nil {
				t.Fatal(err)
			}
			if left := bytes.Equal(before, after); left != c.left {
				t.Errorf("what was saved before was left as it was: %t; want %t", left, c.left)
			}
			if read := startedAgain(t, at, tree, dating, file); read != 0 {
				t.Errorf("a daemon started after it, where the file system is trusted and the file as it was, read %d files; want 0", read)
			}
		})
	}
}

// Of two repositories, one on a file system that dates a file's changes
// itself and one on a file system that does not, what was known of the
// first is saved and used, and nothing of the second: its file is read
// by the daemon started again, and is not in what was saved.
func TestOfTwoRepositoriesOnlyTheOneWhoseFileSystemIsTrustedIsSaved(t *testing.T) {
	root := t.TempDir()
	var files []string
	for _, repository := range []string{"dated", "undated"} {
		file := filepath.Join(root, repository, "file")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("as it is"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	tree := Tree{Root: root, Paths: map[string]string{"dated": "dated", "undated": "undated"}}
	on := func(directory string) (string, bool) {
		if filepath.Base(directory) == "undated" {
			return exfat(directory)
		}
		return dating(directory)
	}
	at := stopped(t, tree, on, files...)
	rewritten(t, at, func(s *saved) {
		if _, kept := s.Repositories["undated"]; kept || len(s.Repositories["dated"].Files) != 1 {
			t.Errorf("saved %v; want the one file of the repository whose file system is trusted, and nothing of the other", s.Repositories)
		}
	})
	if read := startedAgain(t, at, tree, on, files...); read != 1 {
		t.Errorf("the daemon started again read %d files; want 1, the file of the repository whose file system is not trusted", read)
	}
}

// A keeper says once, of each repository on a file system it does not
// trust, that what it knows of it is not kept, with where it is and the
// file system's name. The controls are the repository on a file system
// it trusts, of which nothing is said, and the keeper that is saved
// nowhere, which keeps nothing of any and asks of none.
func TestAKeeperSaysOnceWhichRepositoriesItDoesNotKeep(t *testing.T) {
	tree, _ := one(t)
	directory, err := filepath.EvalSymlinks(tree.Root)
	if err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(t.TempDir(), "files")

	keeper := OpenKeeper(saved, "")
	keeper.fileSystem = exfat
	if said := keeper.Unkept(); said != nil {
		t.Errorf("before any run began the keeper said %v; want nothing", said)
	}
	take(t, keeper, tree)
	said := keeper.Unkept()
	if len(said) != 1 || said[0] != (Unkept{Directory: directory, FileSystem: "exfat"}) {
		t.Fatalf("after its first run the keeper said %v; want %s on exfat", said, directory)
	}
	for _, word := range []string{directory, "(exfat)", "not kept across restarts"} {
		if !strings.Contains(said[0].String(), word) {
			t.Errorf("it is said as %q; want %q in it", said[0], word)
		}
	}
	take(t, keeper, tree)
	if again := keeper.Unkept(); again != nil {
		t.Errorf("after its second run the keeper said %v again; want it said once", again)
	}
	if unnamed := (Unkept{Directory: directory}).String(); strings.Contains(unnamed, "()") {
		t.Errorf("a file system with no name is said as %q; want it said that it has none", unnamed)
	}

	trusting := OpenKeeper(saved, "")
	trusting.fileSystem = dating
	take(t, trusting, tree)
	if said := trusting.Unkept(); said != nil {
		t.Errorf("of a repository whose file system it trusts the keeper said %v; want nothing", said)
	}

	unsaved := &Keeper{fileSystem: func(string) (string, bool) {
		t.Error("a keeper that is saved nowhere asked what file system a repository is on")
		return exfat("")
	}}
	take(t, unsaved, tree)
	if said := unsaved.Unkept(); said != nil {
		t.Errorf("a keeper that is saved nowhere said %v; want nothing", said)
	}
}

// The file system of this machine's temporary directory has a name, as
// the system says it, and on macOS it is APFS, which is trusted. The
// control is a directory that is not there, which has none and is not
// trusted.
func TestTheFileSystemOfADirectoryIsWhatTheSystemSays(t *testing.T) {
	name, dates := fileSystemOf(t.TempDir())
	if name == "" {
		t.Error("the temporary directory's file system has no name; want the system's name for it")
	}
	if runtime.GOOS == "darwin" && (name != "apfs" || !dates) {
		t.Errorf("the temporary directory is on %q, trusted: %t; want apfs, trusted", name, dates)
	}
	if name, dates := fileSystemOf(filepath.Join(t.TempDir(), "gone")); name != "" || dates {
		t.Errorf("a directory that is not there is on %q, trusted: %t; want no name, not trusted", name, dates)
	}
}

// Only the file systems known to date a file's changes themselves are
// trusted, each by what the system says of it: a name on macOS, a
// number on Linux. Every other is not, the ones known by name and any
// number or name that is not known.
func TestOnlyTheFileSystemsKnownToDateChangesThemselvesAreTrusted(t *testing.T) {
	for magic, want := range map[uint32]struct {
		name  string
		dates bool
	}{
		0xef53:     {"ext4", true},
		0x58465342: {"xfs", true},
		0x9123683e: {"btrfs", true},
		0x01021994: {"tmpfs", true},
		0xf2f52010: {"f2fs", true},
		0xca451a4e: {"bcachefs", true},
		0x2fc12fc1: {"zfs", true},
		0x794c7630: {"overlayfs", true},
		0x4d44:     {"msdos", false},
		0x2011bab0: {"exfat", false},
		0x65735546: {"fuse", false},
		0x6969:     {"nfs", false},
		0xff534d42: {"cifs", false},
		0xfe534d42: {"smb2", false},
		0x01021997: {"9p", false},
		0x5346544e: {"ntfs", false},
		0x7366746e: {"ntfs3", false},
		0x12345678: {"0x12345678", false},
		0:          {"0x0", false},
	} {
		if name, dates := linuxFileSystem(magic); name != want.name || dates != want.dates {
			t.Errorf("Linux's %#x is %q, trusted: %t; want %q, %t", magic, name, dates, want.name, want.dates)
		}
	}
	if len(linuxDates) != 8 {
		t.Errorf("%d file systems of Linux are trusted; want the 8 this test names", len(linuxDates))
	}
	for name, want := range map[string]bool{
		"apfs": true, "hfs": true,
		"exfat": false, "msdos": false, "nfs": false, "smbfs": false, "webdav": false, "macfuse": false, "virtiofs": false, "APFS": false, "": false,
	} {
		if darwinDates[name] != want {
			t.Errorf("macOS's %q is trusted: %t; want %t", name, darwinDates[name], want)
		}
	}
	if len(darwinDates) != 2 {
		t.Errorf("%d file systems of macOS are trusted; want the 2 this test names", len(darwinDates))
	}
}
