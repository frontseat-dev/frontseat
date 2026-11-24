package source_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	connect "connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
	sourcev1 "github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/common/vcs"
	"github.com/frontseat-dev/frontseat/workspace/box/source"
)

// A stored repository is mounted in the workspace before its supervisor
// is told of it: the supervisor brings nothing for it, asks its version
// control system nothing, changes nothing of what is at its directory,
// and says it holds it as it was told, at the changeset told, from no
// remote, beside what it cloned. It records it in the workspace's
// manifest as mounted, at that changeset, for a build to read, beside
// what it cloned and after it. It holds none by that record: started
// again, it holds none until it is told again, and told again it
// records the same, byte for byte. The control is a repository told to
// be cloned, which the system is asked for, and which is recorded as a
// clone.
func TestAStoredRepositoryIsHeldAsItIsMountedAndNothingIsBroughtForIt(t *testing.T) {
	ctx := context.Background()
	root, system := t.TempDir(), &tapes{}
	mounted := filepath.Join(root, "services")
	if err := os.MkdirAll(filepath.Join(mounted, "greet"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mounted, "greet", "greet.go"), []byte("package greet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	held, api := supervising(t, root, false, system)
	stored := []*sourcev1.StoredSource{{Name: "services", Repository: "acme/services", Changeset: "changeset-9", Roots: []string{"greet"}}}
	if _, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: &sourcev1.SourceSpec{Generation: 1, Stored: stored}}); err != nil {
		t.Fatalf("told a stored repository alone: %v", err)
	}
	done, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: 1})
	if err != nil || done.GetSource().GetState() != sourcev1.Source_STATE_READY {
		t.Fatalf("told a stored repository it is %v (%v), want it ready", done.GetSource(), err)
	}
	holds := done.GetSource().GetRepositories()
	if len(holds) != 1 || holds[0].GetName() != "services" || holds[0].GetStored() != "acme/services" || holds[0].GetCommit() != "changeset-9" || holds[0].GetRemote() != "" || len(holds[0].GetRoots()) != 1 {
		t.Errorf("it says it holds %v, want the stored repository as it was told", holds)
	}
	if len(system.asked) != 0 {
		t.Errorf("its version control system was asked %v for a repository that is mounted", system.asked)
	}
	if kept, err := os.ReadFile(filepath.Join(mounted, "greet", "greet.go")); err != nil || string(kept) != "package greet\n" {
		t.Errorf("what is mounted was changed: %q, %v", kept, err)
	}
	asMounted := record.Repository{Name: "services", Commit: "changeset-9", Roots: []string{"greet"}, Mounted: true}
	if alone := recordedIn(t, root); len(alone.Repositories) != 1 || !reflect.DeepEqual(alone.Repositories[0], asMounted) || alone.State != "" {
		t.Errorf("the manifest records %+v, want the stored repository alone, as mounted at its changeset, and whole", alone)
	}
	// The control, beside it: a repository to clone is brought.
	both := &sourcev1.SourceSpec{Generation: 2, Stored: stored, Repositories: []*sourcev1.RepositorySource{{Remote: "vault/reels", Name: "reels"}}}
	if _, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: both}); err != nil {
		t.Fatal(err)
	}
	with, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: 2})
	if err != nil || len(with.GetSource().GetRepositories()) != 2 || len(system.asked) != 1 || system.asked[0] != "clone vault/reels" {
		t.Errorf("told a repository to clone beside it, it holds %v and its system was asked %v (%v)", with.GetSource().GetRepositories(), system.asked, err)
	}
	recorded := recordedIn(t, root)
	if len(recorded.Repositories) != 2 || recorded.Repositories[0].Name != "reels" || recorded.Repositories[0].Mounted || recorded.Repositories[0].Remote != "vault/reels" || !reflect.DeepEqual(recorded.Repositories[1], asMounted) {
		t.Errorf("the manifest records %+v, want the clone, as a clone, and the stored repository after it, as mounted", recorded.Repositories)
	}
	written, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifest.File)))
	if err != nil {
		t.Fatal(err)
	}
	held.Close()
	// Started again, it keeps what it cloned and no stored repository,
	// and what the one before it recorded is still there for a build.
	again, api := supervising(t, root, false, system)
	t.Cleanup(again.Close)
	if now, _ := again.Now(); len(now.Stored) != 0 || len(now.Repositories) != 1 || now.Generation != 0 {
		t.Errorf("started again it holds %v and the stored %v at the generation %d, want what it cloned and no generation", now.Repositories, now.Stored, now.Generation)
	}
	if still, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifest.File))); err != nil || !bytes.Equal(still, written) {
		t.Errorf("started again, the manifest is %q (%v), want it as it was written", still, err)
	}
	// Told again what the control plane keeps, it records the same.
	if _, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: both}); err != nil {
		t.Fatal(err)
	}
	if told, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: 2}); err != nil || told.GetSource().GetState() != sourcev1.Source_STATE_READY || len(told.GetSource().GetRepositories()) != 2 {
		t.Fatalf("told again it is %v (%v), want it ready, holding both", told.GetSource(), err)
	}
	if same, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifest.File))); err != nil || !bytes.Equal(same, written) {
		t.Errorf("told again, the manifest is %q (%v), want the same as before: %q", same, err, written)
	}
	if len(system.asked) != 1 {
		t.Errorf("told again, its version control system was asked %v, want nothing more", system.asked)
	}
}

