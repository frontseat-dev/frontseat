package source_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/build/engine/engine/source/sourcetest"
	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
)

// looked is a look at a tree and at the files beside it, once every
// file in it has settled: one a later look is held against.
func looked(t *testing.T, tree source.Tree, beside ...string) *source.Looks {
	t.Helper()
	for {
		seen, err := source.Look(context.Background(), tree, beside)
		if err != nil {
			t.Fatal(err)
		}
		if seen.Settled() {
			return seen
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// watched is a repository of files a run lists and files it does not:
// one its ignore file names, one of its version control system's and
// one in a directory of a tool's.
func watched(t *testing.T) source.Tree {
	t.Helper()
	tree := repository(t)
	for name, content := range map[string]string{
		"frontseat.yaml":      "plugins: {}\n",
		"app/main.go":         "package main\n",
		"app/old.go":          "package main\n",
		"app/led.go":          "package main\n",
		source.IgnoreFile:     "/out/\n*.log\n",
		"out/built":           "built",
		"app/debug.log":       "logged",
		".git/index":          "index",
		".idea/workspace.xml": "<project/>",
	} {
		write(t, filepath.Join(tree.Root, filepath.FromSlash(name)), content)
	}
	if err := os.Symlink("led.go", filepath.Join(tree.Root, "app", "link.go")); err != nil {
		t.Fatal(err)
	}
	return tree
}

// A file written again with other content of the same size, and its
// times set back to what they were, does not look as it did: when
// anything of it last changed is the disk's own to say, and is part of
// how a file looks. It is the change a look at size and time of writing
// misses. Control: nothing written, nothing looks otherwise.
func TestAFileRewrittenAtItsSizeWithItsTimesSetBackLooksOtherwise(t *testing.T) {
	tree := watched(t)
	file := filepath.Join(tree.Root, "app", "main.go")
	before := looked(t, tree)
	was, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if again := looked(t, tree); len(again.Since(before)) != 0 {
		t.Fatalf("the control: with nothing written, %v look otherwise", again.Since(before))
	}

	write(t, file, "package MAIN\n")
	if err := os.Chtimes(file, was.ModTime(), was.ModTime()); err != nil {
		t.Fatal(err)
	}
	now, err := os.Stat(file)
	if err != nil || now.Size() != was.Size() || !now.ModTime().Equal(was.ModTime()) {
		t.Fatalf("the file is %d bytes written at %s (%v); the test wants it %d bytes written at %s", now.Size(), now.ModTime(), err, was.Size(), was.ModTime())
	}
	if changed := looked(t, tree).Since(before); !slices.Equal(changed, []string{"app/main.go"}) {
		t.Errorf("rewritten at its size with its times set back, %v look otherwise; want app/main.go", changed)
	}
}

// What a run would read otherwise looks otherwise, and is named: a file
// made, removed, moved (the name it had and the one it has), touched
// with nothing of it changed, and the repository's own frontseat.yaml;
// and a link led elsewhere and back, by the link, which leaves the file
// it leads to as it was.
// What a run does not list does not: a file the ignore file names, made
// in a directory the run lists or written again, one of the version
// control system's, one in a directory of a tool's, and a file made and
// removed again, which no run that listed before it read. Each of those
// is the control of the others: the same write, to a file that is
// listed, is seen.
func TestWhatARunListsLooksOtherwiseWhenItChangesAndNothingElseDoes(t *testing.T) {
	at := func(tree source.Tree, name string) string {
		return filepath.Join(tree.Root, filepath.FromSlash(name))
	}
	for name, tc := range map[string]struct {
		change func(*testing.T, source.Tree)
		want   []string
	}{
		"a file made":    {func(t *testing.T, tree source.Tree) { write(t, at(tree, "app/new.go"), "package main\n") }, []string{"app/new.go"}},
		"a file removed": {func(t *testing.T, tree source.Tree) { remove(t, at(tree, "app/old.go")) }, []string{"app/old.go"}},
		"a file moved": {func(t *testing.T, tree source.Tree) {
			if err := os.Rename(at(tree, "app/old.go"), at(tree, "app/renamed.go")); err != nil {
				t.Fatal(err)
			}
		}, []string{"app/old.go", "app/renamed.go"}},
		"a file touched": {func(t *testing.T, tree source.Tree) {
			if err := os.Chtimes(at(tree, "app/main.go"), time.Time{}, time.Now().Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
		}, []string{"app/main.go"}},
		"a file made and removed again": {func(t *testing.T, tree source.Tree) {
			write(t, at(tree, "app/passing.go"), "package main\n")
			remove(t, at(tree, "app/passing.go"))
		}, nil},
		"a file made and removed again in the repository's own directory": {func(t *testing.T, tree source.Tree) {
			write(t, at(tree, "passing.go"), "package main\n")
			remove(t, at(tree, "passing.go"))
		}, nil},
		"a link led elsewhere and back": {func(t *testing.T, tree source.Tree) {
			for _, to := range []string{"old.go", "led.go"} {
				remove(t, at(tree, "app/link.go"))
				if err := os.Symlink(to, at(tree, "app/link.go")); err != nil {
					t.Fatal(err)
				}
			}
		}, []string{"app/link.go"}},
		"a file the ignore file names, made in a directory listed": {func(t *testing.T, tree source.Tree) { write(t, at(tree, "app/new.log"), "logged") }, nil},
		"the repository's frontseat.yaml":                          {func(t *testing.T, tree source.Tree) { write(t, at(tree, "frontseat.yaml"), "plugins: {go: {}}\n") }, []string{"frontseat.yaml"}},
		"a file the ignore file names by its name":                 {func(t *testing.T, tree source.Tree) { write(t, at(tree, "app/debug.log"), "logged again") }, nil},
		"an ignored file":              {func(t *testing.T, tree source.Tree) { write(t, at(tree, "out/built"), "built again") }, nil},
		"an ignored file made":         {func(t *testing.T, tree source.Tree) { write(t, at(tree, "out/more"), "more") }, nil},
		"a file of git's":              {func(t *testing.T, tree source.Tree) { write(t, at(tree, ".git/index"), "another index") }, nil},
		"a file of a tool's directory": {func(t *testing.T, tree source.Tree) { remove(t, at(tree, ".idea/workspace.xml")) }, nil},
	} {
		t.Run(name, func(t *testing.T) {
			tree := watched(t)
			before := looked(t, tree)
			tc.change(t, tree)
			if changed := looked(t, tree).Since(before); !slices.Equal(changed, tc.want) {
				t.Errorf("%v look otherwise; want %v", changed, tc.want)
			}
		})
	}
}

// remove removes a file of a test's own repository.
func remove(t *testing.T, file string) {
	t.Helper()
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
}

// A file named beside the tree is looked at where it is, wherever that
// is: written again, made and removed, it looks otherwise, by the name
// it was given. A repository of a workspace of several is named with
// its files. Control: the file beside that was left alone is not named.
func TestFilesBesideATreeAndTheRepositoriesOfAWorkspaceAreNamed(t *testing.T) {
	workspace := t.TempDir()
	write(t, filepath.Join(workspace, "one", "a.go"), "package a\n")
	write(t, filepath.Join(workspace, "two", "b.go"), "package b\n")
	tree := source.Tree{Root: workspace, Paths: map[string]string{"one": "one", "two": "two"}}
	elsewhere := t.TempDir()
	settings, plugin, absent, alone := filepath.Join(elsewhere, "build.yaml"), filepath.Join(elsewhere, "go.wasm"), filepath.Join(elsewhere, "ca.pem"), filepath.Join(elsewhere, "left alone")
	for _, file := range []string{settings, plugin, alone} {
		write(t, file, "as it was")
	}
	beside := []string{settings, plugin, absent, alone}
	before := looked(t, tree, beside...)

	write(t, settings, "as it is")
	remove(t, plugin)
	write(t, absent, "made")
	write(t, filepath.Join(workspace, "two", "b.go"), "package b // more\n")
	want := []string{absent, settings, plugin, "two/b.go"}
	slices.Sort(want)
	if changed := looked(t, tree, beside...).Since(before); !slices.Equal(changed, want) {
		t.Errorf("%v look otherwise; want %v", changed, want)
	}
}

// awaited is what waiting for what a run read to change answered
// within a while; nothing, and the deadline, where nothing did.
func awaited(t *testing.T, wait func(context.Context) ([]string, error), within time.Duration) ([]string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), within)
	defer cancel()
	return wait(ctx)
}

// answers holds waiting for what was looked at to change, within a
// while, to answer want; where want is nil, nothing.
func answers(t *testing.T, what string, looks *source.Looks, within time.Duration, want []string) {
	t.Helper()
	changed, err := awaited(t, func(ctx context.Context) ([]string, error) { return looks.Await(ctx, time.Millisecond) }, within)
	if want == nil && !errors.Is(err, context.DeadlineExceeded) || want != nil && (err != nil || !slices.Equal(changed, want)) {
		t.Errorf("%s: %v (%v); want %v", what, changed, err, want)
	}
}

// A file a run reads by name that no listing takes, beneath a directory
// whose name begins with a dot, is waited on as the run held it: written
// again, it is named; one the run asked for and was not there, made, is
// named too. Control: a file beside them in the same directory, which
// the run did not read, is written, and nothing is answered.
func TestWhatARunReadByNameBeneathADotDirectoryIsWaitedOn(t *testing.T) {
	for name, tc := range map[string]struct {
		written string
		want    []string
	}{
		"the file read, written again":   {".cargo/config.toml", []string{".cargo/config.toml"}},
		"the file asked for, made":       {".cargo/absent.toml", []string{".cargo/absent.toml"}},
		"a file beside them, not read":   {".cargo/other.toml", nil},
		"a file of no directory entered": {".git/index", nil},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for _, file := range []string{"app/main.go", ".cargo/config.toml", ".cargo/other.toml", ".git/index"} {
				write(t, filepath.Join(root, filepath.FromSlash(file)), "as it was")
			}
			// Read once it has settled: what is waited on is how it
			// looked, and not a doubt.
			time.Sleep(50 * time.Millisecond)
			wait, err := sourcetest.Ran(t.Context(), root, time.Millisecond, ".cargo/config.toml", ".cargo/absent.toml")
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(root, filepath.FromSlash(tc.written)), "as it is now")
			changed, err := awaited(t, wait, 500*time.Millisecond)
			if tc.want == nil && !errors.Is(err, context.DeadlineExceeded) || tc.want != nil && (err != nil || !slices.Equal(changed, tc.want)) {
				t.Errorf("%s written: %v (%v); want %v", tc.written, changed, err, tc.want)
			}
		})
	}
}

