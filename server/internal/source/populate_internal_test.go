package source

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/source/record"
	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
)

// stalled is a version control system whose remote accepts and never
// answers: a clone waits until it is stopped, unless it is let through.
type stalled struct {
	entered chan struct{}
	through atomic.Bool
	left    atomic.Int32
}

func (s *stalled) NameOf(remote string) string { return filepath.Base(remote) }

func (s *stalled) Revision(string) error { return nil }

func (s *stalled) Clone(ctx context.Context, _ string, _ vcs.Source) (string, error) {
	defer s.left.Add(1)
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
	system, w, asked := overStalled(t)
	if err := w.Populate(1, asked, nil); err != nil {
		t.Fatal(err)
	}
	<-system.entered

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
