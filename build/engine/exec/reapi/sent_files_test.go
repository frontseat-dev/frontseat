package reapi_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
)

// a file that changed to content of the same size before it was
// sent, small (sent in a batch) and large (sent as a stream): the grid
// is given the name of what the run found and must take nothing else
// under it.
func TestAFileOfTheSameSizeIsNotSentAsSomethingElse(t *testing.T) {
	grid := dial(t)
	for name, size := range map[string]int{"small": 64, "large": 6 << 20} {
		t.Run(name, func(t *testing.T) {
			unique := strconv.FormatInt(time.Now().UnixNano(), 10)
			was := unique + " was " + strings.Repeat("a", size)
			is := unique + " is! " + strings.Repeat("b", size)
			dir := root(t, map[string]string{"greeting.txt": is})
			action := reading(dir, []string{"greeting.txt"}, said{filepath.Join(dir, "greeting.txt"): was}, "head -c 40 greeting.txt")
			done, err := grid.Execute(context.Background(), action)
			if err == nil {
				t.Fatalf("printed %q and no error; want the action not run", done.Stdout)
			}
			t.Logf("refused: %.300s", err.Error())
			// And nothing is kept under the name: the file as the run found
			// it, sent next, is what the action reads.
			if err := os.WriteFile(filepath.Join(dir, "greeting.txt"), []byte(was), 0o644); err != nil {
				t.Fatal(err)
			}
			done, err = grid.Execute(context.Background(), action)
			if err != nil || !bytes.HasPrefix([]byte(was), done.Stdout) || len(done.Stdout) != 40 {
				t.Errorf("printed %q, err %v; want the start of what the run found", done.Stdout, err)
			}
		})
	}
}

// changing is a run's files, one of which its author writes just after
// the run has said what it held: before the action is sent.
type changing struct {
	*source.Snapshot
	file string
	once sync.Once
	how  func(file string) error
	err  error
}

func (c *changing) File(path string) (string, int64, bool, error) {
	hash, size, executable, err := c.Snapshot.File(path)
	if path == c.file {
		c.once.Do(func() { c.err = c.how(c.file) })
	}
	return hash, size, executable, err
}

// through the snapshot a run really has. A file changes after
// the snapshot has answered for it and before it is sent: the action is
// not run, and the error says which file changed.
func TestAFileThatChangesAfterTheSnapshotAnsweredIsNamed(t *testing.T) {
	grid := dial(t)
	for name, how := range map[string]func(file string) error{
		"written again": func(file string) error {
			return os.WriteFile(file, []byte("written again "+time.Now().String()), 0o644)
		},
		"gone": os.Remove,
		"a directory in its place": func(file string) error {
			if err := os.Remove(file); err != nil {
				return err
			}
			return os.Mkdir(file, 0o755)
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := root(t, map[string]string{"greeting.txt": "as the run found it " + strconv.FormatInt(time.Now().UnixNano(), 10) + "\n"})
			snapshot, err := new(source.Keeper).Take(context.Background(), source.Tree{Root: dir, Paths: map[string]string{"": "."}})
			if err != nil {
				t.Fatal(err)
			}
			files := &changing{Snapshot: snapshot, file: filepath.Join(dir, "greeting.txt"), how: how}
			action := reading(dir, []string{"greeting.txt", "nonce"}, files, "cat greeting.txt")

			done, err := grid.Execute(context.Background(), action)

			if files.err != nil {
				t.Fatal(files.err)
			}
			var changed *source.Changed
			if !errors.As(err, &changed) || changed.Path != "greeting.txt" {
				t.Errorf("printed %q, err %v; want greeting.txt named as changed", done.Stdout, err)
			}
		})
	}
}

// a file the run listed and the daemon may not read. The SDK
// leaves a file it may not read out of a root without a word; through
// the snapshot it must be an error.
func TestAFileTheDaemonMayNotReadIsNotLeftOut(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	grid := dial(t)
	dir := root(t, map[string]string{"greeting.txt": "hello " + strconv.FormatInt(time.Now().UnixNano(), 10) + "\n"})
	if err := os.Chmod(filepath.Join(dir, "greeting.txt"), 0); err != nil {
		t.Fatal(err)
	}
	snapshot, err := new(source.Keeper).Take(context.Background(), source.Tree{Root: dir, Paths: map[string]string{"": "."}})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := snapshot.Glob(context.Background(), "", []string{"**"})
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed %v, err %v", listed, err)
	}
	done, err := grid.Execute(context.Background(), reading(dir, listed, snapshot, "ls"))
	if err == nil {
		t.Errorf("ran, and saw %q; want an error: an input the run listed could not be read", done.Stdout)
	} else {
		t.Logf("refused: %v", err)
	}
}
