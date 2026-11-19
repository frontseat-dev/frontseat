package source_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
)

// a run reads a file under a dot directory, and the file then
// goes. The run is told "it is not there", not "it changed".
func TestAHiddenFileThatGoesAfterItWasReadHasChanged(t *testing.T) {
	for name, at := range map[string]string{"the control, a file that is listed": "listed/config", "a file under a dot directory": ".settings/config"} {
		t.Run(name, func(t *testing.T) {
			tree := repository(t)
			write(t, filepath.Join(tree.Root, filepath.FromSlash(at)), "set")
			run := taken(t, tree)
			if content, found, err := run.Read(context.Background(), "", at); err != nil || !found || string(content) != "set" {
				t.Fatalf("read %q, found %v, err %v", content, found, err)
			}
			if err := os.Remove(filepath.Join(tree.Root, filepath.FromSlash(at))); err != nil {
				t.Fatal(err)
			}

			_, found, err := run.Read(context.Background(), "", at)

			var changed *source.Changed
			if !errors.As(err, &changed) {
				t.Errorf("found %v, err %v; want the file named as changed: the run has read it", found, err)
			}
		})
	}
}

// a file under a dot directory that was not there when the run
// first asked, and is there when it asks again: the run is of two
// moments. The control is a listed path, where a file that came after
// is not of the run.
func TestAHiddenFileThatComesAfterTheRunAskedIsNotOfIt(t *testing.T) {
	for name, at := range map[string]string{"the control, a path that is listed": "listed/config", "a path under a dot directory": ".settings/config"} {
		t.Run(name, func(t *testing.T) {
			tree := repository(t)
			run := taken(t, tree)
			if _, found, err := run.Read(context.Background(), "", at); err != nil || found {
				t.Fatalf("found %v, err %v; want a file that is not there", found, err)
			}
			write(t, filepath.Join(tree.Root, filepath.FromSlash(at)), "came after")

			content, found, err := run.Read(context.Background(), "", at)

			if err == nil && found {
				t.Errorf("read %q: the run was told it was not there, and is now told it is", content)
			}
		})
	}
}

// a rewrite of a file that is a link within the repository
// replaces the link, and leaves the file the run read as it was.
func TestARewriteThroughALinkWritesTheFileTheRunRead(t *testing.T) {
	tree := repository(t)
	if err := os.Symlink("inside", filepath.Join(tree.Root, "within")); err != nil {
		t.Fatal(err)
	}
	run := taken(t, tree)
	if content, _, err := run.Read(context.Background(), "", "within"); err != nil || string(content) != "inside" {
		t.Fatalf("read %q, err %v", content, err)
	}

	if err := run.Apply("", source.Change{Path: "within", Kind: source.Replace, From: hashed("inside"), Content: []byte("rewritten")}); err != nil {
		t.Fatal(err)
	}

	if info, err := os.Lstat(filepath.Join(tree.Root, "within")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link is now %v, err %v; want it still a link", info.Mode(), err)
	}
	if content, _ := os.ReadFile(filepath.Join(tree.Root, "inside")); string(content) != "rewritten" {
		t.Errorf("the file the run read holds %q; want what was written", content)
	}
}

// a repository whose directory is a link to a directory lists
// nothing, without a word. The control is the same repository named by
// its directory.
func TestARepositoryThatIsALinkToADirectoryIsListed(t *testing.T) {
	tree := repository(t)
	parent := filepath.Dir(tree.Root)
	if err := os.Symlink("repo", filepath.Join(parent, "linked")); err != nil {
		t.Fatal(err)
	}
	for name, at := range map[string]string{"the control, its directory": "repo", "a link to its directory": "linked"} {
		t.Run(name, func(t *testing.T) {
			listsInsideAndReads(t, taken(t, source.Tree{Root: parent, Paths: map[string]string{"": at}}), "inside")
		})
	}
}

