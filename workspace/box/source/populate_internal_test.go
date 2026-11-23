package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/common/vcs"
)

// stalled is a version control system whose remote accepts and never
// answers: a clone writes the first of what it brings and waits until it
// is stopped, unless it is let through.
type stalled struct {
	entered chan struct{}
	through atomic.Bool
	left    atomic.Int32
}

func (s *stalled) NameOf(remote string) string { return filepath.Base(remote) }

func (s *stalled) Revision(string) error { return nil }

func (s *stalled) Clone(ctx context.Context, directory string, _ vcs.Source) (string, error) {
	defer s.left.Add(1)
	if err := os.WriteFile(filepath.Join(directory, "brought.txt"), []byte("brought\n"), 0o644); err != nil {
		return "", err
	}
	if s.through.Load() {
		return "0", nil
	}
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return "", ctx.Err()
}

func (s *stalled) Widen(context.Context, string, string, []string) error { return nil }

// overStalled is a workspace that brings its repositories with a system
// whose remote answers nothing, and a repository to ask it for.
func overStalled(t *testing.T) (*stalled, *Workspace, []record.Checkout) {
	t.Helper()
	system := &stalled{entered: make(chan struct{}, 1)}
	w, err := Open(t.TempDir(), false, system)
	if err != nil {
		t.Fatal(err)
	}
	return system, w, []record.Checkout{{Name: "services", Remote: "forge/services"}}
}

// underWay is a workspace over a stalled system, told to bring the
// repository asked, once the clone of it is under way.
func underWay(t *testing.T) (*stalled, *Workspace, []record.Checkout) {
	t.Helper()
	system, w, asked := overStalled(t)
	if err := w.Populate(1, asked, nil); err != nil {
		t.Fatal(err)
	}
	<-system.entered
	return system, w, asked
}

// ended waits for a workspace to stop being populated, for no longer
// than a test may take, and returns it as it then stands.
func ended(t *testing.T, w *Workspace) Holding {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		now, changed := w.Now()
		if now.State != Populating {
			return now
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatal("the workspace was still being populated after thirty seconds")
		}
	}
}

// A remote that accepts and answers nothing does not leave a workspace
// being populated for ever, which would refuse every later source: the
// repository is given up after its bound, the workspace says so, and
// asked again, with a remote that answers, it is brought.
func TestARepositoryNotBroughtWithinItsBoundFailsTheWorkspaceWhichSaysSo(t *testing.T) {
	system, w, asked := overStalled(t)
	defer w.Close()
	w.within = 50 * time.Millisecond

	if err := w.Populate(1, asked, nil); err != nil {
		t.Fatal(err)
	}

	now := ended(t, w)
	if now.State != Failed || !strings.Contains(now.Message, "services was not brought within 50ms") {
		t.Errorf("the workspace is %v (%s), want failed, saying the repository was not brought within its bound", now.State, now.Message)
	}
	system.through.Store(true)
	if err := w.Populate(2, asked, nil); err != nil {
		t.Fatalf("asked again: %v", err)
	}
	if now := ended(t, w); now.State != Ready {
		t.Errorf("asked again of a remote that answers, the workspace is %v (%s), want ready", now.State, now.Message)
	}
}

// What brings a workspace's repositories ends with the workspace: Close
// stops it and returns only once it has stopped, the workspace says why
// it was not populated, and nothing is brought after.
func TestClosingAWorkspaceStopsWhatIsBringingItsRepositories(t *testing.T) {
	system, w, asked := underWay(t)

	closed := make(chan struct{})
	go func() { w.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not return: what brings the repository was not stopped")
	}

	if left := system.left.Load(); left != 1 {
		t.Errorf("Close returned with %d clones ended, want the one joined", left)
	}
	if now, _ := w.Now(); now.State != Failed || !strings.Contains(now.Message, errClosed.Error()) {
		t.Errorf("the workspace is %v (%s), want failed, saying the daemon stopped", now.State, now.Message)
	}
	system.through.Store(true)
	if err := w.Populate(2, asked, nil); err != nil {
		t.Fatal(err)
	}
	if now, _ := w.Now(); now.State != Failed || system.left.Load() != 1 {
		t.Errorf("told a source once closed, the workspace is %v after %d clones, want failed and nothing brought", now.State, system.left.Load())
	}
}

