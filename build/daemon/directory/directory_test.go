package directory_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/daemon/directory"
	"github.com/frontseat-dev/frontseat/common/record"
	"github.com/frontseat-dev/frontseat/common/record/manifest"
	"github.com/frontseat-dev/frontseat/common/vcs"
)

// asked is a version control system that says which directory it was
// asked of, and where a checkout says it was brought from.
type asked struct {
	vcs.History
	standing []string
}

func (a *asked) Revision(name string) error {
	if len(name) != 40 {
		return errors.New("is not a commit's full hash")
	}
	return nil
}

func (a *asked) Standing(_ context.Context, directory string) (vcs.Standing, error) {
	a.standing = append(a.standing, directory)
	return vcs.Standing{}, nil
}

func (a *asked) Origin(context.Context, string) (string, error) {
	return "https://forge.example/own.git", nil
}

var commit = strings.Repeat("a", 40)

func recorded(t *testing.T, root string, repositories ...record.Repository) {
	t.Helper()
	if err := manifest.Write(root, manifest.Manifest{Repositories: repositories}); err != nil {
		t.Fatal(err)
	}
}

func opened(t *testing.T, root string, alone bool) (*directory.Directory, *asked) {
	t.Helper()
	system := &asked{}
	d, err := directory.Open(root, alone, system)
	if err != nil {
		t.Fatal(err)
	}
	return d, system
}

// A directory holds what its manifest records, as the manifest is now: a
// repository recorded after the directory was opened is held, and one no
// longer recorded is not. A directory with no manifest holds none.
func TestADirectoryHoldsWhatItsManifestRecordsNow(t *testing.T) {
	root := t.TempDir()
	d, _ := opened(t, root, false)
	if held, err := d.Holds(); err != nil || len(held.Repositories) != 0 {
		t.Fatalf("a directory with no manifest holds %v (%v); want nothing", held, err)
	}
	recorded(t, root, record.Repository{Name: "code", Remote: "https://forge.example/code.git", Commit: commit})
	if held, err := d.Holds(); err != nil || len(held.Repositories) != 1 || held.Repositories[0].Name != "code" {
		t.Fatalf("it holds %v (%v); want the repository recorded since it was opened", held, err)
	}
	recorded(t, root, record.Repository{Name: "other", Remote: "https://forge.example/other.git", Commit: commit})
	if held, err := d.Holds(); err != nil || len(held.Repositories) != 1 || held.Repositories[0].Name != "other" {
		t.Errorf("it holds %v (%v); want what the manifest records now", held, err)
	}
}

