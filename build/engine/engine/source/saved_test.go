package source_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
)

// program is the program of the daemons of these tests.
const program = "the program"

// opened is the keeper saved at a file, as a daemon of the program that
// starts opens it.
func opened(file string) *source.Keeper { return source.OpenKeeper(file, program) }

// stoppedAfterReading is a daemon that read files once and stopped,
// saving what it knew at a file it returns, with what the first of the
// files held.
func stoppedAfterReading(t *testing.T, tree source.Tree, files ...string) (string, string) {
	t.Helper()
	kept := filepath.Join(t.TempDir(), "files")
	keeper := opened(kept)
	_, was := readOnce(t, keeper, tree, files[0])
	for _, file := range files[1:] {
		readOnce(t, keeper, tree, file)
	}
	if err := keeper.Save(); err != nil {
		t.Fatal(err)
	}
	return kept, was
}

// readsOf is how many files a run of a keeper reads to answer for one
// file, and what it says the file held.
func readsOf(t *testing.T, keeper *source.Keeper, tree source.Tree, file string) (int, string) {
	t.Helper()
	run := begun(t, keeper, tree)
	hash, _, _, err := run.File(file)
	if err != nil {
		t.Fatal(err)
	}
	return run.Hashed(), hash
}

// garbled is a file that holds no keeper.
func garbled(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "garbled")
	write(t, file, "not a keeper")
	return file
}

// piped is a pipe nothing writes to, where a keeper would be.
func piped(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "piped")
	if err := syscall.Mkfifo(file, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// cutShort is a copy of a saved keeper that ends half way.
func cutShort(t *testing.T, kept string) string {
	t.Helper()
	whole, err := os.ReadFile(kept)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "cut short")
	write(t, file, string(whole[:len(whole)/2]))
	return file
}

// What a daemon knows of its files outlives it: a keeper saved when it
// stops and opened when it starts again answers a file that looks as it
// did without reading it. The controls are the keepers that know
// nothing, and fail nothing: one never saved, one whose file holds
// something else, is a directory, is a pipe or was cut short, and one
// another program saved.
func TestWhatAKeeperKnewIsKnownAfterTheDaemonStartsAgain(t *testing.T) {
	tree := repository(t)
	inside := filepath.Join(tree.Root, "inside")
	settled()
	kept, was := stoppedAfterReading(t, tree, inside)

	if read, still := readsOf(t, opened(kept), tree, inside); read != 0 || still != was {
		t.Errorf("after starting again, a run read %d files and got %s; want none read and %s", read, still, was)
	}

	for name, keeper := range map[string]*source.Keeper{
		"never saved":              opened(filepath.Join(t.TempDir(), "never")),
		"holding something else":   opened(garbled(t)),
		"a directory":              opened(t.TempDir()),
		"a pipe nothing writes to": opened(piped(t)),
		"cut short":                opened(cutShort(t, kept)),
		"saved by another program": source.OpenKeeper(kept, "another program"),
	} {
		if read, still := readsOf(t, keeper, tree, inside); read != 1 || still != was {
			t.Errorf("a keeper %s: a run read %d files and got %s; want the file read, and %s", name, read, still, was)
		}
	}
}

// A file that changed while no daemon ran is read by the daemon started
// after, however it is made to look: written again in place with what
// is as long and its time of writing set back, or replaced by another
// file as long and written at the same time, moved into its place. When
// it last changed is the disk's to say, and says it.
func TestAFileChangedWhileNoDaemonRanIsReadHoweverItIsMadeToLook(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, file string, written os.FileInfo){
		"written again in place": func(t *testing.T, file string, _ os.FileInfo) {
			write(t, file, "INSIDE")
		},
		"replaced by another moved into its place": func(t *testing.T, file string, written os.FileInfo) {
			other := filepath.Join(filepath.Dir(filepath.Dir(file)), "other")
			write(t, other, "INSIDE")
			if err := os.Chtimes(other, written.ModTime(), written.ModTime()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(other, file); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			tree := repository(t)
			inside := filepath.Join(tree.Root, "inside")
			settled()
			kept, was := stoppedAfterReading(t, tree, inside)
			written, err := os.Stat(inside)
			if err != nil {
				t.Fatal(err)
			}
			change(t, inside, written)
			if err := os.Chtimes(inside, written.ModTime(), written.ModTime()); err != nil {
				t.Fatal(err)
			}
			if after, err := os.Stat(inside); err != nil || after.Size() != written.Size() || !after.ModTime().Equal(written.ModTime()) {
				t.Fatalf("the file is %v, err %v; want one as long and written when it was", after, err)
			}
			settled()
			if read, now := readsOf(t, opened(kept), tree, inside); read != 1 || now == was {
				t.Errorf("a run read %d files and got %s; want the file read, and what it holds now", read, now)
			}
		})
	}
}