// What a run listed is waited on as the run held it: a file written
// again at its size with its times set back is named, and so is a file
// made in a directory the run listed. Waiting returns as soon as it is
// cancelled, however long it was to wait before it looked again.
// Control: nothing written, nothing is answered.
func TestWhatARunListedIsWaitedOnUntilItIsCancelled(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "app", "main.go"), "package main\n")
	file := filepath.Join(root, "app", "main.go")
	was, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	ran := func() func(context.Context) ([]string, error) {
		wait, err := sourcetest.Ran(t.Context(), root, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		return wait
	}
	wait := ran()
	if changed, err := awaited(t, wait, 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the control: with nothing written, %v (%v)", changed, err)
	}
	write(t, file, "package MAIN\n")
	if err := os.Chtimes(file, was.ModTime(), was.ModTime()); err != nil {
		t.Fatal(err)
	}
	if changed, err := awaited(t, wait, 5*time.Second); err != nil || !slices.Equal(changed, []string{"app/main.go"}) {
		t.Errorf("rewritten at its size with its times set back: %v (%v); want app/main.go", changed, err)
	}

	wait = ran()
	write(t, filepath.Join(root, "app", "new.go"), "package main\n")
	if changed, err := awaited(t, wait, 5*time.Second); err != nil || !slices.Equal(changed, []string{"app/new.go"}) {
		t.Errorf("a file made in a directory listed: %v (%v); want app/new.go", changed, err)
	}

	slow, err := sourcetest.Ran(t.Context(), root, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	began := time.Now()
	if _, err := slow(ctx); !errors.Is(err, context.Canceled) || time.Since(began) > time.Second {
		t.Errorf("cancelled after 50ms, waiting returned after %s: %v; want at once, cancelled", time.Since(began), err)
	}
}

// ranOnce is a run of a repository of one file, app/main.go, once it
// has settled: how to wait for what the run read to change, looking
// every millisecond.
func ranOnce(t *testing.T, root string) func(context.Context) ([]string, error) {
	t.Helper()
	write(t, filepath.Join(root, "app", "main.go"), "package main\n")
	time.Sleep(50 * time.Millisecond)
	wait, err := sourcetest.Ran(t.Context(), root, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return wait
}

// A wait does not answer while files are still being written: what
// looks otherwise is answered once two looks running are the same, and
// every file in them was written longer ago than the moment its times
// are told in. Here five files are written one after another, each
// within that moment of the one before, as a checkout or a generator
// writes them, and looked at every millisecond: what is answered is all
// five. Control: one file written with nothing after it is answered
// alone (TestWhatARunListedIsWaitedOnUntilItIsCancelled).
func TestAWaitAnswersOnceWhatIsWrittenHasSettled(t *testing.T) {
	root := t.TempDir()
	wait := ranOnce(t, root)
	go func() {
		for i := range 5 {
			_ = os.WriteFile(filepath.Join(root, "app", fmt.Sprintf("written-%d.go", i)), []byte("package main\n"), 0o644)
			time.Sleep(5 * time.Millisecond)
		}
	}()
	if changed, err := awaited(t, wait, 5*time.Second); err != nil || len(changed) != 5 {
		t.Errorf("while five files were written one after another, the wait answered %v (%v); want all five", changed, err)
	}
}

// A repository that cannot be listed is no repository to a wait: every
// file of it looks otherwise, and is answered, for the build asked for
// then to say what is wrong. What a run that could not list it read is
// nothing of it (Unread), and is waited on until it can be listed, when
// every file of it is answered. Control: while it cannot, nothing is.
func TestWhatCannotBeListedIsAnsweredAndThenWaitedOnUntilItCanBe(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists a directory it may not read")
	}
	root := t.TempDir()
	app := filepath.Join(root, "app")
	wait := ranOnce(t, root)
	if err := os.Chmod(app, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(app, 0o755) })
	if changed, err := awaited(t, wait, 5*time.Second); err != nil || !slices.Contains(changed, "app/main.go") {
		t.Errorf("a directory that cannot be listed: %v (%v); want app/main.go among what is answered", changed, err)
	}

	tree := source.Tree{Root: root, Paths: map[string]string{"": "."}}
	_, failed := new(source.Keeper).Take(t.Context(), tree)
	if failed == nil {
		t.Fatal("a repository that cannot be listed was taken")
	}
	unread := source.Unread(t.Context(), tree, failed)
	waitUnread := func(ctx context.Context) ([]string, error) { return unread.Await(ctx, time.Millisecond) }
	if changed, err := awaited(t, waitUnread, 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the control: while it cannot be listed, %v (%v)", changed, err)
	}
	if err := os.Chmod(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if changed, err := awaited(t, waitUnread, 5*time.Second); err != nil || !slices.Contains(changed, "app/main.go") {
		t.Errorf("listed again: %v (%v); want app/main.go among what is answered", changed, err)
	}
}

// barred has a file or a directory be one its owner may not read until
// the test ends, or it is let be again (let); root reads what it may
// not, so the test is skipped there.
func barred(t *testing.T, at string) (let func()) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads what it may not")
	}
	info, err := os.Stat(at)
	if err != nil {
		t.Fatal(err)
	}
	let = func() {
		if err := os.Chmod(at, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(at, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(at, info.Mode().Perm()) })
	return let
}

