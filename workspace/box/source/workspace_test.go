package source_test

import "github.com/frontseat-dev/frontseat/common/record/manifest"

import "github.com/frontseat-dev/frontseat/common/record"

import "github.com/frontseat-dev/frontseat/sdk/go/entity"

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/testsupport/testforge"

	"github.com/frontseat-dev/frontseat/workspace/box/source"

	"github.com/frontseat-dev/frontseat/common/vcs/git"
)

// settled waits for a workspace to stop being populated and returns it
// as it then stands.
func settled(t *testing.T, w *source.Workspace) source.Holding {
	t.Helper()
	deadline := time.After(time.Minute)
	for {
		now, changed := w.Now()
		if now.State != source.Populating {
			return now
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatal("the workspace was still being populated after a minute")
		}
	}
}

func TestOfSeveralAskedToFillAWorkspaceAtOnceOneIsTakenAndTheRestRefused(t *testing.T) {
	forge := testforge.Make(t)
	w, err := source.Open(t.TempDir(), false, git.System{})
	if err != nil {
		t.Fatal(err)
	}
	asked := []record.Checkout{{Name: "services", Remote: forge.Repository("services"), Ref: "main"}}

	const askers = 8
	start := make(chan struct{})
	answers := make(chan error, askers)
	for range askers {
		go func() {
			<-start
			answers <- w.Populate(1, asked, nil, nil)
		}()
	}
	close(start)

	taken := 0
	for range askers {
		if err := <-answers; err == nil {
			taken++
		}
	}
	if taken != 1 {
		t.Errorf("%d of %d were taken, want one: two cloning into one directory at once", taken, askers)
	}
	if now := settled(t, w); now.State != source.Ready || len(now.Repositories) != 1 {
		t.Errorf("the workspace ended %v holding %d repositories (%s), want ready with one", now.State, len(now.Repositories), now.Message)
	}
}

func TestNothingIsClonedIntoACheckoutTheSandboxWasGiven(t *testing.T) {
	w, err := source.Open(t.TempDir(), true, git.System{})
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Populate(1, []record.Checkout{{Name: "services", Remote: "file:///nowhere"}}, nil, nil); err == nil {
		t.Error("a mounted checkout was populated")
	}
}

// overForge makes a forge and opens an empty workspace to fill from it,
// and returns both.
func overForge(t *testing.T) (testforge.Forge, *source.Workspace) {
	t.Helper()
	forge := testforge.Make(t)
	w, err := source.Open(within(t), false, git.System{})
	if err != nil {
		t.Fatal(err)
	}
	return forge, w
}

// within makes a workspace's directory with nothing beside it, and
// returns it.
func within(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	return directory
}

