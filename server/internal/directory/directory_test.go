package directory_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/directory"
	"github.com/frontseat-dev/frontseat/server/internal/manifest"
	"github.com/frontseat-dev/frontseat/server/internal/record"
	"github.com/frontseat-dev/frontseat/server/internal/vcs"
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
	if held, err := d.Repositories(); err != nil || len(held) != 0 {
		t.Fatalf("a directory with no manifest holds %v (%v); want nothing", held, err)
	}
	recorded(t, root, record.Repository{Name: "code", Remote: "https://forge.example/code.git", Commit: commit})
	if held, err := d.Repositories(); err != nil || len(held) != 1 || held[0].Name != "code" {
		t.Fatalf("it holds %v (%v); want the repository recorded since it was opened", held, err)
	}
	recorded(t, root, record.Repository{Name: "other", Remote: "https://forge.example/other.git", Commit: commit})
	if held, err := d.Repositories(); err != nil || len(held) != 1 || held[0].Name != "other" {
		t.Errorf("it holds %v (%v); want what the manifest records now", held, err)
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
	if held, err := d.Repositories(); err != nil || len(held) != 0 {
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
		if held, err := d.Repositories(); err == nil {
			t.Errorf("%s: held %v; want it refused", name, held)
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
