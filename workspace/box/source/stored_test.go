package source_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	connect "connectrpc.com/connect/v2"

	sourcev1 "github.com/frontseat-dev/frontseat/common/told/sourceapi/frontseat/source/v1alpha1"
	"github.com/frontseat-dev/frontseat/workspace/box/source"
)

// A stored repository is mounted in the workspace before its supervisor
// is told of it: the supervisor brings nothing for it, asks its version
// control system nothing, changes nothing of what is at its directory,
// and says it holds it as it was told, at the changeset told, from no
// remote, beside what it cloned. It keeps no record of it: started
// again, it holds none until it is told again. The control is a
// repository told to be cloned, which the system is asked for.
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
	t.Cleanup(held.Close)
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
	// The control, beside it: a repository to clone is brought.
	both := &sourcev1.SourceSpec{Generation: 2, Stored: stored, Repositories: []*sourcev1.RepositorySource{{Remote: "vault/reels", Name: "reels"}}}
	if _, err := api.SetSource(ctx, &sourcev1.SetSourceRequest{Spec: both}); err != nil {
		t.Fatal(err)
	}
	with, err := api.WaitSource(ctx, &sourcev1.WaitSourceRequest{Generation: 2})
	if err != nil || len(with.GetSource().GetRepositories()) != 2 || len(system.asked) != 1 || system.asked[0] != "clone vault/reels" {
		t.Errorf("told a repository to clone beside it, it holds %v and its system was asked %v (%v)", with.GetSource().GetRepositories(), system.asked, err)
	}
	// Started again, it keeps what it cloned and no stored repository.
	again, err := source.Open(root, false, system)
	if err != nil {
		t.Fatal(err)
	}
	if now, _ := again.Now(); len(now.Stored) != 0 || len(now.Repositories) != 1 || now.Generation != 0 {
		t.Errorf("started again it holds %v and the stored %v at the generation %d, want what it cloned and no generation", now.Repositories, now.Stored, now.Generation)
	}
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
		"no directory there":         {Stored: []*sourcev1.StoredSource{{Name: "absent", Repository: "absent", Changeset: "changeset-9"}}},
		"a file where it is to be":   {Stored: []*sourcev1.StoredSource{{Name: "a-file", Repository: "services", Changeset: "changeset-9"}}},
		"a name that climbs":         {Stored: []*sourcev1.StoredSource{{Name: "..", Repository: "services", Changeset: "changeset-9"}}},
		"no changeset":               {Stored: []*sourcev1.StoredSource{{Name: "services", Repository: "services"}}},
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
