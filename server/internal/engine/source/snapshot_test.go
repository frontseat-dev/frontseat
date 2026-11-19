package source_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
)

// repository is a directory with a repository in it, a file inside the
// repository and one beside it.
func repository(t *testing.T) source.Tree {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "inside"), "inside")
	write(t, filepath.Join(parent, "outside"), "outside")
	return source.Tree{Root: root, Paths: map[string]string{"": "."}}
}

func write(t *testing.T, file, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readInside is the repository, and a run that has read its file inside.
func readInside(t *testing.T) (source.Tree, *source.Snapshot) {
	t.Helper()
	tree := repository(t)
	run := taken(t, tree)
	if _, _, err := run.Read(context.Background(), "", "inside"); err != nil {
		t.Fatal(err)
	}
	return tree, run
}

// globsAsSelected fails the test unless what the run finds for patterns
// is what selecting by them among every file it holds gives.
func globsAsSelected(t *testing.T, run *source.Snapshot, every, patterns []string) {
	t.Helper()
	var want []string
	for _, name := range every {
		if pattern.Select(patterns, name) {
			want = append(want, name)
		}
	}
	got, err := run.Glob(context.Background(), "", patterns)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("%v: found %v, err %v; want %v", patterns, got, err, want)
	}
}

// listsInside fails the test unless the run lists the file inside and
// nothing else.
func listsInside(t *testing.T, run *source.Snapshot) {
	t.Helper()
	listed, err := run.Glob(context.Background(), "", []string{"**"})
	if want := []string{"inside"}; err != nil || !reflect.DeepEqual(listed, want) {
		t.Errorf("listed %v, err %v; want %v", listed, err, want)
	}
}

// listsInsideAndReads fails the test unless the run lists the file inside
// and nothing else, and finds the file name; it returns what the run
// read of it.
func listsInsideAndReads(t *testing.T, run *source.Snapshot, name string) []byte {
	t.Helper()
	listsInside(t, run)
	content, found, err := run.Read(context.Background(), "", name)
	if err != nil || !found {
		t.Errorf("read %q, found %v, err %v; want the file", content, found, err)
	}
	return content
}

// hashed is the hash of content, as a file that holds it is known by.
func hashed(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// taken is the repository as a run that begins now sees it.
func taken(t *testing.T, tree source.Tree) *source.Snapshot {
	t.Helper()
	snapshot, err := new(source.Keeper).Take(context.Background(), tree)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// quietly changes a file's content and puts its size and the time it
// says it was written back as they were. When anything of it last
// changed still tells.
func quietly(t *testing.T, file, content string) {
	t.Helper()
	was, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(content)) != was.Size() {
		t.Fatalf("the test changes %s quietly only with content of its size", file)
	}
	write(t, file, content)
	if err := os.Chtimes(file, time.Time{}, was.ModTime()); err != nil {
		t.Fatal(err)
	}
}

func TestAFileOfTheRepositoryIsRead(t *testing.T) {
	run := taken(t, repository(t))

	content, found, err := run.Read(context.Background(), "", "inside")

	if err != nil || !found || string(content) != "inside" {
		t.Errorf("read %q, found %v, err %v; want the file", content, found, err)
	}
}

// A plugin reads through this, and names the file itself. What it names
// is held to the repository: a path that leaves it is refused, however
// it leaves.
func TestAFileOutsideTheRepositoryIsNotRead(t *testing.T) {
	tree := repository(t)
	if err := os.Symlink(filepath.Join("..", "outside"), filepath.Join(tree.Root, "link")); err != nil {
		t.Fatal(err)
	}
	run := taken(t, tree)
	for _, path := range []string{"../outside", "inside/../../outside", filepath.Join(filepath.Dir(tree.Root), "outside"), "link"} {
		content, found, err := run.Read(context.Background(), "", path)
		if err == nil {
			t.Errorf("%s: read %q, found %v, and no error; want it refused", path, content, found)
		}
	}
}

func TestAFileThatIsNotThereIsNotAnError(t *testing.T) {
	run := taken(t, repository(t))

	_, found, err := run.Read(context.Background(), "", "absent")

	if err != nil || found {
		t.Errorf("found %v, err %v; want neither", found, err)
	}
}

// A link to a file of the repository is that file, to whatever reads it
// or lists it; one that leaves the repository is no file of it.
func TestALinkWithinTheRepositoryIsTheFileItLeadsTo(t *testing.T) {
	tree := repository(t)
	if err := os.Symlink("inside", filepath.Join(tree.Root, "within")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "outside"), filepath.Join(tree.Root, "leaves")); err != nil {
		t.Fatal(err)
	}
	run := taken(t, tree)

	content, found, err := run.Read(context.Background(), "", "within")
	if err != nil || !found || string(content) != "inside" {
		t.Errorf("read %q, found %v, err %v; want what the link leads to", content, found, err)
	}
	listed, err := run.Glob(context.Background(), "", []string{"**"})
	if want := []string{"inside", "within"}; err != nil || !reflect.DeepEqual(listed, want) {
		t.Errorf("listed %v, err %v; want %v", listed, err, want)
	}
}