// a plugin names a file to read. Where that is a named pipe
// nobody writes to, the read never returns, and a run that is cancelled
// does not end it.
func TestAReadOfANamedPipeReturns(t *testing.T) {
	tree := repository(t)
	if err := syscall.Mkfifo(filepath.Join(tree.Root, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := taken(t, tree)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := run.Read(ctx, "", "pipe")
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		t.Logf("returned: %v", err)
	case <-time.After(3 * time.Second):
		t.Error("the read of a named pipe had not returned three seconds after its run was cancelled")
		// Let the reader go, so that the test's directory can be removed.
		if w, err := os.OpenFile(filepath.Join(tree.Root, "pipe"), os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			w.Close()
		}
	}
}

// a fix is written while the run's actions are sent the same
// file. Nothing but the run writes the file here, so nothing may say it
// changed.
func TestARewriteAndReadsOfTheSameFileAtOnce(t *testing.T) {
	tree, run := readInside(t)
	inside := filepath.Join(tree.Root, "inside")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var failed error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, _, _, err := run.File(inside); err != nil {
				failed = err
				return
			}
		}
	}()
	was := "inside"
	for i := range 300 {
		content := "rewritten a"
		if i%2 == 1 {
			content = "rewritten b"
		}
		if err := run.Apply("", source.Change{Path: "inside", Kind: source.Replace, From: hashed(was), Content: []byte(content)}); err != nil {
			t.Errorf("rewrite %d: %v", i, err)
			break
		}
		was = content
	}
	close(stop)
	wg.Wait()
	if failed != nil {
		t.Errorf("asked what the file held while the run rewrote it: %v", failed)
	}
}

// Glob looks only beneath the directories pattern.Under names,
// and "a/**" selects "a" itself: what Glob finds is not what selecting
// among every file gives.
func TestGlobFindsWhatSelectingAmongEveryFileGives(t *testing.T) {
	tree := repository(t)
	write(t, filepath.Join(tree.Root, "out"), "a file, not a directory")
	run := taken(t, tree)
	every, err := run.Glob(context.Background(), "", []string{"**"})
	if err != nil {
		t.Fatal(err)
	}
	for _, patterns := range [][]string{{"**/out"}, {"out/**"}} {
		globsAsSelected(t, run, every, patterns)
	}
}

// A fix is worked out from what the run read of a file: one the run has
// not read is not rewritten, and one it has is.
func TestAFileTheRunHasNotReadIsNotRewritten(t *testing.T) {
	tree := repository(t)
	run := taken(t, tree)

	if err := run.Apply("", source.Change{Path: "inside", Kind: source.Replace, From: hashed("inside"), Content: []byte("rewritten")}); err == nil {
		t.Error("a file the run had not read was rewritten; want it refused")
	}
	if content, _ := os.ReadFile(filepath.Join(tree.Root, "inside")); string(content) != "inside" {
		t.Errorf("the file holds %q; want it as it was", content)
	}

	if _, _, err := run.Read(context.Background(), "", "inside"); err != nil {
		t.Fatal(err)
	}
	if err := run.Apply("", source.Change{Path: "inside", Kind: source.Replace, From: hashed("inside"), Content: []byte("rewritten")}); err != nil {
		t.Errorf("the control, a file the run has read: %v", err)
	}
}

// A fix is written over what it was worked out from and nothing else:
// not over what the run itself has written since.
func TestAFixIsNotWrittenOverAnotherFix(t *testing.T) {
	tree, run := readInside(t)
	if err := run.Apply("", source.Change{Path: "inside", Kind: source.Replace, From: hashed("inside"), Content: []byte("the first fix")}); err != nil {
		t.Fatal(err)
	}

	err := run.Apply("", source.Change{Path: "inside", Kind: source.Replace, From: hashed("inside"), Content: []byte("the second fix")})

	if err == nil {
		t.Error("a second fix worked out from what the first replaced was written; want it refused")
	}
	if content, _ := os.ReadFile(filepath.Join(tree.Root, "inside")); string(content) != "the first fix" {
		t.Errorf("the file holds %q; want the first fix", content)
	}
}

// A file under a dot directory is not listed, and is held all the same
// to what the run read of it.
func TestAHiddenFileChangedSinceItWasReadIsRefused(t *testing.T) {
	tree := repository(t)
	hidden := filepath.Join(tree.Root, ".settings", "config")
	write(t, hidden, "set")
	run := taken(t, tree)
	if _, _, err := run.Read(context.Background(), "", ".settings/config"); err != nil {
		t.Fatal(err)
	}
	// The control: read again as it is.
	if _, _, err := run.Read(context.Background(), "", ".settings/config"); err != nil {
		t.Fatalf("read twice: %v", err)
	}
	write(t, hidden, "set, and more")

	_, _, err := run.Read(context.Background(), "", ".settings/config")

	var changed *source.Changed
	if !errors.As(err, &changed) {
		t.Errorf("err %v; want the file named as changed", err)
	}
}
