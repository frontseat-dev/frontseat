package source

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// lie is a SHA-256 no file of a test holds.
var lie = strings.Repeat("ab", 32)

// truly is the SHA-256 of what the file of one holds.
func truly() string {
	sum := sha256.Sum256([]byte("as it is"))
	return hex.EncodeToString(sum[:])
}

// saying is a keeper that is told a file's attribute says a hash, and
// that the file looks as it did whenever it is asked afresh.
func saying(hash string) *Keeper {
	return &Keeper{now: later, attribute: func(*os.File) (string, bool) { return hash, hash != "" }, unchanged: func(*os.File, fs.FileInfo) bool { return true }}
}

// mountedAt is the repository of one, mounted, its mount's stamp the
// change time its file has by so many nanoseconds.
func mountedAt(t *testing.T, tree Tree, file string, by int64) Tree {
	t.Helper()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	_, changed := identity(info)
	tree.Mounted = map[string]Mount{"": {Stamp: changed + by, Stamped: true}}
	return tree
}

// asked is what a run of a keeper says the file holds, and how many
// files it read to say so.
func asked(t *testing.T, keeper *Keeper, tree Tree, file string) (string, int) {
	t.Helper()
	run := take(t, keeper, tree)
	hash, _, _, err := run.File(file)
	if err != nil {
		t.Fatal(err)
	}
	return hash, run.Hashed()
}

// A file of a mounted repository that is shown at its mount's stamp
// holds what its attribute says, and is not read: here the attribute
// says another hash than the file's, which the run takes. The controls
// are each read, and found to hold what they hold: the same file where
// its change time is not the stamp, where the layer said no stamp,
// where the repository is not mounted, where the file says no
// attribute, and where the repository is a clone that has the
// attribute.
func TestAFileShownAtItsMountsStampHoldsWhatItsAttributeSays(t *testing.T) {
	plain, file := one(t)
	stamped := mountedAt(t, plain, file, 0)
	if hash, read := asked(t, saying(lie), stamped, file); hash != lie || read != 0 {
		t.Fatalf("at the stamp, the run read %d files and got %s; want none read and what the attribute says", read, hash)
	}
	unstamped := plain
	unstamped.Mounted = map[string]Mount{"": {}}
	// A layer that said no stamp says none at the first moment either,
	// which is what a file system that keeps no change time answers.
	if (&Snapshot{Tree: unstamped}).stamped("", looked{}) {
		t.Error("a file with no change time is taken to be at the stamp of a layer that said none")
	}
	another := plain
	another.Mounted = map[string]Mount{"other": stamped.Mounted[""]}
	for name, control := range map[string]struct {
		tree Tree
		says string
	}{
		"a change time a nanosecond after the stamp":  {mountedAt(t, plain, file, -1), lie},
		"a change time a nanosecond before the stamp": {mountedAt(t, plain, file, 1), lie},
		"a layer that said no stamp":                  {unstamped, lie},
		"a clone":                                     {plain, lie},
		"another repository mounted":                  {another, lie},
		"a file that says no attribute":               {stamped, ""},
	} {
		if hash, read := asked(t, saying(control.says), control.tree, file); hash != truly() || read != 1 {
			t.Errorf("%s: the run read %d files and got %s; want the file read, and what it holds", name, read, hash)
		}
	}
}

// The hash, the size and the mode of a file are three answers, and a
// mount may come to give another file between them. So the file is
// asked how it looks, afresh, before its attribute is asked and after,
// and where it does not look as the run holds it to, either time, it is
// read. The control is the file that looks the same both times, which
// is not read.
func TestAFileThatDoesNotLookAsItDidOnceItsAttributeIsAskedIsRead(t *testing.T) {
	plain, file := one(t)
	stamped := mountedAt(t, plain, file, 0)
	for name, looks := range map[string][]bool{
		"it looks otherwise before": {false, true},
		"it looks otherwise after":  {true, false},
	} {
		keeper, looked, attributeAsked := saying(lie), 0, 0
		keeper.unchanged = func(*os.File, fs.FileInfo) bool { looked++; return looks[looked-1] }
		keeper.attribute = func(*os.File) (string, bool) { attributeAsked = looked; return lie, true }
		if hash, read := asked(t, keeper, stamped, file); hash != truly() || read != 1 {
			t.Errorf("%s: the run read %d files and got %s; want the file read, and what it holds", name, read, hash)
		}
		if looks[0] && attributeAsked != 1 {
			t.Errorf("%s: the attribute was asked after %d looks; want it asked between the two", name, attributeAsked)
		}
	}
	keeper, looked := saying(lie), 0
	keeper.unchanged = func(*os.File, fs.FileInfo) bool { looked++; return true }
	if hash, read := asked(t, keeper, stamped, file); hash != lie || read != 0 || looked != 2 {
		t.Errorf("the control: the run read %d files, looked %d times and got %s; want none read, two looks and what the attribute says", read, looked, hash)
	}
}