// A run is of the files there were when it began. One that comes after
// is not there for it, and is there for the run that begins after it.
func TestAFileThatCameAfterTheRunBeganIsNotOfIt(t *testing.T) {
	tree := repository(t)
	run := taken(t, tree)
	write(t, filepath.Join(tree.Root, "later"), "later")

	listsInside(t, run)
	if _, found, err := run.Read(context.Background(), "", "later"); err != nil || found {
		t.Errorf("found %v, err %v; want a file that is not there", found, err)
	}

	next := taken(t, tree)
	listed, err := next.Glob(context.Background(), "", []string{"**"})
	if want := []string{"inside", "later"}; err != nil || !reflect.DeepEqual(listed, want) {
		t.Errorf("the next run listed %v, err %v; want %v", listed, err, want)
	}
}

// What is in a directory whose name begins with a dot is not listed,
// and is read where it is named.
func TestWhatIsUnderADotDirectoryIsReadAndNotListed(t *testing.T) {
	tree := repository(t)
	write(t, filepath.Join(tree.Root, ".settings", "config"), "set")
	run := taken(t, tree)

	if content := listsInsideAndReads(t, run, ".settings/config"); string(content) != "set" {
		t.Errorf("read %q; want the file", content)
	}
}

// A file that is not as it was when the run began fails whatever reads
// it: the run is of one moment, and says which file left it.
func TestAFileChangedSinceTheRunBeganIsRefused(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, file string){
		"written again": func(t *testing.T, file string) { write(t, file, "inside, and more") },
		"gone":          func(t *testing.T, file string) { os.Remove(file) },
	} {
		t.Run(name, func(t *testing.T) {
			tree := repository(t)
			run := taken(t, tree)
			change(t, filepath.Join(tree.Root, "inside"))

			_, _, err := run.Read(context.Background(), "", "inside")

			var changed *source.Changed
			if !errors.As(err, &changed) || changed.Path != "inside" {
				t.Errorf("err %v; want the file named as changed", err)
			}
		})
	}
}

// A file written again and made to say it was written when it was,
// with content of the same size, has still changed: when anything of it
// last changed is not its author's to set.
func TestAFileMadeToLookAsItDidHasStillChanged(t *testing.T) {
	tree, run := readInside(t)
	// The control: read again as it is, it is the same file.
	if _, _, err := run.Read(context.Background(), "", "inside"); err != nil {
		t.Fatalf("read twice: %v", err)
	}
	quietly(t, filepath.Join(tree.Root, "inside"), "edited")

	_, _, err := run.Read(context.Background(), "", "inside")

	var changed *source.Changed
	if !errors.As(err, &changed) {
		t.Errorf("err %v; want the file named as changed", err)
	}
	if _, _, _, err := run.File(filepath.Join(tree.Root, "inside")); !errors.As(err, &changed) {
		t.Errorf("asked what it held: err %v; want the file named as changed", err)
	}
}