// nothingAnswered holds a wait to answer nothing, three times in a row:
// it waits patiently, and does not answer what it answered before.
func nothingAnswered(t *testing.T, what string, wait func(context.Context) ([]string, error)) {
	t.Helper()
	for i := range 3 {
		if changed, err := awaited(t, wait, 200*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s, wait %d: %v (%v); want nothing answered", what, i+1, changed, err)
		}
	}
}

// A run of a workspace that could not list one repository of two, a
// directory of it being one its owner may not read, is held to what
// could be listed (Unread): with nothing written, three waits in a row
// answer nothing, as the run would fail again. Controls: a file of the
// repository it listed, written, is answered; the directory made
// readable, the files of the repository it could not list are, and so
// they are where it was made readable before what could be listed was
// looked at.
func TestARunThatCouldNotListOneRepositoryWaitsUntilSomethingChanges(t *testing.T) {
	workspace := t.TempDir()
	for _, name := range []string{"a/main.go", "b/main.go", "b/locked/secret.go"} {
		write(t, filepath.Join(workspace, filepath.FromSlash(name)), "package main\n")
	}
	time.Sleep(50 * time.Millisecond)
	let := barred(t, filepath.Join(workspace, "b", "locked"))
	tree := source.Tree{Root: workspace, Paths: map[string]string{"a": "a", "b": "b"}}
	failed := func(meanwhile func()) func(context.Context) ([]string, error) {
		t.Helper()
		_, err := new(source.Keeper).Take(t.Context(), tree)
		if err == nil {
			t.Fatal("a repository that cannot be listed was taken")
		}
		meanwhile()
		unread := source.Unread(t.Context(), tree, err)
		return func(ctx context.Context) ([]string, error) { return unread.With().Await(ctx, time.Millisecond) }
	}
	wait := failed(func() {})
	nothingAnswered(t, "nothing written", wait)
	write(t, filepath.Join(workspace, "a", "main.go"), "package main // more\n")
	if changed, err := awaited(t, wait, 5*time.Second); err != nil || !slices.Equal(changed, []string{"a/main.go"}) {
		t.Errorf("the control, a file of the repository listed, written: %v (%v); want a/main.go", changed, err)
	}
	wait = failed(func() {})
	nothingAnswered(t, "failed again, nothing written", wait)
	let()
	if changed, err := awaited(t, wait, 5*time.Second); err != nil || !slices.Equal(changed, []string{"b/locked/secret.go", "b/main.go"}) {
		t.Errorf("the control, the repository made listable: %v (%v); want its files", changed, err)
	}
	let = barred(t, filepath.Join(workspace, "b", "locked"))
	wait = failed(let)
	if changed, err := awaited(t, wait, 5*time.Second); err != nil || !slices.Equal(changed, []string{"b/locked/secret.go", "b/main.go"}) {
		t.Errorf("the control, the repository made listable before what could be listed was looked at: %v (%v); want its files", changed, err)
	}
}

