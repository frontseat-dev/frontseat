package source

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rewritten saves again what a keeper saved at a file, changed.
func rewritten(t *testing.T, file string, change func(*saved)) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var s saved
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&s); err != nil {
		t.Fatal(err)
	}
	change(&s)
	var again bytes.Buffer
	if err := gob.NewEncoder(&again).Encode(s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, again.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// reads is how many files a run of a keeper reads to answer for files.
func reads(t *testing.T, keeper *Keeper, tree Tree, files []string) int {
	t.Helper()
	run := take(t, keeper, tree)
	for _, file := range files {
		if _, _, _, err := run.File(file); err != nil {
			t.Fatal(err)
		}
	}
	return run.Hashed()
}

// stopped is a daemon that read files once they had settled and
// stopped, on a file system that is as it is said, the machine's own
// where nothing is said, and the file it saved what it knew at.
func stopped(t *testing.T, tree Tree, on func(string) (string, bool), files ...string) string {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	at := filepath.Join(t.TempDir(), "files")
	first := OpenKeeper(at, "")
	first.fileSystem = on
	reads(t, first, tree, files)
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}
	return at
}

// startedAgain is how many files the first run of a daemon started
// where one saved what it knew reads to answer for files, on a file
// system that is as it is said.
func startedAgain(t *testing.T, at string, tree Tree, on func(string) (string, bool), files ...string) int {
	t.Helper()
	next := OpenKeeper(at, "")
	next.fileSystem = on
	return reads(t, next, tree, files)
}

// What a daemon saved is used by the one after it only as far as it
// could be that daemon's own knowledge: of this format, of the same
// directory of the same disk, saved by a clock this daemon's is not
// behind, and of a file only where the file had settled by when it was
// saved. Each of these otherwise, the file is read. The control is what
// was saved, written again as it was, by which the file is not read.
func TestWhatWasSavedIsUsedOnlyAsFarAsItCouldBeThisDaemonsOwn(t *testing.T) {
	only := func(s *saved) savedRepository { return s.Repositories[""] }
	for name, c := range map[string]struct {
		change func(*saved)
		read   int
	}{
		"as it was saved":   {func(*saved) {}, 0},
		"of another format": {func(s *saved) { s.Format++ }, 1},
		"of another directory of the disk at the same place": {func(s *saved) {
			of := only(s)
			of.Place.File++
			s.Repositories[""] = of
		}, 1},
		"of another disk at the same place": {func(s *saved) {
			of := only(s)
			of.Place.Device++
			s.Repositories[""] = of
		}, 1},
		"saved at a time still to come, the clock having gone back": {func(s *saved) { s.Saved = time.Now().Add(time.Hour).UnixNano() }, 1},
		"saved before its file had settled": {func(s *saved) {
			s.Saved = only(s).Files["file"].Changed + int64(time.Millisecond)
		}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			tree, file := one(t)
			at := stopped(t, tree, nil, file)
			rewritten(t, at, c.change)

			if read := startedAgain(t, at, tree, nil, file); read != c.read {
				t.Errorf("the daemon started again read %d files; want %d", read, c.read)
			}
		})
	}
}
