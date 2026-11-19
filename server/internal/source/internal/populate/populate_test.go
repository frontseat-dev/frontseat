package populate

import "github.com/frontseat-dev/frontseat/server/internal/source/record"

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/source/internal/vcs/git"
	"github.com/frontseat-dev/frontseat/server/testsupport/testcommits"
	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
)

// remote makes a repository with two directories and returns its URL.
func remote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "services")
	testfiles.Tree(t, dir, map[string]string{
		"hello/go.mod":  "module example.com/hello\n\ngo 1.27\n",
		"greet/go.mod":  "module example.com/greet\n\ngo 1.27\n",
		"docs/guide.md": "# guide\n",
	})
	testcommits.First(t, dir)
	return "file://" + dir
}

func TestARepositoryIsClonedWholeAtItsCommit(t *testing.T) {
	workspace := t.TempDir()

	got, err := Clone(context.Background(), git.System{}, workspace, record.Checkout{Name: "services", Remote: remote(t), Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Commit) != 40 || got.Name != "services" {
		t.Errorf("cloned %+v, want a commit and the name services", got)
	}
	holdsAll(t, workspace)
}

func TestRootsMakeAPartialCheckout(t *testing.T) {
	workspace, got := partial(t)

	if strings.Join(got.Roots, ",") != "hello" {
		t.Errorf("roots = %v, want hello", got.Roots)
	}
	if _, err := os.Stat(filepath.Join(workspace, "services", "hello", "go.mod")); err != nil {
		t.Errorf("hello/go.mod is not there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "services", "greet")); !os.IsNotExist(err) {
		t.Errorf("greet is there, and only hello was asked for (%v)", err)
	}
}

func TestAPartialCheckoutIsWidenedWhenMoreOfItIsAskedFor(t *testing.T) {
	ctx := context.Background()
	workspace, held := partial(t)

	wider, err := Widen(ctx, git.System{}, workspace, held, []string{"greet", "hello"})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(wider.Roots, ",") != "greet,hello" {
		t.Errorf("roots = %v, want greet and hello", wider.Roots)
	}
	if _, err := os.Stat(filepath.Join(workspace, "services", "greet", "go.mod")); err != nil {
		t.Errorf("greet/go.mod is not there after widening: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "services", "docs")); !os.IsNotExist(err) {
		t.Errorf("docs is there, and it was never asked for (%v)", err)
	}
}

// holdsAll fails the test where a file of the test's remote is not in
// the workspace's checkout of it.
func holdsAll(t *testing.T, workspace string) {
	t.Helper()
	for _, file := range []string{"hello/go.mod", "greet/go.mod", "docs/guide.md"} {
		if _, err := os.Stat(filepath.Join(workspace, "services", file)); err != nil {
			t.Errorf("%s is not there: %v", file, err)
		}
	}
}

// partial clones the test's remote, with the directory hello alone
// checked out, into a workspace of the test's, and returns the workspace
// and what was cloned.
func partial(t *testing.T) (string, record.Repository) {
	t.Helper()
	workspace := t.TempDir()
	held, err := Clone(context.Background(), git.System{}, workspace, record.Checkout{Name: "services", Remote: remote(t), Roots: []string{"hello"}})
	if err != nil {
		t.Fatal(err)
	}
	return workspace, held
}

// empty makes a workspace's directory with nothing in it or beside it,
// and returns it.
func empty(t *testing.T) string {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	return workspace
}

// beside returns what a directory's parent holds besides it.
func beside(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.Name() != filepath.Base(dir) {
			names = append(names, e.Name())
		}
	}
	return names
}

// A checkout is named by whoever asks: a request, or a catalog. Its name is joined to the workspace's directory, and
// its remote and commit are handed to git.
func TestACheckoutAWorkspaceCannotHoldIsNotCloned(t *testing.T) {
	from := remote(t)
	for name, c := range map[string]record.Checkout{
		"a name out of the workspace":          {Name: "../elsewhere", Remote: from},
		"a name that is a path":                {Name: "a/b", Remote: from},
		"no name":                              {Remote: from},
		"the workspace itself":                 {Name: ".", Remote: from},
		"where the workspace keeps its record": {Name: ".frontseat", Remote: from},
		"a directory out of the repository":    {Name: "services", Remote: from, Roots: []string{"../other"}},
		"a directory by its full path":         {Name: "services", Remote: from, Roots: []string{"/etc"}},
		"no remote":                            {Name: "services"},
	} {
		t.Run(name, func(t *testing.T) {
			workspace := empty(t)

			_, err := Clone(context.Background(), git.System{}, workspace, c)

			if err == nil {
				t.Error("it was cloned")
			}
			if made := beside(t, workspace); len(made) > 0 {
				t.Errorf("%v was made beside the workspace", made)
			}
			if entries, _ := os.ReadDir(workspace); len(entries) > 0 {
				t.Errorf("the workspace holds %v, and nothing was to be cloned", entries)
			}
		})
	}
}

// Git reads an argument that starts with a dash as an option, and one
// of its options names a program to run.
func TestARemoteIsNeverReadAsAnOption(t *testing.T) {
	workspace := empty(t)
	written := filepath.Join(filepath.Dir(workspace), "written")
	// The directory the clone is made in is a repository, so that git,
	// taking the remote for an option, takes the directory for the remote.
	held, err := Clone(context.Background(), git.System{}, workspace, record.Checkout{Name: "services", Remote: remote(t)})
	if err != nil {
		t.Fatal(err)
	}

	_, err = Clone(context.Background(), git.System{}, filepath.Join(workspace, held.Name), record.Checkout{Name: ".git", Remote: "--upload-pack=touch " + written + ";"})

	if err == nil {
		t.Error("it was cloned")
	}
	if _, err := os.Stat(written); err == nil {
		t.Error("the remote was read as an option, and what it named was run")
	}
}

func TestAPartialCheckoutIsWidenedOnlyWithDirectoriesOfItsRepository(t *testing.T) {
	ctx := context.Background()
	workspace, held := partial(t)

	if _, err := Widen(ctx, git.System{}, workspace, held, []string{"../other"}); err == nil {
		t.Error("it was widened with a directory out of the repository")
	}
	if _, err := Widen(ctx, git.System{}, workspace, held, []string{"greet"}); err != nil {
		t.Errorf("it was not widened with a directory of the repository: %v", err)
	}
}

// All of a repository is asked for by naming no directory of it. A
// partial checkout that is asked for whole becomes whole.
func TestAPartialCheckoutAskedForWholeBecomesWhole(t *testing.T) {
	ctx := context.Background()
	workspace, held := partial(t)
	if _, err := os.Stat(filepath.Join(workspace, "services", "greet")); !os.IsNotExist(err) {
		t.Fatalf("greet is there before all of it was asked for, so this test proves nothing (%v)", err)
	}

	whole, err := Widen(ctx, git.System{}, workspace, held, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(whole.Roots) != 0 {
		t.Errorf("it is recorded with the directories %v, want all of it", whole.Roots)
	}
	holdsAll(t, workspace)
}

// A clone that fails leaves nothing where it was to be, so that it can
// be asked for again; and it takes away nothing that was there before.
func TestACloneThatFailsLeavesNothing(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	from := remote(t)

	if _, err := Clone(ctx, git.System{}, workspace, record.Checkout{Name: "services", Remote: from, Ref: "no-such-branch"}); err == nil {
		t.Fatal("a clone of a branch the remote does not have succeeded")
	}
	if entries, _ := os.ReadDir(workspace); len(entries) != 0 {
		t.Errorf("the clone that failed left %v", entries)
	}

	if _, err := Clone(ctx, git.System{}, workspace, record.Checkout{Name: "services", Remote: from}); err != nil {
		t.Fatalf("it could not be cloned after the clone that failed: %v", err)
	}
	if _, err := Clone(ctx, git.System{}, workspace, record.Checkout{Name: "services", Remote: from}); err == nil {
		t.Error("it was cloned where a repository already was")
	}
	if _, err := os.Stat(filepath.Join(workspace, "services", "hello", "go.mod")); err != nil {
		t.Errorf("the repository that was there is gone after a clone into its place failed: %v", err)
	}
}