// A file a run asked for by name and could not read, one its owner may
// not read, is waited on as it looked when the run asked for it: made
// readable, it is answered, and the run that failed on it is made
// again. Control: a file beside it, which the run never named, made
// readable, answers nothing.
func TestAFileARunCouldNotReadByNameIsWaitedOn(t *testing.T) {
	for name, tc := range map[string]struct {
		let  string
		want []string
	}{
		"the file the run named":     {".cargo/config.toml", []string{".cargo/config.toml"}},
		"a file the run never named": {".cargo/other.toml", nil},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for _, file := range []string{"app/main.go", ".cargo/config.toml", ".cargo/other.toml"} {
				write(t, filepath.Join(root, filepath.FromSlash(file)), "as it was")
			}
			time.Sleep(50 * time.Millisecond)
			lets := map[string]func(){}
			for _, file := range []string{".cargo/config.toml", ".cargo/other.toml"} {
				lets[file] = barred(t, filepath.Join(root, filepath.FromSlash(file)))
			}
			snapshot := taken(t, source.Tree{Root: root, Paths: map[string]string{"": "."}})
			if _, _, err := snapshot.Read(t.Context(), "", ".cargo/config.toml"); err == nil {
				t.Fatal("a file its owner may not read was read")
			}
			looks := snapshot.Looks()
			lets[tc.let]()
			answers(t, tc.let+" made readable", looks, time.Second, tc.want)
		})
	}
}