// What an action is sent of a file is named by where the file is, where
// the daemon runs.
func TestAFileIsNamedByWhatItHeld(t *testing.T) {
	tree := repository(t)
	if err := os.WriteFile(filepath.Join(tree.Root, "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := taken(t, tree)

	hash, size, executable, err := run.File(filepath.Join(tree.Root, "inside"))
	sum := sha256.Sum256([]byte("inside"))
	if want := hex.EncodeToString(sum[:]); err != nil || hash != want || size != 6 || executable {
		t.Errorf("hash %s, size %d, executable %v, err %v; want %s, 6 bytes, not executable", hash, size, executable, err, want)
	}
	if _, _, executable, err := run.File(filepath.Join(tree.Root, "tool")); err != nil || !executable {
		t.Errorf("executable %v, err %v; want an executable", executable, err)
	}
	if _, _, _, err := run.File(filepath.Join(filepath.Dir(tree.Root), "outside")); err == nil {
		t.Error("a file of no repository was answered for; want it refused")
	}
}

// A rewrite is written over the file the run read, and over no other:
// where the file is no longer what the run read, nothing is written.
func TestARewriteIsWrittenOnlyOverWhatTheRunRead(t *testing.T) {
	tree, run := readInside(t)
	file := filepath.Join(tree.Root, "inside")
	if err := run.Apply("", source.Change{Path: "inside", Kind: source.Replace, From: hashed("inside"), Content: []byte("rewritten")}); err != nil {
		t.Fatalf("the control, a file as the run read it: %v", err)
	}
	if content, _ := os.ReadFile(file); string(content) != "rewritten" {
		t.Errorf("the file holds %q; want what was written", content)
	}
	// The run wrote it, so to the run it is what it wrote.
	if content, _, err := run.Read(context.Background(), "", "inside"); err != nil || string(content) != "rewritten" {
		t.Errorf("read %q, err %v; want what the run wrote", content, err)
	}

	run = taken(t, tree)
	if _, _, err := run.Read(context.Background(), "", "inside"); err != nil {
		t.Fatal(err)
	}
	quietly(t, file, "REWRITTEN")
	err := run.Apply("", source.Change{Path: "inside", Kind: source.Replace, From: hashed("rewritten"), Content: []byte("lost")})
	var changed *source.Changed
	if !errors.As(err, &changed) {
		t.Errorf("err %v; want the file named as changed", err)
	}
	if content, _ := os.ReadFile(file); string(content) != "REWRITTEN" {
		t.Errorf("the file holds %q; want what its author wrote", content)
	}
}

// A run writes its fixes while its actions are being sent: a file is
// rewritten while others are read.
func TestARewriteAndReadsAtOnce(t *testing.T) {
	tree := repository(t)
	write(t, filepath.Join(tree.Root, "other"), "other")
	run := taken(t, tree)
	if _, _, err := run.Read(context.Background(), "", "inside"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() {
		var err error
		for range 200 {
			if _, _, _, err = run.File(filepath.Join(tree.Root, "other")); err != nil {
				break
			}
		}
		done <- err
	}()
	was := "inside"
	for i := range 20 {
		now := fmt.Sprintf("rewritten %d", i)
		if err := run.Apply("", source.Change{Path: "inside", Kind: source.Replace, From: hashed(was), Content: []byte(now)}); err != nil {
			t.Fatal(err)
		}
		was = now
	}
	if err := <-done; err != nil {
		t.Error(err)
	}
}

// The files patterns select are those that selecting among every file
// would give, in the same order, wherever the patterns are written
// beneath: directories whose names sort one way and whose files sort
// another, one inside another, and none at all.
func TestTheFilesPatternsSelectAreTheSameHoweverTheyAreLookedFor(t *testing.T) {
	tree := repository(t)
	names := []string{"a/x.go", "a/b/y.go", "a/b/y_test.go", "a-b/z.go", "a0/w.go", "ab/v.go", "c/u.go", "top.go"}
	for _, name := range names {
		write(t, filepath.Join(tree.Root, filepath.FromSlash(name)), "package p\n")
	}
	run := taken(t, tree)
	every, err := run.Glob(context.Background(), "", []string{"**"})
	if err != nil || len(every) != len(names)+1 {
		t.Fatalf("listed %v, err %v", every, err)
	}
	for _, patterns := range [][]string{
		{"a/**", "a-b/**"}, {"a-b/*.go", "a/*.go"}, {"a/**", "a/b/*.go"}, {"a/b/**", "a/**", "!a/**/*_test.go"},
		{"a0/*", "a/*", "a-b/*"}, {"c/*.go"}, {"nowhere/**"}, {"*.go"}, {"a/x.go"}, {"!a/**"},
	} {
		globsAsSelected(t, run, every, patterns)
	}
}