// A daemon that ends while a repository is being cloned, killed with
// its sandbox, leaves what it had cloned so far; the daemon started
// again over the same directory is told the same source and brings the
// repository. What is on the disk while the clone is under way is what
// such a daemon leaves. The control: a directory of the repository's
// name that no clone of the workspace made is another's, and is refused.
func TestAWorkspaceOpenedAgainOverACloneStoppedMidwayBringsIt(t *testing.T) {
	_, w, asked := underWay(t)
	left := t.TempDir()
	if err := os.CopyFS(left, os.DirFS(w.Root())); err != nil {
		t.Fatal(err)
	}
	w.Close()

	again := &stalled{entered: make(chan struct{}, 1)}
	again.through.Store(true)
	reopened, err := Open(left, false, again)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Populate(1, asked, nil); err != nil {
		t.Fatal(err)
	}
	if now := ended(t, reopened); now.State != Ready {
		t.Errorf("opened again over a clone stopped midway, the workspace is %v (%s), want ready", now.State, now.Message)
	}

	another := t.TempDir()
	if err := os.Mkdir(filepath.Join(another, "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(another, "services", "mine.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	held, err := Open(another, false, again)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := held.Populate(1, asked, nil); err != nil {
		t.Fatal(err)
	}
	if now := ended(t, held); now.State != Failed || !strings.Contains(now.Message, "services is in the workspace already") {
		t.Errorf("over a directory no clone made, the workspace is %v (%s), want failed, saying it is there already", now.State, now.Message)
	}
	if _, err := os.Stat(filepath.Join(another, "services", "mine.txt")); err != nil {
		t.Errorf("what was in a directory no clone made is gone: %v", err)
	}
}

// A repository that fails for a reason of its own while the workspace
// is closing is said to fail for that reason, and not to have been
// stopped: only what was stopped is said to be. The control is
// TestClosingAWorkspaceStopsWhatIsBringingItsRepositories.
func TestARepositoryThatFailsOfItselfWhileTheWorkspaceClosesSaysWhy(t *testing.T) {
	_, w, asked := overStalled(t)
	defer w.Close()
	if err := os.Mkdir(filepath.Join(w.Root(), "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.stop()

	if err := w.Populate(1, asked, nil); err != nil {
		t.Fatal(err)
	}

	if now := ended(t, w); now.State != Failed || !strings.Contains(now.Message, "services is in the workspace already") {
		t.Errorf("the workspace is %v (%s), want failed, saying the repository is there already", now.State, now.Message)
	}
}

// A workspace says in its manifest that it is being filled, from before
// anything of the fill is brought until all of it is recorded, and that
// its last fill failed: a build reads the directory and nothing else, and
// is not to take a repository being widened in place, or one a fill left
// midway, for one that is whole. A workspace opened over a manifest that
// says either is not ready. The control is the same workspace once it
// was filled, whose manifest says neither.
func TestAWorkspaceSaysInItsManifestThatItIsBeingFilledAndThatAFillFailed(t *testing.T) {
	system, w, asked := underWay(t)
	said := func() string {
		t.Helper()
		recorded, found, err := manifest.Read(w.Root(), system.Revision)
		if err != nil || !found {
			t.Fatalf("the workspace's manifest: found %v, %v", found, err)
		}
		return recorded.State
	}
	if state := said(); state != manifest.Filling {
		t.Fatalf("while a repository is brought, the manifest says %q; want %q", state, manifest.Filling)
	}
	w.Close()
	if state := said(); state != manifest.Failed {
		t.Fatalf("after a fill that failed, the manifest says %q; want %q", state, manifest.Failed)
	}

	system.through.Store(true)
	reopened, err := Open(w.Root(), false, system)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if now, _ := reopened.Now(); now.State != Failed {
		t.Errorf("opened over a manifest that says its fill failed, the workspace is %v; want failed", now.State)
	}
	if err := reopened.Populate(2, asked, nil); err != nil {
		t.Fatal(err)
	}
	if now := ended(t, reopened); now.State != Ready {
		t.Fatalf("asked again, the workspace is %v (%s); want ready", now.State, now.Message)
	}
	if state := said(); state != "" {
		t.Errorf("the control: filled, the manifest says %q; want nothing", state)
	}
}