// Every file a mount gave looks alike in its times, so how one looks
// says nothing of whether it is the file a run before saw. Nothing a
// mount said is kept for the run after, which asks it again and goes by
// what it says then; nothing read of a file shown at the stamp is
// either; and what was read of the file while its layer said no stamp
// is not gone by once it says one. The control is the clone, whose file
// the second run does not read, by how it looks.
func TestNothingOfAFileShownAtItsMountsStampIsKeptForTheRunAfter(t *testing.T) {
	plain, file := one(t)
	stamped := mountedAt(t, plain, file, 0)
	other := strings.Repeat("cd", 32)

	keeper := saying(lie)
	if hash, _ := asked(t, keeper, stamped, file); hash != lie {
		t.Fatalf("the first run got %s; want what the attribute said", hash)
	}
	keeper.attribute = func(*os.File) (string, bool) { return other, true }
	if hash, read := asked(t, keeper, stamped, file); hash != other || read != 0 || len(keeper.files) != 0 {
		t.Errorf("the second run got %s, read %d files and the keeper knows %d; want what the attribute says now, and nothing kept", hash, read, len(keeper.files))
	}

	reading := saying("")
	for range 2 {
		if hash, read := asked(t, reading, stamped, file); hash != truly() || read != 1 || len(reading.files) != 0 {
			t.Errorf("a file at the stamp that says no attribute: got %s, read %d files and the keeper knows %d; want it read each run and nothing kept", hash, read, len(reading.files))
		}
	}

	moved := saying(lie)
	unstamped := plain
	unstamped.Mounted = map[string]Mount{"": {}}
	if hash, read := asked(t, moved, unstamped, file); hash != truly() || read != 1 || len(moved.files) != 1 {
		t.Fatalf("with no stamp said, the run got %s, read %d files and the keeper knows %d; want the file read and known by how it looks", hash, read, len(moved.files))
	}
	if hash, read := asked(t, moved, stamped, file); hash != lie || read != 0 {
		t.Errorf("once a stamp is said, the run got %s and read %d files; want what the mount says now, and not what was read before", hash, read)
	}

	clone := saying(lie)
	asked(t, clone, plain, file)
	if hash, read := asked(t, clone, plain, file); hash != truly() || read != 0 {
		t.Errorf("the control, a clone: the second run got %s and read %d files; want what it holds, known by how it looks", hash, read)
	}
}

// Nothing of a repository something else mounted is saved for the
// daemon after this one, and nothing saved is used of one, on whatever
// file system it is said to be: how its files look is the mount's to
// say. The daemon says so once. The control is the clone beside it, on
// the same file system, whose file is saved and not read again.
func TestNothingOfAMountedRepositoryIsSavedOrUsedByTheDaemonAfter(t *testing.T) {
	tree, files := repositories(t, "cloned", "mounted")
	tree.Mounted = map[string]Mount{"mounted": {}}
	at := stopped(t, tree, dating, files...)
	rewritten(t, at, func(s *saved) {
		if _, kept := s.Repositories["mounted"]; kept || len(s.Repositories["cloned"].Files) != 1 {
			t.Errorf("saved %v; want the one file of the clone, and nothing of the mounted repository", s.Repositories)
		}
	})
	if read := startedAgain(t, at, tree, dating, files...); read != 1 {
		t.Errorf("the daemon started again read %d files; want 1, the file of the mounted repository", read)
	}

	// What a daemon saved of the repository while it was a clone is not
	// used once it is mounted.
	cloned := tree
	cloned.Mounted = nil
	was := stopped(t, cloned, dating, files...)
	if read := startedAgain(t, was, tree, dating, files...); read != 1 {
		t.Errorf("of a repository saved as a clone and mounted since, the daemon started again read %d files; want 1, its file", read)
	}
	if read := startedAgain(t, was, cloned, dating, files...); read != 0 {
		t.Errorf("the control, both still clones: the daemon started again read %d files; want none", read)
	}

	keeper := OpenKeeper(filepath.Join(t.TempDir(), "files"), "")
	keeper.fileSystem = dating
	take(t, keeper, tree)
	said := keeper.Unkept()
	if len(said) != 1 || !said[0].Mounted || filepath.Base(said[0].Directory) != "mounted" || !strings.Contains(said[0].String(), "is mounted") {
		t.Errorf("the keeper said %v; want the mounted repository, said as mounted, and nothing of the clone", said)
	}
}

