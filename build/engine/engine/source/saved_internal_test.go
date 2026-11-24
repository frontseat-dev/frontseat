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
			time.Sleep(30 * time.Millisecond)
			at := filepath.Join(t.TempDir(), "files")
			first := OpenKeeper(at, "")
			if _, _, _, err := take(t, first, tree).File(file); err != nil {
				t.Fatal(err)
			}
			if err := first.Save(); err != nil {
				t.Fatal(err)
			}
			rewritten(t, at, c.change)

			run := take(t, OpenKeeper(at, ""), tree)
			if _, _, _, err := run.File(file); err != nil || run.Hashed() != c.read {
				t.Errorf("the daemon started again read %d files, err %v; want %d", run.Hashed(), err, c.read)
			}
		})
	}
}
