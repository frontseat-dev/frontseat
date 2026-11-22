package source_test

import "github.com/frontseat-dev/frontseat/server/internal/source/internal/manifest"

import "github.com/frontseat-dev/frontseat/server/internal/source/record"

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/source"

	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
)

// tapes is a version control system that is not git: its revisions are
// tape numbers, and it brings a repository by writing one file.
type tapes struct {
	mu    sync.Mutex
	asked []string
}

func (s *tapes) note(what string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, what)
}

func (s *tapes) NameOf(remote string) string { return filepath.Base(remote) }

func (s *tapes) Revision(name string) error {
	if name != "tape-7" {
		return errors.New("is no tape")
	}
	return nil
}

func (s *tapes) Clone(_ context.Context, directory string, from vcs.Source) (string, error) {
	s.note("clone " + from.Remote)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	return "tape-7", os.WriteFile(filepath.Join(directory, "from-tape.txt"), []byte("spooled\n"), 0o644)
}

func (s *tapes) Widen(context.Context, string, string, []string) error { return nil }

// A workspace brings its repositories and says where they stand with the
// version control system it was given, whatever that is: here one that
// has never heard of git, in a directory git would not call a repository.
func TestAWorkspaceAsksTheVersionControlSystemItWasGiven(t *testing.T) {
	system := &tapes{}
	root := t.TempDir()
	w, err := source.Open(root, false, system)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.NameOf("vault/reels"); got != "reels" {
		t.Errorf("the repository is named %q, want reels, as the system names it", got)
	}

	if err := w.Populate(1, []record.Checkout{{Name: "reels", Remote: "vault/reels"}}, nil); err != nil {
		t.Fatal(err)
	}
	now := settled(t, w)
	if now.State != source.Ready || len(now.Repositories) != 1 || now.Repositories[0].Commit != "tape-7" {
		t.Fatalf("the workspace ended %v (%s) holding %+v, want reels at tape-7", now.State, now.Message, now.Repositories)
	}
	if want := []string{"clone vault/reels"}; !slices.Equal(system.asked, want) {
		t.Errorf("the system was asked %q, want %q", system.asked, want)
	}
	// What is no revision of this system is refused, as git's own would
	// be here: a manifest that says a repository stands at one.
	written, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifest.File)))
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(other, filepath.FromSlash(manifest.File))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, filepath.FromSlash(manifest.File)), []byte(strings.ReplaceAll(string(written), "tape-7", "4b825dc642cb6eb9a060e54bf8d69288fbee4904")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(other, false, system); err == nil {
		t.Error("a workspace at a git commit was opened by a system whose revisions are tapes")
	}
	// The manifest it wrote is read again by the same rule.
	if _, err := source.Open(root, false, system); err != nil {
		t.Errorf("the workspace is not opened again: %v", err)
	}
}

// A workspace that brings repositories is given something to bring them
// with; a checkout the sandbox was given needs nothing.
func TestAWorkspaceThatBringsRepositoriesNeedsAVersionControlSystem(t *testing.T) {
	if _, err := source.Open(t.TempDir(), false, nil); err == nil {
		t.Error("a workspace with nothing to bring repositories with was opened")
	}
	mounted, err := source.Open(t.TempDir(), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mounted.Populate(1, []record.Checkout{{Name: "reels", Remote: "vault/reels"}}, nil); err == nil {
		t.Error("something was cloned into a mounted checkout")
	}
	if got := mounted.NameOf("vault/reels"); got != "" {
		t.Errorf("a mounted checkout names a repository %q", got)
	}
}

// A remote may hold a token, and a refusal reaches the workspace's
// status: the workspace that holds a repository from one remote and is
// asked for it from another says so without repeating either. Asked for
// it from the remote it has, it takes it.
func TestARefusedSecondRemoteOfARepositoryIsNotRepeated(t *testing.T) {
	w, err := source.Open(t.TempDir(), false, &tapes{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	holds := []record.Checkout{{Name: "reels", Remote: "https://x:tok3n-held@vault/reels"}}
	bring := func(asked []record.Checkout) error {
		err := w.Populate(1, asked, nil)
		for err == nil {
			now, changed := w.Now()
			if now.State != source.Populating {
				break
			}
			<-changed
		}
		return err
	}
	if err := bring(holds); err != nil {
		t.Fatal(err)
	}

	err = bring([]record.Checkout{{Name: "reels", Remote: "https://x:tok3n-asked@vault/reels"}})

	if err == nil {
		t.Fatal("a second remote of a repository the workspace holds was taken")
	}
	if strings.Contains(err.Error(), "tok3n") || !strings.Contains(err.Error(), "reels") {
		t.Errorf("the refusal is %q, want the repository named and neither remote repeated", err)
	}
	if err := bring(holds); err != nil {
		t.Errorf("the control, the remote it holds: %v", err)
	}
}