// A wait is answered by what a run would list and read otherwise, and
// not by how a directory looks: a file the repository ignores, made in
// a directory the run listed, answers nothing, nor does a file the run
// rewrote itself (a fix: written beside itself and moved into place),
// though each leaves the directory looking otherwise. Controls: a
// source file made in the same directory is answered, and so is the
// same file written by someone else.
func TestAWaitIsAnsweredByWhatARunListsAndNotByItsDirectories(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*testing.T, string, *source.Snapshot)
		want   []string
	}{
		"a file the repository ignores, made": {func(t *testing.T, root string, _ *source.Snapshot) {
			write(t, filepath.Join(root, "app", "debug.log"), "logged")
		}, nil},
		"the control, a source file made": {func(t *testing.T, root string, _ *source.Snapshot) {
			write(t, filepath.Join(root, "app", "new.go"), "package main\n")
		}, []string{"app/new.go"}},
		"a file the run rewrote itself": {func(t *testing.T, _ string, run *source.Snapshot) {
			was, _, err := run.Digest(t.Context(), "", "app/main.go")
			if err != nil {
				t.Fatal(err)
			}
			if err := run.Apply("", source.Change{Path: "app/main.go", Kind: source.Replace, Content: []byte("package main // fixed\n"), From: was}); err != nil {
				t.Fatal(err)
			}
		}, nil},
		"the control, the same file written by someone else": {func(t *testing.T, root string, _ *source.Snapshot) {
			write(t, filepath.Join(root, "app", "main.go"), "package main // fixed\n")
		}, []string{"app/main.go"}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, source.IgnoreFile), "*.log\n")
			write(t, filepath.Join(root, "app", "main.go"), "package main\n")
			time.Sleep(50 * time.Millisecond)
			run := taken(t, source.Tree{Root: root, Paths: map[string]string{"": "."}})
			tc.change(t, root, run)
			answers(t, name, run.Looks(), time.Second, tc.want)
		})
	}
}