// A manifest is read each time it is asked of, and never taken for the
// one read before by how its file looks: whatever runs in the directory
// can write it in place, at the length it had and the time it said. The
// control is the first reading, of what was written first.
func TestAManifestWrittenInPlaceAtTheSameLengthAndTimeIsReadAgain(t *testing.T) {
	root := t.TempDir()
	d, _ := opened(t, root, false)
	recorded(t, root, record.Repository{Name: "code", Remote: "https://forge.example/code.git", Commit: commit})
	if held, err := d.Holds(); err != nil || len(held.Repositories) != 1 || held.Repositories[0].Name != "code" {
		t.Fatalf("the control: it holds %v (%v); want what was written first", held, err)
	}
	file := filepath.Join(root, filepath.FromSlash(manifest.File))
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	was, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	now := bytes.ReplaceAll(was, []byte("code"), []byte("evil"))
	if len(now) != len(was) || bytes.Equal(now, was) {
		t.Fatalf("the fixture writes %q over %q, which is not another manifest of its length", now, was)
	}
	// In place: the same file of the disk, written through.
	if err := os.WriteFile(file, now, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if held, err := d.Holds(); err != nil || len(held.Repositories) != 1 || held.Repositories[0].Name != "evil" {
		t.Errorf("it holds %v (%v); want what the manifest says now", held, err)
	}
}

// What a directory holds is of one reading: its repositories and the
// entities it knows, each in a repository of that answer. A manifest
// that cannot be read is an error, and not a directory that knows no
// entity; the control is the same manifest, read.
func TestWhatADirectoryHoldsIsOfOneReadingAndAManifestNotReadIsAnError(t *testing.T) {
	root := t.TempDir()
	d, _ := opened(t, root, false)
	file := filepath.Join(root, filepath.FromSlash(manifest.File))
	recorded(t, root, record.Repository{Name: "code", Remote: "https://forge.example/code.git", Commit: commit})
	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(written, "known:\n  - ref: component:default/gateway\n    repository: code\n    root: gateway\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := d.Holds()
	if err != nil || len(held.Repositories) != 1 || len(held.Entities) != 1 || held.Entities[0].Repository != held.Repositories[0].Name || held.Entities[0].Root != "gateway" {
		t.Fatalf("the control: it holds %v (%v); want the repository and the entity that lives in it", held, err)
	}
	if err := os.WriteFile(file, []byte("version: 1\nanother: reader\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if held, err := d.Holds(); err == nil {
		t.Errorf("a manifest that is not read holds %v; want an error, and not a directory that knows nothing", held)
	}
}

// A directory whose manifest says it is being filled, or that its last
// fill failed, holds nothing a build may take: what is recorded there is
// not whole. Each is refused as what it is, by an error a caller knows
// (ErrNotWhole), which says which. The control is the same manifest
// saying neither.
func TestADirectoryBeingFilledOrWhoseFillFailedHoldsNothingToBuild(t *testing.T) {
	root := t.TempDir()
	d, _ := opened(t, root, false)
	held := []record.Repository{{Name: "code", Remote: "https://forge.example/code.git", Commit: commit}}
	for state, says := range map[string]string{manifest.Filling: "being filled", manifest.Failed: "failed"} {
		if err := manifest.Write(root, manifest.Manifest{Repositories: held, State: state}); err != nil {
			t.Fatal(err)
		}
		got, err := d.Holds()
		if !errors.Is(err, directory.ErrNotWhole) || !strings.Contains(err.Error(), says) || len(got.Repositories) != 0 {
			t.Errorf("a manifest that says %q: held %v (%v); want nothing, refused as not whole and saying it is %s", state, got, err, says)
		}
	}
	recorded(t, root, held...)
	if got, err := d.Holds(); err != nil || len(got.Repositories) != 1 {
		t.Errorf("the control: a manifest that says neither holds %v (%v); want its repository", got, err)
	}
}

// A repository is asked of in its own directory of the directory, and
// says where it was brought from as the manifest records; one the
// manifest does not record is asked of nowhere.
func TestARepositoryIsAskedOfInItsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	recorded(t, root, record.Repository{Name: "code", Remote: "https://forge.example/code.git", Commit: commit})
	d, system := opened(t, root, false)
	ctx := context.Background()
	if _, err := d.Standing(ctx, "code"); err != nil || len(system.standing) != 1 || system.standing[0] != filepath.Join(root, "code") {
		t.Errorf("code was asked of in %v (%v); want its directory", system.standing, err)
	}
	if remote, err := d.Remote(ctx, "code"); err != nil || remote != "https://forge.example/code.git" {
		t.Errorf("code was brought from %q (%v); want what the manifest records", remote, err)
	}
	for _, name := range []string{"absent", "", "../code"} {
		if _, err := d.Standing(ctx, name); err == nil {
			t.Errorf("a repository %q the directory does not hold was asked of", name)
		}
	}
	if len(system.standing) != 1 {
		t.Errorf("the version control system was asked of %v; want the one repository held", system.standing)
	}
}

// A directory that is one repository alone, a checkout the build was
// given, is that repository, of no name: it is asked of in the directory
// itself, says itself where it was brought from, and reads no manifest.
func TestADirectoryAloneIsOneRepositoryOfNoName(t *testing.T) {
	root := t.TempDir()
	recorded(t, root, record.Repository{Name: "code", Remote: "https://forge.example/code.git", Commit: commit})
	d, system := opened(t, root, true)
	ctx := context.Background()
	if held, err := d.Holds(); err != nil || len(held.Repositories) != 0 {
		t.Errorf("alone, it holds %v (%v); want no repository by a name", held, err)
	}
	if _, err := d.Standing(ctx, ""); err != nil || len(system.standing) != 1 || system.standing[0] != root {
		t.Errorf("it was asked of in %v (%v); want the directory itself", system.standing, err)
	}
	if remote, err := d.Remote(ctx, ""); err != nil || remote != "https://forge.example/own.git" {
		t.Errorf("it was brought from %q (%v); want what the checkout says", remote, err)
	}
	if _, err := d.Standing(ctx, "code"); err == nil {
		t.Error("alone, a repository by a name was asked of")
	}
}

// Whatever runs in a directory can write its manifest: one that names a
// place outside the directory, or a commit that is none, is refused, and
// nothing of it is held. The control is the manifest read above.
func TestAManifestThatNamesAPlaceOutsideIsRefused(t *testing.T) {
	for name, bad := range map[string]record.Repository{
		"a directory outside": {Name: "..", Remote: "https://forge.example/code.git", Commit: commit},
		"no commit":           {Name: "code", Remote: "https://forge.example/code.git", Commit: "main"},
	} {
		root := t.TempDir()
		recorded(t, root, bad)
		d, _ := opened(t, root, false)
		held, err := d.Holds()
		if err == nil {
			t.Errorf("%s: held %v; want it refused", name, held)
			continue
		}
		// What refuses it is told to whoever asked for a build, and
		// repeats nothing the manifest said; what it is, is kept.
		if !errors.Is(err, record.ErrUnfit) {
			t.Errorf("%s: refused as %v; want it kept that the manifest is unfit", name, err)
		}
		for _, said := range []string{strconv.Quote(bad.Name), strconv.Quote(bad.Commit)} {
			if strings.Contains(err.Error(), said) {
				t.Errorf("%s: the refusal repeats %s of the manifest: %v", name, said, err)
			}
		}
	}
}

// A directory is read with a version control system: given none, it is
// not opened.
func TestADirectoryIsNotOpenedWithNoVersionControlSystem(t *testing.T) {
	if _, err := directory.Open(t.TempDir(), false, nil); err == nil {
		t.Error("a directory was opened with no version control system")
	}
}

// A directory holds a repository something else mounted there as it
// holds a clone, by its record: where a commit is asked of it, the
// changeset it stands at is said. It has no history: whatever is asked
// of its version control system, where it stands, its tags, what
// changed, what is uncommitted or ignored, a tag made or pushed, where
// it was brought from, is refused as mounted, and the system is asked
// nothing of it. The control is the clone beside it, of which the
// system is asked, in its own directory.
func TestADirectoryHoldsAMountedRepositoryAndAsksNoHistoryOfIt(t *testing.T) {
	root := t.TempDir()
	d, system := opened(t, root, false)
	recorded(t, root, record.Repository{Name: "code", Remote: "https://forge.example/code.git", Commit: commit})
	if mounted, err := d.Mounted(); err != nil || mounted != "" {
		t.Fatalf("the control, a directory of clones, holds the mounted repository %q (%v); want none", mounted, err)
	}
	recorded(t, root,
		record.Repository{Name: "code", Remote: "https://forge.example/code.git", Commit: commit},
		record.Repository{Name: "stored", Commit: "changeset-9", Roots: []string{"greet"}, Mounted: true})
	held, err := d.Holds()
	if err != nil || len(held.Repositories) != 2 || !held.Repositories[1].Mounted || held.Repositories[1].Commit != "changeset-9" || held.Repositories[0].Mounted {
		t.Fatalf("it holds %+v (%v); want the clone, and the mounted repository at its changeset", held.Repositories, err)
	}
	if mounted, err := d.Mounted(); err != nil || mounted != "stored" {
		t.Errorf("it names the mounted repository %q (%v); want stored", mounted, err)
	}
	ctx := context.Background()
	for what, ask := range map[string]func(repository string) error{
		"where it stands":      func(r string) error { _, err := d.Standing(ctx, r); return err },
		"its tags":             func(r string) error { _, err := d.Tags(ctx, r); return err },
		"what changed":         func(r string) error { _, err := d.Changes(ctx, r, commit, nil); return err },
		"what is uncommitted":  func(r string) error { _, err := d.Uncommitted(ctx, r); return err },
		"what is ignored":      func(r string) error { _, _, err := d.Ignored(ctx, r); return err },
		"a tag made":           func(r string) error { return d.Tag(ctx, r, "v1.0.0", commit) },
		"a tag pushed":         func(r string) error { return d.PushTag(ctx, r, "v1.0.0") },
		"where it was brought": func(r string) error { _, err := d.Remote(ctx, r); return err },
	} {
		if err := ask("stored"); !errors.Is(err, directory.ErrMounted) {
			t.Errorf("%s, asked of the mounted repository: %v; want it refused as mounted", what, err)
		}
	}
	if len(system.standing) != 0 {
		t.Errorf("the version control system was asked where %v stands; want nothing asked of a mounted repository", system.standing)
	}
	if _, err := d.Standing(ctx, "code"); err != nil || len(system.standing) != 1 || system.standing[0] != filepath.Join(root, "code") {
		t.Errorf("the control, the clone: asked of %v (%v); want its own directory", system.standing, err)
	}
	if remote, err := d.Remote(ctx, "code"); err != nil || remote != "https://forge.example/code.git" {
		t.Errorf("the control, the clone, was brought from %q (%v); want its remote", remote, err)
	}
}
