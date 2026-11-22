package source

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// later is a time long after any file of a test was written: what a
// file held is kept only once the file has settled.
func later() time.Time { return time.Now().Add(time.Hour) }

// one is a repository of one file, and where the file is.
func one(t *testing.T) (Tree, string) {
	t.Helper()
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("as it is"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Tree{Root: root, Paths: map[string]string{"": "."}}, file
}

// take is the repository as a run the keeper begins now sees it.
func take(t *testing.T, keeper *Keeper, tree Tree) *Snapshot {
	t.Helper()
	run, err := keeper.Take(context.Background(), tree)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// forgetsWhatIsGone fails the test unless a keeper that a run asked for
// the file of the repository knows of that file alone, and of nothing
// once the file is gone and the next run has begun.
func forgetsWhatIsGone(t *testing.T, tree Tree, file string) {
	t.Helper()
	keeper := &Keeper{now: later}
	run := take(t, keeper, tree)
	if _, _, _, err := run.File(file); err != nil || len(keeper.files) != 1 {
		t.Fatalf("the keeper knows of %d files, err %v; want the one", len(keeper.files), err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := keeper.Take(context.Background(), tree); err != nil {
		t.Fatal(err)
	}
	if len(keeper.files) != 0 {
		t.Errorf("the keeper knows of %d files; want none", len(keeper.files))
	}
}

// mistaken makes a keeper know of a file, as it looks, something it does
// not hold: what a change that leaves a file looking the same leaves
// behind.
func mistaken(t *testing.T, keeper *Keeper, file string) {
	t.Helper()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	// A file is known by where it is, any link on the way followed.
	at, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	keeper.keep(at, kept{looked: look(info), settled: true, held: held{hash: "what it held before", size: info.Size()}})
}

// What a daemon knows of a file may be wrong, where the file changed
// and looks the same. A run that reads the file before it goes by what
// was known goes by what it read, and so does the run after.
func TestWhatWasKnownOfAFileGivesWayToWhatIsRead(t *testing.T) {
	tree, file := one(t)
	keeper := &Keeper{now: later}
	mistaken(t, keeper, file)
	run := take(t, keeper, tree)

	content, found, err := run.Read(context.Background(), "", "file")
	if err != nil || !found || string(content) != "as it is" {
		t.Fatalf("read %q, found %v, err %v; want the file as it is", content, found, err)
	}

	next := take(t, keeper, tree)
	if hash, _, _, err := next.File(file); err != nil || hash == "what it held before" || next.Hashed() != 0 {
		t.Errorf("the next run got %s, read %d files, err %v; want what was read, known already", hash, next.Hashed(), err)
	}
}

// A run that has gone by what was known of a file, and then reads
// something else in it, is of two things at once: it fails, and the run
// after reads the file for itself.
func TestARunThatWentByWhatWasWronglyKnownFails(t *testing.T) {
	tree, file := one(t)
	keeper := &Keeper{now: later}
	mistaken(t, keeper, file)
	run := take(t, keeper, tree)
	if hash, _, _, err := run.File(file); err != nil || hash != "what it held before" {
		t.Fatalf("got %s, err %v; want the run to go by what was known", hash, err)
	}

	_, _, err := run.Read(context.Background(), "", "file")
	var changed *Changed
	if !errors.As(err, &changed) {
		t.Errorf("err %v; want the file named as changed", err)
	}

	next := take(t, keeper, tree)
	if hash, _, _, err := next.File(file); err != nil || hash == "what it held before" || next.Hashed() != 1 {
		t.Errorf("the next run got %s, read %d files, err %v; want the file read for itself", hash, next.Hashed(), err)
	}
}

// What is known of a file that is no longer there is let go, so that a
// daemon keeps no more than its workspace holds.
func TestWhatIsKnownOfAFileThatIsGoneIsLetGo(t *testing.T) {
	tree, file := one(t)
	forgetsWhatIsGone(t, tree, file)
}

// A file written again within the moment its times are told in looks as
// it did. So what a file held is not kept while the file has only just
// been written: the run after reads it again, and what was written in
// that moment is seen. Once the moment has passed it is kept.
func TestWhatAFileJustWrittenHeldIsNotKept(t *testing.T) {
	for name, c := range map[string]struct {
		since time.Duration
		kept  bool
	}{
		"just written":                {time.Millisecond, false},
		"written a moment ago":        {time.Second, true},
		"written in the time to come": {-time.Minute, false},
	} {
		t.Run(name, func(t *testing.T) {
			tree, file := one(t)
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			written := time.Unix(0, newest(look(info)))
			keeper := &Keeper{now: func() time.Time { return written.Add(c.since) }}
			run := take(t, keeper, tree)
			if _, _, _, err := run.File(file); err != nil {
				t.Fatal(err)
			}

			next := take(t, keeper, tree)
			if _, _, _, err := next.File(file); err != nil {
				t.Fatal(err)
			}

			if kept := next.Hashed() == 0; kept != c.kept {
				t.Errorf("the run after read %d files; kept %v, want %v", next.Hashed(), kept, c.kept)
			}
		})
	}
}

// Times told in whole seconds are those of a disk that tells no finer:
// a file is given two seconds to settle there.
func TestAFileOfADiskThatTellsWholeSecondsSettlesLonger(t *testing.T) {
	whole := looked{written: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC).UnixNano()}
	fine := looked{written: whole.written + 1234}
	if settles(whole) != 2*time.Second || settles(fine) != 20*time.Millisecond {
		t.Errorf("a file settles in %s where times are whole seconds and %s where they are not; want 2s and 20ms", settles(whole), settles(fine))
	}
}

// as is a file as a disk describes it.
type as struct {
	fs.FileInfo
	size int64
	mode fs.FileMode
	stat syscall.Stat_t
}

func (a as) Size() int64        { return a.size }
func (a as) Mode() fs.FileMode  { return a.mode }
func (a as) ModTime() time.Time { return time.Unix(1, 0) }
func (a as) Sys() any           { return &a.stat }

// How a file looks is more than its size and its times: it is also
// which file of the disk it is, and what may be done with it. Another
// file moved into its place, and the same file made runnable, look
// different.
func TestHowAFileLooksIsWhichFileItIsAndWhatMayBeDoneWithIt(t *testing.T) {
	file := as{size: 5, mode: 0o644, stat: syscall.Stat_t{Ino: 7}}
	another, runnable, longer := file, file, file
	another.stat.Ino = 8
	runnable.mode = 0o755
	longer.size = 6

	if look(file) != look(file) {
		t.Fatal("the control: a file does not look as itself")
	}
	for name, other := range map[string]as{"another file of the disk": another, "the file made runnable": runnable, "the file made longer": longer} {
		if look(other) == look(file) {
			t.Errorf("%s looks as the file did; want it to look different", name)
		}
	}
}