// lookAt is a directory BenchmarkAnIdleWait looks at, in place of one
// of the benchmark's own: a checkout, to measure what one look of an
// idle wait costs there.
var lookAt = flag.String("look", "", "a directory BenchmarkAnIdleWait looks at, in place of one of its own")

// waitedOn is the directory the benchmarks of a wait look at: the one
// -look names, else one of two thousand files of their own, and the tree
// that is that directory, one repository.
func waitedOn(b *testing.B) source.Tree {
	b.Helper()
	directory := *lookAt
	if directory == "" {
		directory = b.TempDir()
		files := map[string]string{}
		for i := range 2000 {
			files[fmt.Sprintf("p%02d/f%03d.go", i/100, i%100)] = "package p\n"
		}
		testfiles.Tree(b, directory, files)
	}
	return source.Tree{Root: directory, Paths: map[string]string{"": "."}}
}

// What one look of a wait for what a run read to change costs, with
// nothing changed: the look of the directory as a run lists it, and the
// look held against what the run held. A wait waits nine times as long
// as a look took before the next (Await), and never less than a quarter
// of a second, so it looks for at most a tenth of its time; what one
// look costs is held all the same (TestBudgetOfAnIdleWait). Of a
// checkout:
//
//	go test -run '^$' -bench BenchmarkAnIdleWait -benchmem ./engine/source/ -args -look "$PWD/../.."
func BenchmarkAnIdleWait(b *testing.B) {
	tree := waitedOn(b)
	snapshot, err := new(source.Keeper).Take(b.Context(), tree)
	if err != nil {
		b.Fatal(err)
	}
	held := snapshot.Looks()
	for b.Loop() {
		now, err := source.Look(b.Context(), tree, nil)
		if err != nil {
			b.Fatal(err)
		}
		if changed := now.Since(held); len(changed) != 0 {
			b.Fatalf("%v changed while the look was measured", changed)
		}
	}
}

// What a build daemon keeps for whoever waits for what a run read to
// change, in bytes of the heap: what the run listed and read, its
// snapshot, every file of it read as a build that reads them all reads
// them, less what the daemon's keeper knows of them, which it keeps for
// every run that has not ended and the four newest that have
// (kept-bytes/run); and what each wait under way holds it against, its
// own Looks (kept-bytes/wait). Both are held, with its allocations
// (TestBudgetOfWhatAWaitKeeps). Of a checkout:
//
//	go test -run '^$' -bench BenchmarkWhatAWaitKeeps -benchtime 3x ./build/engine/engine/source/ -args -look "$PWD"
func BenchmarkWhatAWaitKeeps(b *testing.B) {
	tree := waitedOn(b)
	heap := func() uint64 {
		var stats runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}
	// The daemon's keeper is the daemon's, whatever its runs keep: it
	// knows every file before what a run keeps is measured.
	keeper := new(source.Keeper)
	ran := func() (*source.Snapshot, int) {
		snapshot, err := keeper.Take(b.Context(), tree)
		if err != nil {
			b.Fatal(err)
		}
		listed, err := snapshot.Glob(b.Context(), "", []string{"**"})
		if err != nil {
			b.Fatal(err)
		}
		for _, name := range listed {
			if _, _, err := snapshot.Digest(b.Context(), "", name); err != nil {
				b.Fatal(err)
			}
		}
		return snapshot, len(listed)
	}
	ran()
	var run, wait uint64
	var files int
	for b.Loop() {
		before := heap()
		snapshot, read := ran()
		files = read
		taken := heap()
		looks := snapshot.Looks()
		run, wait = taken-before, heap()-taken
		runtime.KeepAlive(snapshot)
		runtime.KeepAlive(looks)
	}
	b.ReportMetric(float64(files), "files")
	b.ReportMetric(float64(run), "kept-bytes/run")
	b.ReportMetric(float64(wait), "kept-bytes/wait")
}

