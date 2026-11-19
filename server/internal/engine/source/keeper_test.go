package source_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
)

// settled waits until the files a test has just written have settled:
// what a file held is kept only then.
func settled() { time.Sleep(30 * time.Millisecond) }

// begun is a run that begins now, of a daemon that has run before.
func begun(t *testing.T, keeper *source.Keeper, tree source.Tree) *source.Snapshot {
	t.Helper()
	snapshot, err := keeper.Take(context.Background(), tree)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// A daemon keeps what a file held for as long as the file looks the
// same: the run after reads only what looks different.
func TestAFileThatLooksAsItDidIsNotReadAgain(t *testing.T) {
	tree := repository(t)
	inside := filepath.Join(tree.Root, "inside")
	keeper := &source.Keeper{}
	settled()

	first := begun(t, keeper, tree)
	was, _, _, err := first.File(inside)
	if err != nil || first.Hashed() != 1 {
		t.Fatalf("the first run read %d files, err %v; want the one", first.Hashed(), err)
	}
	// Asked again by the same run, it is not read again.
	if _, _, _, err := first.File(inside); err != nil || first.Hashed() != 1 {
		t.Errorf("asked twice, the run read %d files, err %v; want the one, once", first.Hashed(), err)
	}

	second := begun(t, keeper, tree)
	still, _, _, err := second.File(inside)
	if err != nil || still != was || second.Hashed() != 0 {
		t.Errorf("the second run read %d files and got %s, err %v; want none read and %s", second.Hashed(), still, err, was)
	}

	// The control: written again, it is read again, and is what it now
	// holds.
	write(t, inside, "inside, and more")
	third := begun(t, keeper, tree)
	now, _, _, err := third.File(inside)
	if err != nil || now == was || third.Hashed() != 1 {
		t.Errorf("the third run read %d files and got %s, err %v; want the file read and another hash", third.Hashed(), now, err)
	}
}

// A file a plugin reads is read for its content whatever is known of
// it, and is known to the run after by what was read.
func TestAFileReadForItsContentIsKnownToTheNextRun(t *testing.T) {
	tree := repository(t)
	keeper := &source.Keeper{}
	settled()
	first := begun(t, keeper, tree)
	if _, _, err := first.Read(context.Background(), "", "inside"); err != nil {
		t.Fatal(err)
	}

	second := begun(t, keeper, tree)
	_, found, err := second.Digest(context.Background(), "", "inside")

	if err != nil || !found || second.Hashed() != 0 {
		t.Errorf("the second run read %d files, found %v, err %v; want the file known and not read", second.Hashed(), found, err)
	}
}