// recordedIn is the manifest of the workspace in a directory, as a
// build reads it.
func recordedIn(t *testing.T, root string) manifest.Manifest {
	t.Helper()
	recorded, found, err := manifest.Read(root, (&tapes{}).Revision)
	if err != nil || !found {
		t.Fatalf("the workspace's manifest: found %v, %v", found, err)
	}
	return recorded
}

// A stored repository the supervisor cannot hold as told is refused by
// name, SOURCE_UNFIT, and nothing is held of the source it came with:
// one whose directory is not there, since a supervisor brings none; one
// whose name is no directory of the workspace; one told at no changeset
// or with no name of the store's; one told twice, or by the name of a
// repository that is cloned. The control is one that is mounted.
func TestAStoredRepositoryThatIsNotMountedAsToldIsRefusedByName(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, there := range []string{"services", "reels"} {
		if err := os.Mkdir(filepath.Join(root, there), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	held, api := supervising(t, root, false, &tapes{})
	t.Cleanup(held.Close)
	sound := &sourcev1.StoredSource{Name: "services", Repository: "services", Changeset: "changeset-9"}
	for what, spec := range map[string]*sourcev1.SourceSpec{
		"no directory there":       {Stored: []*sourcev1.StoredSource{{Name: "absent", Repository: "absent", Changeset: "changeset-9"}}},
		"a file where it is to be": {Stored: []*sourcev1.StoredSource{{Name: "a-file", Repository: "services", Changeset: "changeset-9"}}},
		"a name that climbs":       {Stored: []*sourcev1.StoredSource{{Name: "..", Repository: "services", Changeset: "changeset-9"}}},
		"no changeset":             {Stored: []*sourcev1.StoredSource{{Name: "services", Repository: "services"}}},
		"a changeset not written plainly, which could not be recorded": {Stored: []*sourcev1.StoredSource{{Name: "services", Repository: "services", Changeset: "changeset 9"}}},
		"no name of the store's":     {Stored: []*sourcev1.StoredSource{{Name: "services", Changeset: "changeset-9"}}},
		"a directory outside it":     {Stored: []*sourcev1.StoredSource{{Name: "services", Repository: "services", Changeset: "changeset-9", Roots: []string{"../reels"}}}},
		"told twice":                 {Stored: []*sourcev1.StoredSource{sound, sound}},
		"the name of what is cloned": {Stored: []*sourcev1.StoredSource{{Name: "reels", Repository: "reels", Changeset: "changeset-9"}}, Repositories: []*sourcev1.RepositorySource{{Remote: "vault/reels", Name: "reels"}}},
	} {
		spec.Generation = 1
		_, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: spec})
		reasonOf(t, err, connect.CodeInvalidArgument, "SOURCE_UNFIT")
		if now, _ := held.Now(); len(now.Stored) != 0 || now.Generation != 0 {
			t.Errorf("%s: refused, it holds the stored %v at the generation %d", what, now.Stored, now.Generation)
		}
	}
	if _, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: &sourcev1.SourceSpec{Generation: 1, Stored: []*sourcev1.StoredSource{sound}}}); err != nil {
		t.Errorf("the control, one that is mounted: %v", err)
	}
	if done, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: 1}); err != nil || done.GetSource().GetState() != sourcev1.Source_STATE_READY || len(done.GetSource().GetRepositories()) != 1 {
		t.Errorf("the control, once told, is %v (%v)", done.GetSource(), err)
	}
}

// snapping is a tape system whose clone says it began, waits until it
// is let go, and then fails.
type snapping struct {
	*tapes
	began, let chan struct{}
}

func (s snapping) Clone(context.Context, string, vcs.Source) (string, error) {
	close(s.began)
	<-s.let
	return "", errors.New("the tape snapped")
}

// The manifest records a stored repository as mounted in every state
// it says: while the workspace is being filled, and where its fill
// failed, as well as once it is whole. A build takes nothing of a
// workspace that says either, and whatever reads the manifest then
// still finds every repository of the workspace in it.
func TestAStoredRepositoryIsRecordedWhileTheWorkspaceIsFilledAndWhereItsFillFailed(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	system := snapping{tapes: &tapes{}, began: make(chan struct{}), let: make(chan struct{})}
	w, err := source.Open(root, false, system)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	stored := []source.Stored{{Name: "services", Repository: "acme/services", Changeset: "changeset-9"}}
	if err := w.Populate(1, []record.Checkout{{Name: "reels", Remote: "vault/reels"}}, nil, stored); err != nil {
		t.Fatal(err)
	}
	<-system.began
	if filling := recordedIn(t, root); filling.State != manifest.Filling || len(filling.Repositories) != 1 || !filling.Repositories[0].Mounted {
		t.Errorf("while it is filled the manifest records %+v, want it filling, with the stored repository as mounted", filling)
	}
	close(system.let)
	if now := settled(t, w); now.State != source.Failed {
		t.Fatalf("the workspace ended %v (%s), want it failed", now.State, now.Message)
	}
	if failed := recordedIn(t, root); failed.State != manifest.Failed || len(failed.Repositories) != 1 || !failed.Repositories[0].Mounted || failed.Repositories[0].Commit != "changeset-9" {
		t.Errorf("once its fill failed the manifest records %+v, want it failed, with the stored repository as mounted", failed)
	}
}