// A file a run asked for by name and could not look at, or that was no
// file, fails the run, and is waited on as what it was: neither a file
// nor not there, so that whatever it becomes is answered, for the run
// that failed on it to be made again. Here: a directory where the file
// is named, removed; a directory on the way that may not be looked in,
// made one that may, with no file in it, and with a directory where the
// file would be (what could not be looked at made what is no file); a
// pipe, removed; a link out of
// the repository, removed. Control: each left as it was, nothing is
// answered.
func TestWhatARunCouldNotLookAtOrWasNoFileIsAnsweredOnceItChanges(t *testing.T) {
	for name, tc := range map[string]struct {
		// make makes what the run asks for, beneath the repository; fix
		// makes it otherwise.
		make, fix func(t *testing.T, root string)
	}{
		"a directory": {func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, ".cargo", "config.toml"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, root string) { remove(t, filepath.Join(root, ".cargo", "config.toml")) }},
		"a directory that may not be looked in, made one that may, where the file is a directory": {func(t *testing.T, root string) {
			unenterable(t, root, ".cargo/config.toml")
		}, enterable},
		"a directory that may not be looked in": {func(t *testing.T, root string) {
			unenterable(t, root, ".cargo")
		}, enterable},
		"a pipe": {func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, ".cargo"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(filepath.Join(root, ".cargo", "config.toml"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, root string) { remove(t, filepath.Join(root, ".cargo", "config.toml")) }},
		"a link out of the repository": {func(t *testing.T, root string) {
			outside := filepath.Join(t.TempDir(), "config.toml")
			write(t, outside, "[build]\n")
			if err := os.MkdirAll(filepath.Join(root, ".cargo"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, ".cargo", "config.toml")); err != nil {
				t.Fatal(err)
			}
		}, func(t *testing.T, root string) { remove(t, filepath.Join(root, ".cargo", "config.toml")) }},
	} {
		for _, fixed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, made otherwise: %t", name, fixed), func(t *testing.T) {
				root := t.TempDir()
				write(t, filepath.Join(root, "app", "main.go"), "package main\n")
				tc.make(t, root)
				time.Sleep(50 * time.Millisecond)
				run := taken(t, source.Tree{Root: root, Paths: map[string]string{"": "."}})
				if _, _, err := run.Read(t.Context(), "", ".cargo/config.toml"); err == nil {
					t.Fatal("the run read what it could not look at, or what was no file")
				}
				looks := run.Looks()
				var want []string
				if fixed {
					tc.fix(t, root)
					want = []string{".cargo/config.toml"}
				}
				answers(t, name, looks, time.Second, want)
			})
		}
	}
}

// unenterable makes a directory beneath a repository, .cargo among it,
// and has .cargo be one that may not be looked in until the test ends,
// or it is let be (enterable); root looks in what it may not, so the
// test is skipped there.
func unenterable(t *testing.T, root, directory string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root looks in what it may not")
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, ".cargo"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, ".cargo"), 0o755) })
}

// enterable has a repository's .cargo be one that may be looked in.
func enterable(t *testing.T, root string) {
	t.Helper()
	if err := os.Chmod(filepath.Join(root, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// readCargo is a run of a repository, once its files have settled, that
// read .cargo/config.toml by name, and what it read, as it looked.
func readCargo(t *testing.T, root string) *source.Looks {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	run := taken(t, source.Tree{Root: root, Paths: map[string]string{"": "."}})
	if _, found, err := run.Read(t.Context(), "", ".cargo/config.toml"); err != nil || !found {
		t.Fatalf("the run did not read .cargo/config.toml: found %t, %v", found, err)
	}
	return run.Looks()
}

// A repository's ignore file is what a run lists by, and is waited on
// by its name, as a file the run reads, whether the run lists it or
// not: one that ignores itself, written again so that a file the run
// read by name is ignored, is answered. Control: a file it ignores,
// written, answers nothing.
func TestAnIgnoreFileThatIgnoresItselfIsWaitedOn(t *testing.T) {
	for name, tc := range map[string]struct {
		written, content string
		want             []string
	}{
		"the ignore file, written to ignore what the run read": {source.IgnoreFile, ".*\n", []string{source.IgnoreFile}},
		"the control, a file it ignores, written":              {".env", "TOKEN=other\n", nil},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for file, content := range map[string]string{
				source.IgnoreFile:    ".*\n!.cargo/\n",
				".cargo/config.toml": "[build]\n",
				".env":               "TOKEN=none\n",
				"app/main.go":        "package main\n",
			} {
				write(t, filepath.Join(root, filepath.FromSlash(file)), content)
			}
			looks := readCargo(t, root)
			write(t, filepath.Join(root, filepath.FromSlash(tc.written)), tc.content)
			answers(t, name, looks, time.Second, tc.want)
		})
	}
}

// A file a run made by name, beneath a directory whose name begins with
// a dot, which no listing takes, is waited on as the run wrote it: with
// nothing written after, nothing is answered, where it would be by
// every wait were it held to a listing that never takes it. Control:
// the file written again after is answered.
func TestAFileARunMadeBeneathADotDirectoryIsWaitedOnAsItWroteIt(t *testing.T) {
	for name, tc := range map[string]struct {
		after bool
		want  []string
	}{
		"nothing written after":            {false, nil},
		"the control, written again after": {true, []string{".cargo/config.toml"}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "app", "main.go"), "package main\n")
			time.Sleep(50 * time.Millisecond)
			run := taken(t, source.Tree{Root: root, Paths: map[string]string{"": "."}})
			if err := run.Apply("", source.Change{Path: ".cargo/config.toml", Kind: source.Create, Content: []byte("[build]\n")}); err != nil {
				t.Fatal(err)
			}
			looks := run.Looks()
			if tc.after {
				time.Sleep(50 * time.Millisecond)
				write(t, filepath.Join(root, ".cargo", "config.toml"), "[build]\njobs = 1\n")
			}
			answers(t, name, looks, time.Second, tc.want)
		})
	}
}

