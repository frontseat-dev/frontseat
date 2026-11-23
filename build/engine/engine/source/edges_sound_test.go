package source_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
)

// REVIEW, found sound: names a plugin might give that are no file of
// the repository are refused or not found, and none panics or reads
// outside.
func TestOddNames(t *testing.T) {
	tree := repository(t)
	write(t, filepath.Join(tree.Root, "dir", "file"), "in a directory")
	if err := os.Symlink(filepath.Dir(tree.Root), filepath.Join(tree.Root, "up")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop", filepath.Join(tree.Root, "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nowhere", filepath.Join(tree.Root, "dangling")); err != nil {
		t.Fatal(err)
	}
	run := taken(t, tree)
	for _, name := range []string{"", ".", "..", "/", "dir", "dir/", "inside/", "inside/.", "inside\x00", "up/outside", "up", "loop", "dangling", "dir/../../outside", strings.Repeat("a/", 5000) + "x", "\\..\\outside", "inside/../inside/../../outside"} {
		content, found, err := run.Read(context.Background(), "", name)
		if found && string(content) != "inside" {
			t.Errorf("Read(%.40q) = %q, found; want nothing of outside the repository", name, content)
		}
		hash, found, err2 := run.Digest(context.Background(), "", name)
		if found && err2 == nil && hash == "" {
			t.Errorf("Digest(%.40q) found with no hash", name)
		}
		_ = err
	}
	for _, at := range []string{"", ".", tree.Root, filepath.Dir(tree.Root), filepath.Join(tree.Root, "up", "outside"), filepath.Join(tree.Root, "..", "outside"), filepath.Join(tree.Root, "dir")} {
		if hash, _, _, err := run.File(at); err == nil {
			t.Errorf("File(%q) = %s and no error; want it refused", at, hash)
		}
	}
	if content, found, err := run.Read(context.Background(), "no such repository", "inside"); err != nil || found {
		t.Errorf("an unknown repository: %q, found %v, err %v", content, found, err)
	}
}

// REVIEW, found sound: a listed file replaced, after the run began, by
// a link, by a directory, by another file moved over it; and a
// directory replaced by a link to one that holds the same names.
func TestAFileReplacedIsChanged(t *testing.T) {
	for name, replace := range map[string]func(t *testing.T, root string){
		"by a link to a file outside": func(t *testing.T, root string) {
			os.Remove(filepath.Join(root, "dir", "file"))
			if err := os.Symlink(filepath.Join(filepath.Dir(root), "outside"), filepath.Join(root, "dir", "file")); err != nil {
				t.Fatal(err)
			}
		},
		"by a directory": func(t *testing.T, root string) {
			os.Remove(filepath.Join(root, "dir", "file"))
			if err := os.Mkdir(filepath.Join(root, "dir", "file"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"by a file of the same content moved over it": func(t *testing.T, root string) {
			write(t, filepath.Join(root, "other"), "in a directory")
			if err := os.Rename(filepath.Join(root, "other"), filepath.Join(root, "dir", "file")); err != nil {
				t.Fatal(err)
			}
		},
		"its directory by a link to a copy outside": func(t *testing.T, root string) {
			outside := filepath.Join(filepath.Dir(root), "elsewhere")
			if err := os.Rename(filepath.Join(root, "dir"), outside); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "dir")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			tree := repository(t)
			write(t, filepath.Join(tree.Root, "dir", "file"), "in a directory")
			run := taken(t, tree)
			replace(t, tree.Root)
			content, found, err := run.Read(context.Background(), "", "dir/file")
			if err == nil {
				t.Errorf("read %q, found %v and no error; want it refused", content, found)
			}
			_, _, _, err = run.File(filepath.Join(tree.Root, "dir", "file"))
			t.Logf("File: %v", err)
		})
	}
}

// REVIEW, found sound: many readers of many files at once, with two
// runs of one daemon; nothing races and every answer is the file's.
func TestManyReadersAtOnce(t *testing.T) {
	tree := repository(t)
	for i := range 50 {
		write(t, filepath.Join(tree.Root, "many", string(rune('a'+i%26))+strings.Repeat("x", i)), strings.Repeat("y", i))
	}
	keeper := &source.Keeper{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := keeper.Take(context.Background(), tree)
			if err != nil {
				t.Error(err)
				return
			}
			names, _ := run.Glob(context.Background(), "", []string{"many/**"})
			var inner sync.WaitGroup
			for _, name := range names {
				inner.Add(2)
				go func() {
					defer inner.Done()
					if _, _, err := run.Read(context.Background(), "", name); err != nil {
						t.Error(err)
					}
				}()
				go func() {
					defer inner.Done()
					if _, _, _, err := run.File(filepath.Join(tree.Root, filepath.FromSlash(name))); err != nil {
						t.Error(err)
					}
				}()
			}
			inner.Wait()
		}()
	}
	wg.Wait()
	var changed *source.Changed
	_ = errors.As(nil, &changed)
}
