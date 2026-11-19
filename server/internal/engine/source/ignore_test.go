package source_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/engine/source"
)

// withExamples is a repository with modules the ignore file is written
// for: a broken one beside a kept one, a broken one deeper down, notes,
// and a file in a dot directory.
func withExamples(t *testing.T) source.Tree {
	t.Helper()
	tree := repository(t)
	for _, name := range []string{"examples/broken/go.mod", "examples/kept/go.mod", "deep/broken/x", "deep/other/x", "notes.md", ".hidden/y"} {
		write(t, filepath.Join(tree.Root, filepath.FromSlash(name)), name)
	}
	return tree
}

func everything(t *testing.T, snapshot *source.Snapshot) []string {
	t.Helper()
	names, err := snapshot.Glob(context.Background(), "", []string{"**"})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func found(t *testing.T, snapshot *source.Snapshot, name string) bool {
	t.Helper()
	_, there, err := snapshot.Read(context.Background(), "", name)
	if err != nil {
		t.Fatal(err)
	}
	return there
}

func TestWithoutAnIgnoreFileEveryFileIsOfTheRepository(t *testing.T) {
	snapshot := taken(t, withExamples(t))
	want := []string{"deep/broken/x", "deep/other/x", "examples/broken/go.mod", "examples/kept/go.mod", "inside", "notes.md"}
	if got := everything(t, snapshot); !reflect.DeepEqual(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	if !found(t, snapshot, "examples/broken/go.mod") {
		t.Fatal("examples/broken/go.mod is not read, with no ignore file")
	}
}

func TestWhatTheIgnoreFileNamesIsNotOfTheRepository(t *testing.T) {
	tree := withExamples(t)
	// Anchored and directory-only, with the kept one let back in; a
	// name at any depth; a suffix; a blank line and a comment.
	write(t, filepath.Join(tree.Root, source.IgnoreFile), "/examples/*/\n!/examples/kept/\n\n# a comment\nbroken/\n*.md\n")
	snapshot := taken(t, tree)
	want := []string{source.IgnoreFile, "deep/other/x", "examples/kept/go.mod", "inside"}
	if got := everything(t, snapshot); !reflect.DeepEqual(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	for name, there := range map[string]bool{
		"examples/broken/go.mod": false,
		"deep/broken/x":          false,
		"notes.md":               false,
		"examples/kept/go.mod":   true,
		"inside":                 true,
		source.IgnoreFile:        true,
	} {
		if got := found(t, snapshot, name); got != there {
			t.Errorf("%s read: found %v, want %v", name, got, there)
		}
	}
}

// What git keeps of these files under each ignore file, by
// `git ls-files --others --exclude-standard` on the same files: a
// pattern by name is held to a name and never lets a path back in by
// matching a directory above it, and a directory once excluded is not
// looked beneath.
func TestTheIgnoreFileIsReadAsGitReadsIts(t *testing.T) {
	for _, c := range []struct {
		ignore string
		kept   []string
	}{
		// Everything but the directories and the Go files: the usual
		// "ignore all except" idiom.
		{"*\n!*/\n!go.mod\n!*.go\n", []string{"examples/broken/go.mod", "examples/kept/go.mod"}},
		// A directory excluded by name is not let back in beneath.
		{"*\n!examples/\n", nil},
		{"examples/\n!examples/kept/\n", []string{".frontseatignore", "deep/broken/x", "deep/other/x", "inside", "notes.md"}},
		// A file by name, at any depth; a path, anchored.
		{"x\n/notes.md\n", []string{".frontseatignore", "examples/broken/go.mod", "examples/kept/go.mod", "inside"}},
		{"**/broken/**\n", []string{".frontseatignore", "deep/other/x", "examples/kept/go.mod", "inside", "notes.md"}},
	} {
		tree := withExamples(t)
		write(t, filepath.Join(tree.Root, source.IgnoreFile), c.ignore)
		got := everything(t, taken(t, tree))
		if len(got) == 0 && len(c.kept) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.kept) {
			t.Errorf("%q listed %v, want %v", c.ignore, got, c.kept)
		}
	}
}

func TestAFileBeneathAnExcludedDotDirectoryIsNotReadByName(t *testing.T) {
	tree := withExamples(t)
	write(t, filepath.Join(tree.Root, ".github/kept/f"), "kept")
	write(t, filepath.Join(tree.Root, ".github/other/f"), "other")
	write(t, filepath.Join(tree.Root, source.IgnoreFile), ".github/\n!.github/kept/\n")
	snapshot := taken(t, tree)
	for _, name := range []string{".github/kept/f", ".github/other/f"} {
		if found(t, snapshot, name) {
			t.Errorf("%s is read by name beneath an excluded directory", name)
		}
	}
	if !found(t, snapshot, ".hidden/y") {
		t.Fatal("a dot directory the ignore file does not name is no longer read by name")
	}
}

func TestAnIgnoreFileOfOnlyCommentsIgnoresNothing(t *testing.T) {
	tree := withExamples(t)
	write(t, filepath.Join(tree.Root, source.IgnoreFile), "# nothing\n\n")
	if !found(t, taken(t, tree), "examples/broken/go.mod") {
		t.Fatal("a file of comments ignored something")
	}
}

func TestAnIgnoreFileThatLeavesTheRepositoryIsRefused(t *testing.T) {
	tree := withExamples(t)
	if err := os.Symlink(filepath.Join(filepath.Dir(tree.Root), "outside"), filepath.Join(tree.Root, source.IgnoreFile)); err != nil {
		t.Fatal(err)
	}
	_, err := new(source.Keeper).Take(context.Background(), tree)
	if err == nil || !strings.Contains(err.Error(), source.IgnoreFile) {
		t.Fatalf("a link out of the repository was taken as its ignore file: %v", err)
	}
}

func TestAnIgnoreFileThatIsADirectoryIsRefused(t *testing.T) {
	tree := withExamples(t)
	if err := os.Mkdir(filepath.Join(tree.Root, source.IgnoreFile), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := new(source.Keeper).Take(context.Background(), tree)
	if err == nil || !strings.Contains(err.Error(), "not a file") {
		t.Fatalf("a directory was taken as the ignore file: %v", err)
	}
}

// Of paths of a repository, those a run would read are told apart from
// those it would not: what the ignore file names, and what is in a
// directory whose name begins with a dot.
func TestWhichPathsARunWouldReadIsSaid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, source.IgnoreFile), []byte("/target/\n*.secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := source.Seen(dir, []string{
		"gen.go", "greet/gen.go", "out/", "target/", "target/debug/x", "a.secret", "greet/b.secret",
		".cache/", ".idea/workspace.xml", "greet/.env", "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "gen.go|greet/gen.go|out/|greet/.env"; strings.Join(got, "|") != want {
		t.Errorf("a run would read %q, want %q", got, want)
	}
	// With no ignore file, everything but what is hidden is read.
	bare := t.TempDir()
	if got, err := source.Seen(bare, []string{"target/", ".git/x", "a.secret"}); err != nil || strings.Join(got, "|") != "target/|a.secret" {
		t.Errorf("with no ignore file a run would read %q, %v", got, err)
	}
	if _, err := source.Seen(filepath.Join(bare, "nowhere"), []string{"a"}); err == nil {
		t.Error("a directory that is not there says what a run would read")
	}
}