// A repository's ignore file is waited on by its name where the run
// found none: made, to ignore every file whose name begins with a dot,
// itself among them and the file the run read by name, it is answered,
// though no listing takes it and the run read it nowhere. Control: with
// none made, nothing is answered.
func TestAnIgnoreFileMadeWhereThereWasNoneIsWaitedOn(t *testing.T) {
	for name, tc := range map[string]struct {
		made bool
		want []string
	}{
		"made, ignoring itself":     {true, []string{source.IgnoreFile}},
		"the control, none is made": {false, nil},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, ".cargo", "config.toml"), "[build]\n")
			write(t, filepath.Join(root, "app", "main.go"), "package main\n")
			looks := readCargo(t, root)
			if tc.made {
				write(t, filepath.Join(root, source.IgnoreFile), ".*\n")
			}
			answers(t, name, looks, time.Second, tc.want)
		})
	}
}

// A repository whose directory cannot be opened is answered by what the
// run asked of it by name, though it was not there then: the run that
// asks again fails, and says why. Here the repository lists nothing,
// and the run asked for .cargo/config.toml, and for its ignore file as
// it began, and found neither. Control: left as it was, nothing is
// answered.
func TestARepositoryThatCannotBeOpenedIsAnsweredByWhatTheRunAskedForByName(t *testing.T) {
	for name, tc := range map[string]struct {
		barred bool
		want   []string
	}{
		"its directory may not be read": {true, []string{".cargo/config.toml", source.IgnoreFile}},
		"the control, left as it was":   {false, nil},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, ".cargo", "other.toml"), "[build]\n")
			time.Sleep(50 * time.Millisecond)
			run := taken(t, source.Tree{Root: root, Paths: map[string]string{"": "."}})
			if _, found, err := run.Read(t.Context(), "", ".cargo/config.toml"); err != nil || found {
				t.Fatalf("the run found .cargo/config.toml: %t, %v; the test wants it not there", found, err)
			}
			looks := run.Looks()
			if tc.barred {
				barred(t, root)
			}
			answers(t, name, looks, time.Second, tc.want)
		})
	}
}

// Measured 2026-11-25 on an Apple M4 Max, three runs of
// `go test -run '^$' -bench '^BenchmarkAnIdleWait$' -benchmem -count=1
// ./build/engine/engine/source/`, of two thousand files: 3.82 to 3.85
// ms and 14,484 allocations a look. The time allowed is ten times that,
// the allocations a tenth more.
func TestBudgetOfAnIdleWait(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 39 * time.Millisecond, Allocs: 15900}, BenchmarkAnIdleWait)
}

// Measured 2026-11-25 on an Apple M4 Max, three runs of
// `go test -run '^$' -bench '^BenchmarkWhatAWaitKeeps$' -benchmem
// -count=1 ./build/engine/engine/source/`, of two thousand files: what a
// run keeps for whoever waits, 954,400 bytes (477 a file), and a wait,
// 294,992 bytes, the same in each run; 6.86 to 7.07 ms and 24,541
// allocations an operation, a run taken and read whole and its Looks,
// with the heap measured three times. The time allowed is ten times
// that, the allocations and the bytes kept a tenth more.
func TestBudgetOfWhatAWaitKeeps(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 71 * time.Millisecond, Allocs: 27000, Most: map[string]float64{"kept-bytes/run": 1049900, "kept-bytes/wait": 324500}}, BenchmarkWhatAWaitKeeps)
}