// held returns what a directory holds, by name.
func held(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Whether a workspace may be filled is settled for everything asked
// before anything is cloned: one checkout it cannot hold, and none of
// what was asked with it is there.
func TestNothingIsClonedWhereOneOfWhatWasAskedCannotBeHeld(t *testing.T) {
	forge := testforge.Make(t)
	directory := within(t)
	w, err := source.Open(directory, false, git.System{})
	if err != nil {
		t.Fatal(err)
	}
	sound := record.Checkout{Name: "services", Remote: forge.Repository("services"), Ref: "main"}

	err = w.Populate(1, []record.Checkout{sound, {Name: "../elsewhere", Remote: forge.Repository("platform"), Ref: "main"}}, nil, nil)

	if err == nil {
		t.Error("the workspace took a repository named out of it")
	}
	if now := settled(t, w); now.State != source.Empty {
		t.Errorf("the workspace is %v (%s), want empty: nothing was to start", now.State, now.Message)
	}
	if made := held(t, filepath.Dir(directory)); len(made) != 1 {
		t.Errorf("%v is beside the workspace, want nothing", made)
	}
	if made := held(t, directory); len(made) != 0 {
		t.Errorf("the workspace holds %v, want nothing", made)
	}

	if err := w.Populate(1, []record.Checkout{sound}, nil, nil); err != nil {
		t.Fatalf("what it can hold was refused: %v", err)
	}
	if now := settled(t, w); now.State != source.Ready {
		t.Errorf("the workspace is %v (%s), want ready with what it can hold", now.State, now.Message)
	}
}

func TestAnEntityPlacedOutOfTheWorkspaceIsNotKnown(t *testing.T) {
	forge, w := overForge(t)
	sound := []record.Checkout{{Name: "services", Remote: forge.Repository("services"), Ref: "main"}}
	ref := entity.Ref{Kind: "component", Namespace: "default", Name: "hello"}

	for name, located := range map[string]record.Located{
		"in a repository out of the workspace": {Ref: ref, Repository: "../elsewhere", Root: "hello"},
		"in a directory out of its repository": {Ref: ref, Repository: "services", Root: "../../elsewhere"},
	} {
		if err := w.Populate(1, sound, []record.Located{located}, nil); err == nil {
			t.Errorf("the workspace took an entity %s", name)
			settled(t, w)
		}
	}
}

// A workspace's manifest is a file in it, which whatever runs there can
// write. One that names a place outside the workspace is not opened.
func TestAWorkspaceWhoseManifestNamesAPlaceOutsideItIsNotOpened(t *testing.T) {
	directory := within(t)
	recorded := manifest.Manifest{Repositories: []record.Repository{{Name: "../elsewhere", Remote: "file:///nowhere", Commit: "0000000000000000000000000000000000000000"}}}
	if err := manifest.Write(directory, recorded); err != nil {
		t.Fatal(err)
	}

	if _, err := source.Open(directory, false, git.System{}); err == nil {
		t.Error("it was opened")
	}

	recorded.Repositories[0].Name = "services"
	if err := manifest.Write(directory, recorded); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(directory, false, git.System{}); err != nil {
		t.Errorf("a workspace whose manifest names a directory of it was not opened: %v", err)
	}
}

// A workspace holds one checkout of a repository. Asked for the same
// name from elsewhere, or at another branch, it says so: it does not
// keep what it has and call that what was asked for.
func TestAWorkspaceIsNotGivenASecondCheckoutOfARepositoryItHolds(t *testing.T) {
	forge, w := overForge(t)
	holds := record.Checkout{Name: "services", Remote: forge.Repository("services"), Ref: "main"}
	if err := w.Populate(1, []record.Checkout{holds}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if now := settled(t, w); now.State != source.Ready {
		t.Fatalf("the workspace is %v: %s", now.State, now.Message)
	}

	for name, asked := range map[string]record.Checkout{
		"from another remote": {Name: "services", Remote: forge.Repository("platform"), Ref: "main"},
		"at another branch":   {Name: "services", Remote: forge.Repository("services"), Ref: "release"},
	} {
		if err := w.Populate(1, []record.Checkout{asked}, nil, nil); err == nil {
			t.Errorf("asked for services %s, the workspace took it", name)
			settled(t, w)
		}
	}
	if err := w.Populate(1, []record.Checkout{holds}, nil, nil); err != nil {
		t.Errorf("asked for what it holds, the workspace refused: %v", err)
	}
	if now := settled(t, w); now.State != source.Ready || len(now.Repositories) != 1 {
		t.Errorf("the workspace is %v holding %d repositories (%s), want ready with the one", now.State, len(now.Repositories), now.Message)
	}
}

// Filling a workspace can fail half way: a remote that does not answer,
// a branch that is not there. What was cloned by then is the
// workspace's, and asking again finishes the job.
func TestAWorkspaceThatFailedToFillIsFilledByAskingAgain(t *testing.T) {
	forge, w := overForge(t)
	services := record.Checkout{Name: "services", Remote: forge.Repository("services"), Ref: "main"}

	if err := w.Populate(1, []record.Checkout{services, {Name: "platform", Remote: forge.Repository("platform"), Ref: "no-such-branch"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if now := settled(t, w); now.State != source.Failed {
		t.Fatalf("the workspace is %v, want failed: platform has no such branch", now.State)
	}

	if err := w.Populate(1, []record.Checkout{services, {Name: "platform", Remote: forge.Repository("platform"), Ref: "main"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if now := settled(t, w); now.State != source.Ready || len(now.Repositories) != 2 {
		t.Errorf("asked again, the workspace is %v holding %d repositories (%s), want ready with both", now.State, len(now.Repositories), now.Message)
	}
}