// A layer says a stamp only where it is on a file system mounted
// read-only, which nothing in a sandbox can make, and holds a file
// named stamp that is one moment as the layout writes it: RFC 3339 with
// nanoseconds, in UTC, on one line. Any other says none, and the
// repository is read. The controls are the same stamp in a layer that
// is not read-only, and this machine's own answer for a directory a
// test may write, which is not read-only.
func TestALayerSaysAStampOnlyWhereItIsReadOnlyAndWrittenAsTheLayoutSays(t *testing.T) {
	always := func(string) bool { return true }
	for written, want := range map[string]Mount{
		"2026-11-24T10:00:00.123456789Z\n":       {Stamp: 1795514400123456789, Stamped: true},
		"2026-11-24T10:00:00Z\n":                 {Stamp: 1795514400000000000, Stamped: true},
		"2026-11-24T10:00:00.123456789Z":         {},
		"2026-11-24T11:00:00.123456789+01:00\n":  {},
		"2026-11-24T10:00:00.123456789Z\n\n":     {},
		"2026-11-24T10:00:00.123456789Z\nmore\n": {},
		"1795514400123456789\n":                  {},
		"":                                       {},
	} {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "stamp"), []byte(written), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := layer(directory, always); got != want {
			t.Errorf("a layer whose stamp is written %q says %+v; want %+v", written, got, want)
		}
		if got := layer(directory, func(string) bool { return false }); got != (Mount{}) {
			t.Errorf("a layer that is not read-only, whose stamp is written %q, says %+v; want no stamp", written, got)
		}
		if got := Layer(directory); got != (Mount{}) {
			t.Errorf("a layer in a directory a test may write says %+v; want no stamp, since it is not mounted read-only", got)
		}
	}
	absent := t.TempDir()
	if got := layer(absent, always); got != (Mount{}) {
		t.Errorf("a layer with no stamp says %+v; want none", got)
	}
	if err := os.Mkdir(filepath.Join(absent, "stamp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := layer(absent, always); got != (Mount{}) {
		t.Errorf("a layer whose stamp is a directory says %+v; want none", got)
	}
	if got := layer(filepath.Join(absent, "nowhere"), always); got != (Mount{}) {
		t.Errorf("a layer that is not there says %+v; want none", got)
	}
}

// A file's hash attribute is read as the system keeps it, and taken
// only where it is a SHA-256 as the layout writes one: 64 hexadecimal
// digits in lower case. A file asked afresh how it looks, by the system
// itself, looks as it did until it is written. It runs where the file
// system of the test's directory keeps a user's extended attributes.
func TestAFilesHashAttributeIsReadAsTheSystemKeepsIt(t *testing.T) {
	_, at := one(t)
	file, err := os.Open(at)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if said, ok := attributed(file); ok {
		t.Fatalf("a file with no attribute says %q", said)
	}
	if err := unix.Setxattr(at, HashAttribute, []byte(lie), 0); errors.Is(err, unix.ENOTSUP) {
		t.Skipf("the file system of %s keeps no extended attribute of a user's: %v", at, err)
	} else if err != nil {
		t.Fatal(err)
	}
	if said, ok := attributed(file); !ok || said != lie {
		t.Errorf("the attribute is read as %q (%v); want %s", said, ok, lie)
	}
	for name, written := range map[string]string{
		"in upper case":  strings.ToUpper(lie),
		"too short":      lie[:63],
		"too long":       lie + "a",
		"not a hash":     strings.Repeat("zz", 32),
		"with a newline": lie[:63] + "\n",
		"empty":          "",
	} {
		if err := unix.Setxattr(at, HashAttribute, []byte(written), 0); err != nil {
			t.Fatal(err)
		}
		if said, ok := attributed(file); ok {
			t.Errorf("an attribute %s is taken, as %q", name, said)
		}
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !looksStill(file, info) {
		t.Error("a file nothing changed since it was looked at does not look as it did")
	}
	if err := os.WriteFile(at, []byte("as it is, and more"), 0o644); err != nil {
		t.Fatal(err)
	}
	if looksStill(file, info) {
		t.Error("a file written since it was looked at looks as it did")
	}
}