// What a keeper read of a file before the file had settled is no more
// gone by after a restart than before one: the daemon started again
// reads it.
func TestAFileReadBeforeItSettledIsReadAgainAfterARestart(t *testing.T) {
	tree := repository(t)
	inside := filepath.Join(tree.Root, "inside")
	// Read at once, before the moment its times are told in has passed.
	write(t, inside, "just written")
	kept, _ := stoppedAfterReading(t, tree, inside)
	settled()
	if read, _ := readsOf(t, opened(kept), tree, inside); read != 1 {
		t.Error("after a restart, a file read before it settled was gone by as it looked")
	}
}

// two is a directory of two repositories, each of one file, and the
// files.
func two(t *testing.T) (source.Tree, string, string) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "file"), "of a")
	write(t, filepath.Join(root, "b", "file"), "of b")
	settled()
	return source.Tree{Root: root, Paths: map[string]string{"a": "a", "b": "b"}}, filepath.Join(root, "a", "file"), filepath.Join(root, "b", "file")
}

// What a keeper knew of a repository the directory no longer holds is
// not saved: it would outlive the repository. The control is the
// repository still held, whose file is known after a restart, and both
// before either was let go.
func TestAKeeperSavesNothingOfARepositoryTheDirectoryNoLongerHolds(t *testing.T) {
	tree, a, b := two(t)
	kept, _ := stoppedAfterReading(t, tree, a, b)
	for _, file := range []string{a, b} {
		if read, _ := readsOf(t, opened(kept), tree, file); read != 0 {
			t.Fatalf("the control: %s, of a repository still held, was read again", file)
		}
	}
	// A daemon that held both, and then a alone, stops.
	keeper := opened(kept)
	begun(t, keeper, tree)
	begun(t, keeper, source.Tree{Root: tree.Root, Paths: map[string]string{"a": "a"}})
	if err := keeper.Save(); err != nil {
		t.Fatal(err)
	}
	if read, _ := readsOf(t, opened(kept), tree, a); read != 0 {
		t.Error("what was known of the repository still held was not saved")
	}
	if read, _ := readsOf(t, opened(kept), tree, b); read != 1 {
		t.Error("what was known of a repository no longer held was saved")
	}
}

// A keeper saved of one directory is not used for another, nor what it
// knew of one repository for another, though the files are the same
// files at the same place: the directory above the repository built as
// one that holds it, and the repository under another name. The control
// is the directory and the repository it was saved of.
func TestAKeeperSavedOfAnotherDirectoryOrRepositoryIsNotUsed(t *testing.T) {
	tree := repository(t)
	inside := filepath.Join(tree.Root, "inside")
	settled()
	kept, _ := stoppedAfterReading(t, tree, inside)
	for name, c := range map[string]struct {
		tree source.Tree
		read int
	}{
		"the directory and repository it was saved of": {tree, 0},
		"another directory, which holds this one":      {source.Tree{Root: filepath.Dir(tree.Root), Paths: map[string]string{"": "repo"}}, 1},
		"the repository under another name":            {source.Tree{Root: tree.Root, Paths: map[string]string{"another": "."}}, 1},
	} {
		if read, _ := readsOf(t, opened(kept), c.tree, inside); read != c.read {
			t.Errorf("%s: a run read %d files; want %d", name, read, c.read)
		}
	}
}

// A daemon that ends before any run of it began knows nothing new, and
// of no directory: it saves nothing over what the daemon before it
// saved, which the daemon after it still knows.
func TestAKeeperNoRunOfWhichBeganSavesNothingOverWhatWasSaved(t *testing.T) {
	tree := repository(t)
	inside := filepath.Join(tree.Root, "inside")
	settled()
	kept, _ := stoppedAfterReading(t, tree, inside)
	if err := opened(kept).Save(); err != nil {
		t.Fatal(err)
	}
	if read, _ := readsOf(t, opened(kept), tree, inside); read != 0 {
		t.Error("a daemon that began no run saved over what the one before it knew")
	}
}

// What a keeper saves is its user's alone to read, written whole with
// nothing left beside it; and it is not saved among the files it keeps,
// where it would be a file of the build: that is refused, and nothing
// is written there. The control is the same keeper saved in a directory
// of its own.
func TestAKeeperIsSavedForItsUserAloneAndNeverAmongTheFilesItKeeps(t *testing.T) {
	tree := repository(t)
	inside := filepath.Join(tree.Root, "inside")
	settled()
	kept, _ := stoppedAfterReading(t, tree, inside)
	info, err := os.Stat(kept)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != 0o600 {
		t.Errorf("what was saved is of mode %v; want a file of mode 0600", info.Mode())
	}
	if beside, err := os.ReadDir(filepath.Dir(kept)); err != nil || len(beside) != 1 {
		t.Errorf("%d files where the keeper was saved, err %v; want the one", len(beside), err)
	}

	among := filepath.Join(tree.Root, "files")
	keeper := opened(among)
	readOnce(t, keeper, tree, inside)
	if err := keeper.Save(); err == nil {
		t.Error("a keeper was saved among the files it keeps")
	}
	if _, err := os.Lstat(among); err == nil {
		t.Error("a refused save wrote a file among the files of the build")
	}
}
