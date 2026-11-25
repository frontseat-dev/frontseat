package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A look is one to hold a later look against only where every file in
// it had settled when it began: a file written within the moment its
// times are told in before that may be written again and look the same.
// It is the keeper's own rule (settledBy), by the keeper's own moment.
// Control: the same file, looked at once that moment has passed.
func TestALookHasSettledOnceEveryFileInItHas(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("just written"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	written := time.Unix(0, newest(look(info)))
	moment := settles(look(info))
	tree := Tree{Root: root, Paths: map[string]string{"": "."}}
	for name, tc := range map[string]struct {
		at      time.Time
		settled bool
	}{
		"looked at within the moment the file was written in": {written.Add(moment - time.Nanosecond), false},
		"looked at once the moment had passed":                {written.Add(moment), true},
	} {
		for where, beside := range map[string][]string{"a file of the tree": nil, "a file beside it": {file}} {
			of := tree
			if beside != nil {
				of = Tree{Root: root}
			}
			seen, err := lookAt(context.Background(), of, beside, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			if len(seen.files) != 1 || seen.Settled() != tc.settled {
				t.Errorf("%s, %s: the look of %d files had settled: %t; want one file, and %t", where, name, len(seen.files), seen.Settled(), tc.settled)
			}
		}
	}
}

// A directory in which a file was made and removed again is held to the
// same: until it has settled, a file made and removed again within the
// moment may leave it looking as it did. Its files had settled long
// before. Control: the same look, once the directory's moment has
// passed.
func TestALookHasSettledOnceEveryDirectoryInItHas(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "app")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kept.go", "passing.go"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("package app\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(2 * settles(looked{written: 1}))
	if err := os.Remove(filepath.Join(directory, "passing.go")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	written, moment := time.Unix(0, newest(look(info))), settles(look(info))
	tree := Tree{Root: root, Paths: map[string]string{"": "."}}
	for at, settled := range map[time.Time]bool{written.Add(moment - time.Nanosecond): false, written.Add(moment): true} {
		seen, err := lookAt(context.Background(), tree, nil, at)
		if err != nil {
			t.Fatal(err)
		}
		if seen.Settled() != settled {
			t.Errorf("looked at %s after the directory last changed: the look had settled: %t; want %t", at.Sub(written), seen.Settled(), settled)
		}
	}
}
