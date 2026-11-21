package git_test

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	rungit "github.com/frontseat-dev/frontseat/server/git"
	"github.com/frontseat-dev/frontseat/server/internal/source/internal/vcs/git"
	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
)

func TestARepositoryGivenNoNameIsNamedAsGitNamesItsDirectory(t *testing.T) {
	for remote, want := range map[string]string{
		"https://example.com/acme/services.git":  "services",
		"https://example.com/acme/services":      "services",
		"https://example.com/acme/services/":     "services",
		"https://example.com/acme/services/.git": "services",
		"ssh://git@example.com/acme/services":    "services",
		"git@example.com:acme/services.git":      "services",
		"git@example.com:services.git":           "services",
		"file:///remotes/services":               "services",
		"/remotes/services":                      "services",
		"file:///":                               "",
		"":                                       "",
	} {
		if got := (git.System{}).NameOf(remote); got != want {
			t.Errorf("NameOf(%q) = %q, want %q", remote, got, want)
		}
	}
}

// A revision is a commit's full hash and nothing git would read as
// anything else: a branch, a path, an option.
func TestARevisionIsACommitsFullHash(t *testing.T) {
	for name, sound := range map[string]bool{
		strings.Repeat("a", 40):               true,
		strings.Repeat("0", 64):               true,
		"":                                    false,
		"main":                                false,
		"HEAD":                                false,
		strings.Repeat("a", 39):               false,
		strings.Repeat("A", 40):               false,
		strings.Repeat("a", 39) + "\n":        false,
		"--upload-pack=touch /tmp/owned":      false,
		"-" + strings.Repeat("a", 39):         false,
		strings.Repeat("a", 36) + "^{/x}":     false,
		strings.Repeat("a", 20) + ".." + "aa": false,
	} {
		if err := (git.System{}).Revision(name); (err == nil) != sound {
			t.Errorf("Revision(%q) = %v, want sound to be %v", name, err, sound)
		}
	}
}

// run runs git in a directory, as whoever made the repository would.
func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// remote is a repository with two commits on main: hello and greet, a
// file to change, one to delete, and a rule for what is ignored.
func remote(t *testing.T) (url, first, second string) {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "--quiet", "--initial-branch", "main")
	write(t, dir, "hello/main.go", "package main\n")
	write(t, dir, "greet/greet.go", "package greet\n")
	write(t, dir, "hello/gone.txt", "to be deleted\n")
	write(t, dir, ".gitignore", "*.secret\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "--quiet", "-m", "first")
	first = run(t, dir, "rev-parse", "HEAD")
	write(t, dir, "hello/later.txt", "later\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "--quiet", "-m", "second")
	return "file://" + dir, first, run(t, dir, "rev-parse", "HEAD")
}

func TestACloneStandsAtTheTipOfWhatWasAsked(t *testing.T) {
	url, _, second := remote(t)

	at, err := git.System{}.Clone(context.Background(), filepath.Join(t.TempDir(), "tip"), vcs.Source{Remote: url, Ref: "main"})

	if err != nil || at != second {
		t.Fatalf("the clone stands at %q, %v; want the tip %s", at, err, second)
	}
}

func TestAPartialCloneHoldsItsRootsAndIsWidened(t *testing.T) {
	ctx := context.Background()
	url, _, _ := remote(t)
	dir := filepath.Join(t.TempDir(), "partial")
	if _, err := (git.System{}).Clone(ctx, dir, vcs.Source{Remote: url, Roots: []string{"hello"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "greet")); err == nil {
		t.Fatal("a clone of hello alone holds greet")
	}

	if err := (git.System{}).Widen(ctx, dir, url, []string{"greet"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "greet/greet.go")); err != nil {
		t.Errorf("widened by greet: %v", err)
	}
}

// A remote written as a path is found from the directory the clone is
// made in, as a person who ran git there would find it.
func TestARemoteThatIsAPathIsFoundFromWhereTheCloneIsMade(t *testing.T) {
	url, _, second := remote(t)
	workspace := filepath.Join(filepath.Dir(strings.TrimPrefix(url, "file://")), "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	at, err := git.System{}.Clone(context.Background(), filepath.Join(workspace, "services"), vcs.Source{Remote: "../" + filepath.Base(strings.TrimPrefix(url, "file://"))})

	if err != nil || at != second {
		t.Errorf("the clone stands at %q, %v; want %s", at, err, second)
	}
}

// history is a repository with a history to ask of: three commits on
// main, the second tagged, then a branch with one more, and a tag on a
// commit that is on no branch the repository stands on.
func history(t *testing.T) (dir string, commits map[string]string) {
	t.Helper()
	dir = t.TempDir()
	commits = map[string]string{}
	run(t, dir, "init", "--quiet", "--initial-branch", "main")
	commit := func(name, message string, files ...string) {
		for _, file := range files {
			write(t, dir, file, name+"\n")
		}
		run(t, dir, "add", "--all")
		run(t, dir, "-c", "user.name=someone", "-c", "user.email=someone@example.com", "commit", "--quiet", "-m", message)
		commits[name] = run(t, dir, "rev-parse", "HEAD")
	}
	commit("first", "feat: begin", "greet/greet.go", "log/log.go")
	commit("second", "fix: trim the greeting\n\nIt was long.", "greet/greet.go")
	run(t, dir, "tag", "greet/v1.0.0")
	run(t, dir, "-c", "user.name=someone", "-c", "user.email=someone@example.com", "tag", "--annotate", "-m", "the log", "log/v1.0.0", commits["first"])
	commit("third", "docs: say more", "log/log.go")
	run(t, dir, "switch", "--quiet", "--create", "elsewhere")
	commit("aside", "fix: somewhere else", "greet/greet.go")
	run(t, dir, "tag", "greet/v9.9.9")
	run(t, dir, "switch", "--quiet", "main")
	run(t, dir, "switch", "--quiet", "--create", "release/1.0")
	commit("fourth", "feat: greet by name", "greet/greet.go")
	return dir, commits
}

func TestWhereARepositoryStands(t *testing.T) {
	dir, commits := history(t)
	ctx := context.Background()
	got, err := (git.System{}).Standing(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	// When a commit was made is the commit's own, as git says it.
	committed := func(revision string) int64 {
		t.Helper()
		at, err := strconv.ParseInt(run(t, dir, "show", "--no-patch", "--format=%ct", revision), 10, 64)
		if err != nil || at == 0 {
			t.Fatalf("%s was committed at %d: %v", revision, at, err)
		}
		return at
	}
	// It was not brought from anywhere, so no default branch is known.
	if want := (vcs.Standing{Branch: "release/1.0", Revision: commits["fourth"], Time: committed(commits["fourth"])}); got != want {
		t.Errorf("Standing = %+v, want %+v", got, want)
	}
	// A clone knows the default branch of where it was brought from,
	// whatever branch it is on.
	// which is the branch that one stands on.
	run(t, dir, "switch", "--quiet", "main")
	clone := filepath.Join(t.TempDir(), "clone")
	run(t, filepath.Dir(clone), "clone", "--quiet", "--branch", "release/1.0", dir, clone)
	got, err = (git.System{}).Standing(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if want := (vcs.Standing{Branch: "release/1.0", Revision: commits["fourth"], DefaultBranch: "main", Time: committed(commits["fourth"])}); got != want {
		t.Errorf("Standing of a clone = %+v, want %+v", got, want)
	}
	// On no branch, it stands at a revision alone.
	run(t, dir, "switch", "--quiet", "--detach", commits["second"])
	got, err = (git.System{}).Standing(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := (vcs.Standing{Revision: commits["second"], Time: committed(commits["second"])}); got != want {
		t.Errorf("Standing on no branch = %+v, want %+v", got, want)
	}
	if _, err := (git.System{}).Standing(ctx, t.TempDir()); err == nil {
		t.Error("a directory that is no repository stands somewhere")
	}
}

func TestTheTagsOfARepositoryAndWhichAreInItsHistory(t *testing.T) {
	dir, commits := history(t)
	got, err := (git.System{}).Tags(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []vcs.Tag{
		{Name: "greet/v1.0.0", Revision: commits["second"], Reachable: true},
		{Name: "greet/v9.9.9", Revision: commits["aside"], Reachable: false},
		// A tag with a message names the commit it was made of, not itself.
		{Name: "log/v1.0.0", Revision: commits["first"], Reachable: true},
	}
	if len(got) != len(want) {
		t.Fatalf("Tags = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tag %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	empty := t.TempDir()
	run(t, empty, "init", "--quiet")
	if got, err := (git.System{}).Tags(context.Background(), empty); err != nil || len(got) != 0 {
		t.Errorf("a repository with no tag has %+v, %v", got, err)
	}
}

func TestTheChangesOfDirectoriesSinceARevision(t *testing.T) {
	dir, commits := history(t)
	ctx := context.Background()
	revisions := func(changes []vcs.Change) []string {
		var out []string
		for _, c := range changes {
			out = append(out, c.Revision)
		}
		return out
	}
	for name, c := range map[string]struct {
		since       string
		directories []string
		want        []string
	}{
		"a directory, from the beginning": {"", []string{"greet"}, []string{commits["first"], commits["second"], commits["fourth"]}},
		"a directory, since its tag":      {commits["second"], []string{"greet"}, []string{commits["fourth"]}},
		"another directory":               {commits["first"], []string{"log"}, []string{commits["third"]}},
		"two directories":                 {commits["first"], []string{"log", "greet"}, []string{commits["second"], commits["third"], commits["fourth"]}},
		"nothing since where it stands":   {commits["fourth"], []string{"greet"}, nil},
		"a directory nothing is in":       {"", []string{"nowhere"}, nil},
		// What is elsewhere, and not in this history, is no change of it.
		"since a revision elsewhere": {commits["aside"], []string{"greet"}, []string{commits["fourth"]}},
	} {
		got, err := (git.System{}).Changes(ctx, dir, c.since, c.directories)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Join(revisions(got), " ") != strings.Join(c.want, " ") {
			t.Errorf("%s: changes are %v, want %v", name, revisions(got), c.want)
		}
	}
	got, err := (git.System{}).Changes(ctx, dir, commits["first"], []string{"greet"})
	if err != nil || len(got) != 2 {
		t.Fatalf("changes = %+v, %v", got, err)
	}
	if got[0].Message != "fix: trim the greeting\n\nIt was long." || got[1].Message != "feat: greet by name" || got[0].Time == 0 {
		t.Errorf("a change's message and time are not its own: %+v", got)
	}
}

// A directory is a name: one that reads as a pattern names itself, and
// never its neighbours.
func TestADirectoryAskedOfIsANameAndNoPattern(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "--quiet", "--initial-branch", "main")
	for _, file := range []string{"a*/x.go", "ab/x.go", "pkg[1]/x.go", "pkg1/x.go", ":(top)/x.go", "top/x.go"} {
		write(t, dir, file, "x\n")
		run(t, dir, "add", "--all")
		run(t, dir, "-c", "user.name=someone", "-c", "user.email=someone@example.com", "commit", "--quiet", "-m", "add "+file)
	}
	for _, directory := range []string{"a*", "pkg[1]", ":(top)", "ab"} {
		got, err := (git.System{}).Changes(context.Background(), dir, "", []string{directory})
		if err != nil {
			t.Errorf("%s: %v", directory, err)
			continue
		}
		if len(got) != 1 || got[0].Message != "add "+directory+"/x.go" {
			t.Errorf("the changes of %q are %+v", directory, got)
		}
	}
}

// What is asked with is checked before git reads it: a revision is a
// full hash, and a directory is in the repository.
func TestWhatHistoryIsAskedWithIsCheckedFirst(t *testing.T) {
	dir, commits := history(t)
	ctx := context.Background()
	if _, err := (git.System{}).Changes(ctx, dir, commits["first"], []string{"greet"}); err != nil {
		t.Fatalf("a sound question was refused, so this test proves nothing: %v", err)
	}
	canary := filepath.Join(t.TempDir(), "owned")
	for name, c := range map[string]struct {
		since       string
		directories []string
	}{
		"a branch for a revision":      {"main", []string{"greet"}},
		"an option for a revision":     {"--output=" + canary, []string{"greet"}},
		"a range for a revision":       {commits["first"][:20] + ".." + commits["second"][:18], []string{"greet"}},
		"no directory":                 {commits["first"], nil},
		"an empty directory":           {commits["first"], []string{""}},
		"a directory above":            {commits["first"], []string{"../elsewhere"}},
		"a directory by its full path": {commits["first"], []string{dir}},
		"a directory written two ways": {commits["first"], []string{"greet/../greet"}},
	} {
		if _, err := (git.System{}).Changes(ctx, dir, c.since, c.directories); err == nil {
			t.Errorf("%s was asked of git", name)
		}
	}
	// A directory may be named as an option is written: it is a name
	// still, of a directory nothing is in.
	if got, err := (git.System{}).Changes(ctx, dir, commits["first"], []string{"--output=" + canary}); err != nil || len(got) != 0 {
		t.Errorf("a directory named as an option is written: %+v, %v", got, err)
	}
	if _, err := os.Stat(canary); err == nil {
		t.Error("git wrote what an option named")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := (git.System{}).Changes(cancelled, dir, "", []string{"greet"}); err == nil {
		t.Error("a cancelled question was answered")
	}
}

// A tag's name is held to git's own rule, by git.
func TestATagNameIsOneGitTakes(t *testing.T) {
	ctx := context.Background()
	for name, sound := range map[string]bool{
		"greet/v1.26.0":       true,
		"v0.77.0":             true,
		"retail-kiosk-1.0.0":  true,
		"greet/v1.0.0+build1": true,
		"":                    false,
		".a/v1.0.0":           false,
		"a.lock/v1.0.0":       false,
		"x/.b/1.0.0":          false,
		"x.lock/v1.0.0":       false,
		"a..b/v1.0.0":         false,
		"v1.0.0 final":        false,
		"v1.0.0/":             false,
		"v1.0.0.lock":         false,
		"--force/1.0.0":       true,
		"-x":                  true,
		"v1.0.0~1":            false,
		"v1.0.0^":             false,
		"v1:0":                false,
		"v1\n2":               false,
		"v1\x002":             false,
		"@{now}":              false,
	} {
		if err := (git.System{}).TagName(ctx, name); (err == nil) != sound {
			t.Errorf("TagName(%q) = %v, want sound to be %v", name, err, sound)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := (git.System{}).TagName(cancelled, "v1.0.0"); err == nil {
		t.Error("a cancelled question was answered")
	}
}

func TestWhatIsNotCommittedIsSaid(t *testing.T) {
	dir, _ := history(t)
	ctx := context.Background()
	if got, err := (git.System{}).Uncommitted(ctx, dir); err != nil || len(got) != 0 {
		t.Fatalf("a repository that is its revision has %v uncommitted, %v", got, err)
	}
	write(t, dir, "greet/greet.go", "changed\n")
	write(t, dir, "new dir/a new file.txt", "new\n")
	write(t, dir, "staged.txt", "staged\n")
	run(t, dir, "add", "staged.txt")
	if err := os.Remove(filepath.Join(dir, "log/log.go")); err != nil {
		t.Fatal(err)
	}
	got, err := (git.System{}).Uncommitted(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"greet/greet.go", "log/log.go", "new dir/a new file.txt", "staged.txt"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("uncommitted are %q, want %q", got, want)
	}
	// What the repository says to ignore is not its to commit.
	clean, _ := history(t)
	write(t, clean, ".gitignore", "*.secret\n")
	run(t, clean, "add", ".gitignore")
	run(t, clean, "commit", "--quiet", "-m", "ignore secrets")
	write(t, clean, "a.secret", "x\n")
	if got, err := (git.System{}).Uncommitted(ctx, clean); err != nil || len(got) != 0 {
		t.Errorf("an ignored file is uncommitted: %v, %v", got, err)
	}
	// It is there all the same, and said so by itself.
	write(t, clean, "out/built.bin", "x\n")
	write(t, clean, ".git/info/exclude", "out/\n")
	if got, err := (git.System{}).Ignored(ctx, clean); err != nil || strings.Join(got, "|") != "a.secret|out/" {
		t.Errorf("ignored are %q, %v", got, err)
	}
	if got, err := (git.System{}).Ignored(ctx, dir); err != nil || len(got) != 0 {
		t.Errorf("a repository that ignores nothing has %q ignored, %v", got, err)
	}
}

// What git is told not to look at cannot be said to be as its commit has
// it: git status says nothing of it, and a build reads it.
func TestWhatGitIsToldNotToLookAtIsNotSaidToBeCommitted(t *testing.T) {
	ctx := context.Background()
	for name, hide := range map[string]func(t *testing.T, dir string){
		"a file assumed unchanged": func(t *testing.T, dir string) {
			run(t, dir, "update-index", "--assume-unchanged", "greet/greet.go")
			write(t, dir, "greet/greet.go", "changed\n")
		},
		"a file to be skipped that is there": func(t *testing.T, dir string) {
			run(t, dir, "update-index", "--skip-worktree", "greet/greet.go")
			write(t, dir, "greet/greet.go", "changed\n")
		},
		"a mode the repository says to overlook": func(t *testing.T, dir string) {
			run(t, dir, "config", "core.fileMode", "false")
			if err := os.Chmod(filepath.Join(dir, "greet/greet.go"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	} {
		dir, _ := history(t)
		hide(t, dir)
		if raw := run(t, dir, "status", "--porcelain"); raw != "" {
			t.Fatalf("%s: git status says %q of it, so this test proves nothing", name, raw)
		}
		got, err := (git.System{}).Uncommitted(ctx, dir)
		if err != nil || strings.Join(got, "|") != "greet/greet.go" {
			t.Errorf("%s: uncommitted are %q, %v", name, got, err)
		}
	}
	// A file to be skipped that is not there, as a sparse checkout leaves
	// it, is as its commit has it: it is not there to be read.
	dir, _ := history(t)
	run(t, dir, "update-index", "--skip-worktree", "log/log.go")
	if err := os.Remove(filepath.Join(dir, "log/log.go")); err != nil {
		t.Fatal(err)
	}
	if got, err := (git.System{}).Uncommitted(ctx, dir); err != nil || len(got) != 0 {
		t.Errorf("a file a sparse checkout left out is uncommitted: %q, %v", got, err)
	}
}

func TestATagNamesARevisionOnceAndNothingElseIsChanged(t *testing.T) {
	dir, commits := history(t)
	ctx := context.Background()
	system := git.System{}
	named := func(tag string) string { return run(t, dir, "rev-parse", "refs/tags/"+tag+"^{commit}") }
	if err := system.Tag(ctx, dir, "greet/v1.1.0", commits["fourth"]); err != nil {
		t.Fatal(err)
	}
	if named("greet/v1.1.0") != commits["fourth"] {
		t.Fatalf("the tag names %s", named("greet/v1.1.0"))
	}
	// Again, of the same revision, it is left as it is.
	if err := system.Tag(ctx, dir, "greet/v1.1.0", commits["fourth"]); err != nil {
		t.Errorf("a tag that names the revision already was refused: %v", err)
	}
	// A name read as an option is a name.
	if err := system.Tag(ctx, dir, "--force/1.0.0", commits["first"]); err != nil || named("--force/1.0.0") != commits["first"] {
		t.Errorf("a tag named as an option is written: %v", err)
	}
	for name, c := range map[string][2]string{
		"a tag that names another revision":       {"greet/v1.0.0", commits["fourth"]},
		"a tag with a message that names another": {"log/v1.0.0", commits["fourth"]},
		"a name git does not take":                {"a..b", commits["fourth"]},
		"no name":                                 {"", commits["fourth"]},
		"a branch for a revision":                 {"greet/v2.0.0", "main"},
		"an option for a revision":                {"greet/v2.0.0", "--force"},
		"a revision the repository has not":       {"greet/v2.0.0", strings.Repeat("a", 40)},
	} {
		before := run(t, dir, "for-each-ref", "refs/tags")
		if err := system.Tag(ctx, dir, c[0], c[1]); err == nil {
			t.Errorf("%s was written", name)
		}
		if after := run(t, dir, "for-each-ref", "refs/tags"); after != before {
			t.Errorf("%s changed the tags", name)
		}
	}
	// An annotated tag that names the same commit is that tag already.
	if err := system.Tag(ctx, dir, "log/v1.0.0", commits["first"]); err != nil {
		t.Errorf("a tag with a message that names the revision already was refused: %v", err)
	}
	// Two at once: one writes, and neither fails where they agree.
	errs := make(chan error, 8)
	for range 8 {
		go func() { errs <- system.Tag(ctx, dir, "greet/v3.0.0", commits["third"]) }()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Errorf("tagging twice at once: %v", err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := system.Tag(cancelled, dir, "greet/v4.0.0", commits["third"]); err == nil {
		t.Error("a cancelled tag was written")
	}
}

// What git says of a fetch from a sandbox: the two requests of a fetch
// over smart HTTP and no push, and settings that present a username and,
// as the password, what a variable holds, told beside what git was told
// already.
func TestWhatAFetchFromASandboxNeedsIsSaid(t *testing.T) {
	asked := git.System{}.Requests("/someone/code.git")
	if len(asked) != 2 ||
		asked[0].Method != "GET" || asked[0].Path != "/someone/code.git/info/refs" || asked[0].Query["service"] != "git-upload-pack" ||
		asked[1].Method != "POST" || asked[1].Path != "/someone/code.git/git-upload-pack" || len(asked[1].Query) != 0 {
		t.Fatalf("a fetch asks %+v", asked)
	}
	for _, each := range asked {
		if strings.Contains(each.Path, "receive-pack") || strings.Contains(each.Query["service"], "receive-pack") {
			t.Fatalf("a fetch pushes: %+v", each)
		}
	}
	// A push asks its own two, said apart, and they are none of a fetch's.
	pushed := git.System{}.Pushes("/someone/code.git")
	if len(pushed) != 2 ||
		pushed[0].Method != "GET" || pushed[0].Path != "/someone/code.git/info/refs" || pushed[0].Query["service"] != "git-receive-pack" ||
		pushed[1].Method != "POST" || pushed[1].Path != "/someone/code.git/git-receive-pack" || len(pushed[1].Query) != 0 {
		t.Fatalf("a push asks %+v", pushed)
	}
	if programs := (git.System{}).Programs(); len(programs) != 1 || programs[0] != "/usr/bin/git" {
		t.Fatalf("the programs that fetch are %v", programs)
	}
	own := git.System{}.Tells()
	if own != "GIT_CONFIG_" {
		t.Fatalf("git's own variables begin with %q", own)
	}

	told, err := git.System{}.Presenting(map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "user.name", "GIT_CONFIG_VALUE_0": "someone"}, "https://forge.example:443", "x-access-token", "A_VARIABLE")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"GIT_CONFIG_COUNT":   "3",
		"GIT_CONFIG_KEY_1":   "credential.https://forge.example:443.username",
		"GIT_CONFIG_VALUE_1": "x-access-token",
		"GIT_CONFIG_KEY_2":   "credential.https://forge.example:443.helper",
		"GIT_CONFIG_VALUE_2": `!f() { printf '%s\n' "password=$A_VARIABLE"; }; f`,
	}
	if len(told) != len(want) {
		t.Fatalf("git is told %v, want %v", told, want)
	}
	for name, value := range want {
		if told[name] != value {
			t.Errorf("%s = %q, want %q", name, told[name], value)
		}
		// Everything it tells is its own, as it says.
		if !strings.HasPrefix(name, own) {
			t.Errorf("git is told %s, which does not begin as it says its own do (%s)", name, own)
		}
	}
	// What was told before is kept: nothing of it is told again.
	if _, again := told["GIT_CONFIG_KEY_0"]; again {
		t.Errorf("what git was told already is told again: %v", told)
	}
	if _, err := (git.System{}).Presenting(map[string]string{"GIT_CONFIG_COUNT": "many"}, "https://forge.example:443", "someone", "A_VARIABLE"); err == nil {
		t.Errorf("a count that is none was taken")
	}
}

// signer stands in for gpg: it says it ran, and signs anything with a
// signature git takes as one, which it verifies in no way.
func signer(t *testing.T, ran string) string {
	t.Helper()
	program := filepath.Join(t.TempDir(), "gpg")
	write(t, filepath.Dir(program), "gpg", "#!/bin/sh\necho gpg >> "+ran+"\ncat >/dev/null\n"+
		"echo '[GNUPG:] SIG_CREATED D 1 8 00 1 X' >&2\n"+
		"printf -- '-----BEGIN PGP SIGNATURE-----\\n\\nAAAA\\n-----END PGP SIGNATURE-----\\n'\n")
	if err := os.Chmod(program, 0o755); err != nil {
		t.Fatal(err)
	}
	return program
}

// plainly runs git as a person would, with none of the hardening and the
// environment of the test, which holds what a sandbox was told; what it
// fails at is the control's to read, by what it ran.
func plainly(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	_ = cmd.Run()
}

// said is what the planted programs said they ran, a line each.
func said(t *testing.T, ran string) []string {
	t.Helper()
	out, err := os.ReadFile(ran)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(out))
}

// What the daemon asks of a repository runs nothing the repository's
// configuration names: a filter that would be run to say whether a file
// changed is not, a file monitor is not, a hook that would be run as a
// tag is written is not, and the program that verifies a signature,
// which logging the history would run, is not. The control is git
// itself, told nothing, which runs each of them.
func TestAskingARepositoryRunsNothingItNames(t *testing.T) {
	dir, commits := history(t)
	ctx := context.Background()
	ran := filepath.Join(t.TempDir(), "ran")
	// A file git follows, which it must look at again: a filter is run to
	// say whether a file it follows changed. It is committed signed.
	write(t, dir, "unnamed", "a file\n")
	run(t, dir, "add", ".")
	run(t, dir, "-c", "gpg.program="+signer(t, ran), "commit", "--quiet", "--gpg-sign", "-m", "one more file")
	if err := os.Remove(ran); err != nil {
		t.Fatalf("the commit was not signed by the stand-in, so the test shows nothing of signatures: %v", err)
	}
	// A filter of a name, for every file; a filter of the empty name,
	// which is one too, for one file; a file monitor; a hook of the
	// repository's, which writing a reference runs; and a program that
	// verifies a signature, which the history is to be shown with.
	write(t, dir, ".gitattributes", "* filter=planted\nunnamed filter=\n")
	run(t, dir, "config", "filter.planted.clean", "sh -c 'echo filter >> "+ran+"; cat'")
	run(t, dir, "config", "filter..clean", "sh -c 'echo unnamed-filter >> "+ran+"; cat'")
	run(t, dir, "config", "core.fsmonitor", "sh -c 'echo monitor >> "+ran+"'")
	run(t, dir, "config", "log.showSignature", "true")
	run(t, dir, "config", "gpg.program", signer(t, ran))
	write(t, dir, ".git/hooks/reference-transaction", "#!/bin/sh\necho hook >> "+ran+"\n")
	if err := os.Chmod(filepath.Join(dir, ".git/hooks/reference-transaction"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Each changed to something of the same length, so that only its
	// content, which the filter is run over, says it changed.
	write(t, dir, "greet/greet.go", "FOURTH\n")
	write(t, dir, "unnamed", "A FILE\n")
	system := git.System{}
	if _, err := system.Uncommitted(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := system.Ignored(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := system.Standing(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := system.Tags(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if changes, err := system.Changes(ctx, dir, commits["fourth"], []string{"."}); err != nil || len(changes) != 1 {
		t.Fatalf("the signed commit is not the one change since the fourth: %+v, %v", changes, err)
	}
	if err := system.Tag(ctx, dir, "greet/v2.0.0", commits["fourth"]); err != nil {
		t.Fatal(err)
	}
	if ran := said(t, ran); len(ran) != 0 {
		t.Fatalf("asking the repository ran what it names: %v", ran)
	}
	// The control: asked plainly, git runs each of them.
	plainly(dir, "status", "--porcelain")
	plainly(dir, "log", "--format=%H", "-1")
	plainly(dir, "update-ref", "refs/tags/plainly", commits["fourth"])
	got := said(t, ran)
	for _, each := range []string{"filter", "unnamed-filter", "monitor", "gpg", "hook"} {
		if !slices.Contains(got, each) {
			t.Errorf("asked plainly, git did not run %q, so the test above shows nothing of it: %v", each, got)
		}
	}
}

// A repository within the repository, populated, is its own: what its
// configuration names, git asked of the repository around it would run,
// told nothing of it. It is asked nothing, and is said to be what the
// system cannot say is as its commit has it, clean or not; one that is
// not there, as a clone leaves it, is nothing.
func TestARepositoryWithinTheRepositoryRunsNothingAndIsNotSaidToBeCommitted(t *testing.T) {
	ctx := context.Background()
	ran := filepath.Join(t.TempDir(), "ran")
	inner := t.TempDir()
	run(t, inner, "init", "--quiet", "--initial-branch", "main")
	write(t, inner, "f", "inner\n")
	run(t, inner, "add", ".")
	run(t, inner, "commit", "--quiet", "-m", "inner")
	dir := t.TempDir()
	run(t, dir, "init", "--quiet", "--initial-branch", "main")
	write(t, dir, "outer", "outer\n")
	run(t, dir, "add", ".")
	run(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "--quiet", inner, "inner")
	run(t, dir, "commit", "--quiet", "-m", "outer")
	// Its own filter, of a name the repository around it does not know,
	// and its own file monitor.
	within := filepath.Join(dir, "inner")
	run(t, within, "config", "filter.its.clean", "sh -c 'echo inner-filter >> "+ran+"; cat'")
	run(t, within, "config", "core.fsmonitor", "sh -c 'echo inner-monitor >> "+ran+"'")
	write(t, run(t, within, "rev-parse", "--absolute-git-dir"), "info/attributes", "* filter=its\n")
	system := git.System{}

	if got, err := system.Uncommitted(ctx, dir); err != nil || strings.Join(got, "|") != "inner" {
		t.Errorf("a clean repository within is said to be %q uncommitted, %v; want inner", got, err)
	}
	write(t, within, "f", "INNER\n")
	if got, err := system.Uncommitted(ctx, dir); err != nil || strings.Join(got, "|") != "inner" {
		t.Errorf("a changed repository within is said to be %q uncommitted, %v; want inner", got, err)
	}
	if _, err := system.Ignored(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if ran := said(t, ran); len(ran) != 0 {
		t.Fatalf("asking the repository ran what the one within it names: %v", ran)
	}
	// The control: asked plainly, of everything, git runs them.
	plainly(dir, "status", "--porcelain", "--ignore-submodules=none")
	if got := said(t, ran); !slices.Contains(got, "inner-filter") || !slices.Contains(got, "inner-monitor") {
		t.Errorf("asked plainly, git ran %v of what the repository within names, so the test above shows nothing", got)
	}

	// A clone leaves it out, an empty directory, which is nothing.
	clone := filepath.Join(t.TempDir(), "clone")
	run(t, filepath.Dir(clone), "clone", "--quiet", dir, clone)
	if got, err := system.Uncommitted(ctx, clone); err != nil || len(got) != 0 {
		t.Errorf("a repository within that is not there is said to be %q uncommitted, %v", got, err)
	}
	// Files written where it would be are something, which git keeps
	// nowhere and says nothing of: neither uncommitted nor ignored.
	write(t, clone, "inner/written.go", "package written\n")
	write(t, clone, "inner/deeper/more", "more\n")
	if got, err := system.Uncommitted(ctx, clone); err != nil || strings.Join(got, "|") != "inner" {
		t.Errorf("files written where a repository within is not are said to be %q uncommitted, %v; want inner", got, err)
	}
	// The control: asked plainly, git says nothing of them.
	if status := run(t, clone, "status", "--porcelain", "--untracked-files=all", "--ignored"); status != "" {
		t.Errorf("asked plainly, git says %q of the files, so the test above shows nothing", status)
	}
}

// What git fetches as a sparse checkout is widened, it fetches from the
// remote it was cloned from, and none of the programs the repository's
// configuration names is run: a remote that is a command and an SSH
// client, which the repository makes origin, are not asked, since origin
// is the remote given; a program to ask the remote with, which git takes
// before any setting after it, and an address rewritten to a command are
// refused by their names; a helper for a system of the repository's
// naming is not run. A path remote that names none is fetched from. The
// control is git asked plainly, which runs each.
func TestWideningARepositoryRunsNothingItNames(t *testing.T) {
	ctx := context.Background()
	url, _, _ := remote(t)
	// Asked for no file's content, it gives none: what is checked out is
	// fetched as the clone widens.
	run(t, strings.TrimPrefix(url, "file://"), "config", "uploadpack.allowFilter", "true")
	ran := filepath.Join(t.TempDir(), "ran")
	helpers := t.TempDir()
	write(t, helpers, "git-remote-planted", "#!/bin/sh\necho remote-helper >> "+ran+"\n")
	if err := os.Chmod(filepath.Join(helpers, "git-remote-planted"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", helpers+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Nothing planted, what is checked out is fetched from the path.
	plain := filepath.Join(t.TempDir(), "plain")
	if _, err := (git.System{}).Clone(ctx, plain, vcs.Source{Remote: url, Roots: []string{"hello"}}); err != nil {
		t.Fatal(err)
	}
	if missing := run(t, plain, "rev-list", "--objects", "--all", "--missing=print"); !strings.Contains(missing, "?") {
		t.Fatal("the clone lacks no object, so nothing below was fetched as it widened")
	}
	if err := (git.System{}).Widen(ctx, plain, url, []string{"greet"}); err != nil {
		t.Fatalf("a path remote that names no program was not fetched from: %v", err)
	}
	if _, err := os.Stat(filepath.Join(plain, "greet/greet.go")); err != nil {
		t.Fatalf("widened from a path remote: %v", err)
	}
	for name, c := range map[string]struct {
		settings [][2]string
		said     string
		// answered is whether widening fetches what it was asked for all
		// the same, from the remote given; refused, the setting it is
		// refused by, if by one.
		answered bool
		refused  string
	}{
		"a remote that is a command":        {[][2]string{{"remote.origin.url", "ext::sh -c echo% ext>>" + ran}, {"protocol.ext.allow", "always"}}, "ext", true, ""},
		"a program to ask the remote with":  {[][2]string{{"remote.origin.uploadpack", "sh -c 'echo upload-pack >> " + ran + "; git-upload-pack \"$@\"' x"}}, "upload-pack", false, "remote.origin.uploadpack"},
		"an SSH client":                     {[][2]string{{"remote.origin.url", "ssh://example.invalid/x"}, {"core.sshCommand", "sh -c 'echo ssh >> " + ran + "' x"}}, "ssh", true, ""},
		"an address rewritten to a command": {[][2]string{{"url.ext::sh -c echo% rewritten>>" + ran + "% .insteadOf", "file://"}, {"protocol.ext.allow", "always"}}, "rewritten", false, "url.<address>.insteadof"},
		"a helper the repository names":     {[][2]string{{"remote.origin.vcs", "planted"}}, "remote-helper", false, ""},
	} {
		clone := func(t *testing.T) string {
			dir := filepath.Join(t.TempDir(), "clone")
			if _, err := (git.System{}).Clone(ctx, dir, vcs.Source{Remote: url, Roots: []string{"hello"}}); err != nil {
				t.Fatal(err)
			}
			for _, setting := range c.settings {
				run(t, dir, "config", setting[0], setting[1])
			}
			return dir
		}
		t.Run(name, func(t *testing.T) {
			_ = os.Remove(ran)
			dir := clone(t)
			err := git.System{}.Widen(ctx, dir, url, []string{"greet"})
			if got := said(t, ran); len(got) != 0 {
				t.Errorf("widening ran what the repository names: %v", got)
			}
			if _, fetched := os.Stat(filepath.Join(dir, "greet/greet.go")); c.answered && (err != nil || fetched != nil) {
				t.Errorf("widening did not fetch from the remote given: %v, %v", err, fetched)
			}
			if !c.answered && err == nil {
				t.Error("widening was answered")
			}
			if c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)) {
				t.Errorf("the repository is refused as %v, not by %s", err, c.refused)
			}
			// The control: asked plainly, git runs it.
			plainly(clone(t), "sparse-checkout", "add", "greet")
			if got := said(t, ran); !slices.Contains(got, c.said) {
				t.Errorf("asked plainly, git ran %v and not %q, so the test above shows nothing", got, c.said)
			}
		})
	}
}

// password is what the forge takes, with what a shell's echo would read
// as an escape that ends what it says.
const password = `se\cret`

// asked is what a server was asked, a request each: its method, what it
// was asked for as it was written (an address, where it was asked as a
// proxy), and the header planted and the cookie it was sent.
type asked struct {
	mu       sync.Mutex
	requests []string
}

func (a *asked) record(r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r.Method+" "+r.RequestURI+" header="+r.Header.Get("X-Planted")+" cookie="+r.Header.Get("Cookie"))
}

// since returns what was asked and forgets it.
func (a *asked) since() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	got := a.requests
	a.requests = nil
	return got
}

// elsewhere is a server that answers nothing and says what it was asked:
// where a fetch must not go.
func elsewhere(t *testing.T) (string, *asked) {
	t.Helper()
	a := &asked{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.record(r)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return server.URL, a
}

// forge serves a repository over git's smart HTTP protocol, by git's own
// http-backend, at /open/code.git to anyone and at /closed/code.git only
// to x-access-token with the password. Nothing leaves the machine.
func forge(t *testing.T) string {
	t.Helper()
	at, _, _ := served(t, false)
	return at
}

// served is a forge, over https where it is secure, and what it was
// asked; and, where it is secure, a file of the one authority its
// certificate is of, as git reads one. It gives a cookie with every
// answer.
func served(t *testing.T, secure bool) (at string, a *asked, authority string) {
	t.Helper()
	url, _, _ := remote(t)
	root := t.TempDir()
	run(t, root, "clone", "--quiet", "--bare", strings.TrimPrefix(url, "file://"), "code.git")
	run(t, filepath.Join(root, "code.git"), "config", "uploadpack.allowFilter", "true")
	exec := run(t, root, "--exec-path")
	backend := &cgi.Handler{Path: filepath.Join(exec, "git-http-backend"), Root: "/", Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	a = &asked{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.record(r)
		http.SetCookie(w, &http.Cookie{Name: "given", Value: "by-the-forge", Path: "/"})
		path, closed := strings.CutPrefix(r.URL.Path, "/closed/")
		if !closed {
			path, _ = strings.CutPrefix(r.URL.Path, "/open/")
		}
		if user, given, _ := r.BasicAuth(); closed && (user != "x-access-token" || given != password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="forge"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		r.URL.Path = "/" + path
		backend.ServeHTTP(w, r)
	})
	server := httptest.NewUnstartedServer(handler)
	if !secure {
		server.Start()
		t.Cleanup(server.Close)
		return server.URL, a, ""
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	authority = filepath.Join(t.TempDir(), "authority.pem")
	write(t, filepath.Dir(authority), "authority.pem", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})))
	return server.URL, a, authority
}

// A credential is presented as a sandbox was told to present it, and by
// no helper the repository names, which would be given it to keep; and
// where none is told, no program the repository names is asked for a
// password. The control is git asked plainly, which runs each.
func TestWideningPresentsACredentialOnlyAsTold(t *testing.T) {
	ctx := context.Background()
	at := forge(t)
	ran := filepath.Join(t.TempDir(), "ran")
	clone := func(t *testing.T, remote string, settings ...[2]string) string {
		dir := filepath.Join(t.TempDir(), "clone")
		if _, err := (git.System{}).Clone(ctx, dir, vcs.Source{Remote: remote, Roots: []string{"hello"}}); err != nil {
			t.Fatal(err)
		}
		for _, setting := range settings {
			run(t, dir, "config", setting[0], setting[1])
		}
		return dir
	}

	told, err := git.System{}.Presenting(map[string]string{}, at, "x-access-token", "A_VARIABLE")
	if err != nil {
		t.Fatal(err)
	}
	tell := func(t *testing.T) {
		for name, value := range told {
			t.Setenv(name, value)
		}
		t.Setenv("A_VARIABLE", password)
	}

	t.Run("a program asked for a password", func(t *testing.T) {
		_ = os.Remove(ran)
		// A program, which git runs by its path and with no shell.
		askpass := filepath.Join(t.TempDir(), "askpass")
		write(t, filepath.Dir(askpass), "askpass", "#!/bin/sh\necho askpass >> "+ran+"\n")
		if err := os.Chmod(askpass, 0o755); err != nil {
			t.Fatal(err)
		}
		// Cloned as told, then fetched from with nothing told.
		settings := [][2]string{{"core.askPass", askpass}}
		tell(t)
		dir, control := clone(t, at+"/closed/code.git", settings...), clone(t, at+"/closed/code.git", settings...)
		for name := range told {
			os.Unsetenv(name)
		}
		os.Unsetenv("A_VARIABLE")
		if err := (git.System{}).Widen(ctx, dir, at+"/closed/code.git", []string{"greet"}); err == nil {
			t.Error("a fetch that is told no password and asks for one was answered")
		}
		if got := said(t, ran); len(got) != 0 {
			t.Errorf("widening ran what the repository names: %v", got)
		}
		plainly(control, "sparse-checkout", "add", "greet")
		if got := said(t, ran); !slices.Contains(got, "askpass") {
			t.Errorf("asked plainly, git ran %v and not askpass, so the test above shows nothing", got)
		}
	})

	tell(t)
	t.Run("a helper given the credential to keep", func(t *testing.T) {
		_ = os.Remove(ran)
		settings := [][2]string{
			{"credential.helper", "!f() { echo helper-$1 >> " + ran + "; cat >/dev/null; }; f"},
			{"credential." + at + ".helper", "!f() { echo its-helper-$1 >> " + ran + "; cat >/dev/null; }; f"},
		}
		dir := clone(t, at+"/closed/code.git", settings...)
		if err := (git.System{}).Widen(ctx, dir, at+"/closed/code.git", []string{"greet"}); err != nil {
			t.Fatalf("the credential told was not presented: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "greet/greet.go")); err != nil {
			t.Errorf("widened with the credential told: %v", err)
		}
		if got := said(t, ran); len(got) != 0 {
			t.Errorf("widening ran what the repository names: %v", got)
		}
		plainly(clone(t, at+"/closed/code.git", settings...), "sparse-checkout", "add", "greet")
		if got := said(t, ran); !slices.Contains(got, "helper-store") || !slices.Contains(got, "its-helper-store") {
			t.Errorf("asked plainly, git ran %v, and gave neither helper the credential to keep, so the test above shows nothing", got)
		}
	})
}

// removed is what a setting planted is, to be taken out of the
// configuration.
const removed = "\x00removed"

// planted is a partial clone of a remote, of hello alone, with settings
// written into its configuration after it was made, as anything that may
// write the repository can, or removed from it.
func planted(t *testing.T, remote string, settings ...[2]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	if _, err := (git.System{}).Clone(context.Background(), dir, vcs.Source{Remote: remote, Roots: []string{"hello"}}); err != nil {
		t.Fatal(err)
	}
	for _, setting := range settings {
		if setting[1] == removed {
			run(t, dir, "config", "--unset", setting[0])
			continue
		}
		run(t, dir, "config", setting[0], setting[1])
	}
	return dir
}

// A widening fetches from the remote the repository was cloned from and
// from nowhere else, whatever its configuration says: an origin made
// another address, for a fetch or for a push, is not asked, and a
// repository whose configuration rewrites an address, for a fetch or for
// a push, names another remote to fetch what it lacks from, by marking
// it or by giving it a filter, names an address to fetch bundles from,
// or names a proxy, for every address, for the remote or for its
// address, is refused by the setting's name and asks nothing. The
// control is git asked plainly, which asks the other.
func TestWideningFetchesOnlyFromTheRemoteItWasClonedFrom(t *testing.T) {
	ctx := context.Background()
	at, _, _ := served(t, false)
	from := at + "/open/code.git"
	other, asked := elsewhere(t)
	add := []string{"sparse-checkout", "add", "greet"}
	for name, c := range map[string]struct {
		settings [][2]string
		// refused is the setting widening is refused by; none, it is
		// answered from the remote given.
		refused string
		// control is what git is asked plainly, which asks the other.
		control []string
	}{
		"origin made another address": {[][2]string{{"remote.origin.url", other + "/open/code.git"}}, "", add},
		"an address rewritten":        {[][2]string{{"url." + other + "/.insteadOf", at + "/"}}, "url.<address>.insteadof", add},
		"an address rewritten for a push": {
			[][2]string{{"url." + other + "/.pushInsteadOf", at + "/"}}, "url.<address>.pushinsteadof",
			[]string{"push", "origin", "HEAD:refs/heads/elsewhere"},
		},
		"another remote to fetch what it lacks from": {
			[][2]string{{"remote.origin.promisor", "false"}, {"remote.origin.partialclonefilter", removed}, {"remote.other.url", other + "/open/code.git"}, {"remote.other.promisor", "true"}},
			"remote.<name>.promisor", add,
		},
		"another remote given a filter": {
			[][2]string{{"remote.origin.promisor", "false"}, {"remote.origin.partialclonefilter", removed}, {"remote.other.url", other + "/open/code.git"}, {"remote.other.partialclonefilter", "blob:none"}},
			"remote.<name>.partialclonefilter", add,
		},
		"an address to fetch bundles from": {[][2]string{{"fetch.bundleURI", other + "/bundle"}}, "fetch.bundleURI", add},
		"another remote as the partial clone's": {
			[][2]string{{"remote.origin.promisor", "false"}, {"remote.origin.partialclonefilter", removed}, {"remote.other.url", other + "/open/code.git"}, {"extensions.partialClone", "other"}},
			"extensions.partialClone", add,
		},
		"a proxy":                 {[][2]string{{"http.proxy", other}}, "http.proxy", add},
		"a proxy for the remote":  {[][2]string{{"remote.origin.proxy", other}}, "remote.<name>.proxy", add},
		"a proxy for the address": {[][2]string{{"http." + from + ".proxy", other}}, "http.<address>.proxy", add},
	} {
		t.Run(name, func(t *testing.T) {
			dir := planted(t, from, c.settings...)
			asked.since()

			err := git.System{}.Widen(ctx, dir, from, []string{"greet"})

			if got := asked.since(); len(got) != 0 {
				t.Errorf("widening asked another: %v", got)
			}
			if c.refused == "" {
				if _, fetched := os.Stat(filepath.Join(dir, "greet/greet.go")); err != nil || fetched != nil {
					t.Errorf("widening did not fetch from the remote given: %v, %v", err, fetched)
				}
			} else if err == nil || !strings.Contains(err.Error(), c.refused) {
				t.Errorf("widening was refused as %v, not by %s", err, c.refused)
			} else if strings.Contains(err.Error(), other) {
				t.Errorf("the refusal repeats what the setting says: %v", err)
			}
			// The control: asked plainly, git asks the other.
			plainly(planted(t, from, c.settings...), c.control...)
			if got := asked.since(); len(got) == 0 {
				t.Error("asked plainly, git did not ask the other, so the test above shows nothing")
			}
		})
	}
}

// A widening sends the remote no header the repository's configuration
// names, and neither sends the cookies of a file it names nor keeps the
// remote's cookies in one; a repository that names a header for the
// remote's own address, which would win over any setting for every
// address, is refused by the setting's name. The control is git asked
// plainly, which sends and keeps each.
func TestWideningSendsNoHeaderOrCookieTheRepositoryNames(t *testing.T) {
	ctx := context.Background()
	at, asked, _ := served(t, false)
	from := at + "/open/code.git"
	host := strings.TrimPrefix(at, "http://")
	host = host[:strings.LastIndex(host, ":")]
	cookies := filepath.Join(t.TempDir(), "cookies")
	write(t, filepath.Dir(cookies), "cookies", host+"\tFALSE\t/\tFALSE\t0\tplanted\tsent\n")
	kept := filepath.Join(t.TempDir(), "kept")
	for name, c := range map[string]struct {
		settings [][2]string
		// sent is what the forge is sent, asked plainly; none, a file of
		// cookies is kept.
		sent string
		// refused is the setting widening is refused by, if by one.
		refused string
	}{
		"a header":                  {[][2]string{{"http.extraHeader", "X-Planted: everywhere"}}, "header=everywhere", ""},
		"a header for the address":  {[][2]string{{"http." + from + ".extraHeader", "X-Planted: here"}}, "header=here", "http.<address>.extraheader"},
		"a file of cookies":         {[][2]string{{"http.cookieFile", cookies}}, "cookie=planted=sent", ""},
		"a file to keep cookies in": {[][2]string{{"http.cookieFile", kept}, {"http.saveCookies", "true"}}, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			_ = os.Remove(kept)
			dir := planted(t, from, c.settings...)
			asked.since()

			err := git.System{}.Widen(ctx, dir, from, []string{"greet"})
			if c.refused == "" && err != nil {
				t.Fatalf("widening was not answered: %v", err)
			}
			if c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)) {
				t.Errorf("widening was refused as %v, not by %s", err, c.refused)
			}

			// The forge's own cookie, given as it answered, it is sent back
			// as the fetch goes on: that is the forge's, and no file's.
			for _, request := range asked.since() {
				if !strings.Contains(request, " header= ") || strings.Contains(request, "planted") {
					t.Errorf("widening sent what the repository names: %s", request)
				}
			}
			if _, err := os.Stat(kept); err == nil {
				t.Error("widening kept the forge's cookies where the repository names")
			}
			// The control: asked plainly, git sends it, or keeps them.
			plainly(planted(t, from, c.settings...), "sparse-checkout", "add", "greet")
			got := strings.Join(asked.since(), "\n")
			if c.sent != "" && !strings.Contains(got, c.sent) {
				t.Errorf("asked plainly, git did not send %s, so the test above shows nothing: %s", c.sent, got)
			}
			if _, err := os.Stat(kept); c.sent == "" && err != nil {
				t.Errorf("asked plainly, git kept no cookie, so the test above shows nothing: %v", err)
			}
		})
	}
}

// A widening checks the remote's certificate against the authorities the
// sandbox told, and against none the repository's configuration names:
// a repository that turns the check off, for every address or for the
// remote's own, is not answered, and one that names authorities of its
// own is refused by the setting's name. The control is git asked
// plainly, which fetches from the forge whose authority the sandbox did
// not tell.
func TestWideningChecksTheServersCertificateAsTheSandboxTold(t *testing.T) {
	ctx := context.Background()
	at, _, authority := served(t, true)
	from := at + "/open/code.git"
	// The forge's authority is the one the sandbox tells, as a sandbox is
	// told its proxy's.
	t.Setenv("GIT_SSL_CAINFO", authority)
	told := planted(t, from)
	if err := (git.System{}).Widen(ctx, told, from, []string{"greet"}); err != nil {
		t.Fatalf("widening from a forge whose authority was told was not answered: %v", err)
	}
	authorities := t.TempDir()
	write(t, authorities, "planted.pem", "")
	for name, c := range map[string]struct {
		settings [][2]string
		// refused is the setting widening is refused by, if by one.
		refused string
	}{
		"no check":                              {[][2]string{{"http.sslVerify", "false"}}, ""},
		"no check for the address":              {[][2]string{{"http." + from + ".sslVerify", "false"}}, "http.<address>.sslverify"},
		"authorities of its own":                {[][2]string{{"http.sslCAInfo", authority}}, "http.sslcainfo"},
		"a directory of authorities of its own": {[][2]string{{"http.sslCAPath", authorities}}, "http.sslcapath"},
	} {
		t.Run(name, func(t *testing.T) {
			// Cloned where its authority is told, and widened where none is.
			t.Setenv("GIT_SSL_CAINFO", authority)
			dir, control := planted(t, from, c.settings...), planted(t, from, c.settings...)
			os.Unsetenv("GIT_SSL_CAINFO")

			err := git.System{}.Widen(ctx, dir, from, []string{"greet"})

			if err == nil {
				t.Error("widening from a forge whose authority no one told was answered")
			}
			if c.refused != "" && (err == nil || !strings.Contains(err.Error(), c.refused)) {
				t.Errorf("widening was refused as %v, not by %s", err, c.refused)
			}
			if c.settings[0][0] == "http.sslCAPath" {
				// git reads a directory of authorities by their hashes, which
				// this one has none of: what it is refused by is the test.
				return
			}
			// The control: asked plainly, git fetches from the forge.
			plainly(control, "sparse-checkout", "add", "greet")
			if _, err := os.Stat(filepath.Join(control, "greet/greet.go")); err != nil {
				t.Errorf("asked plainly, git did not fetch from the forge, so the test above shows nothing: %v", err)
			}
		})
	}
}

// told is what a sandbox tells git in its environment, as settings it
// counts, for as long as the test runs.
func told(t *testing.T, settings ...[2]string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(len(settings)))
	for i, setting := range settings {
		t.Setenv("GIT_CONFIG_KEY_"+strconv.Itoa(i), setting[0])
		t.Setenv("GIT_CONFIG_VALUE_"+strconv.Itoa(i), setting[1])
	}
}

// widenedAsking widens a clone of from planted for the test, with what the
// test told, and returns what the forge was asked while it widened.
func widenedAsking(ctx context.Context, t *testing.T, asked *asked, from string) string {
	t.Helper()
	dir := planted(t, from)
	asked.since()
	if err := (git.System{}).Widen(ctx, dir, from, []string{"greet"}); err != nil {
		t.Fatalf("widening was not answered: %v", err)
	}
	return strings.Join(asked.since(), "\n")
}

// What a sandbox tells git of http holds over what a widening sets for
// every address: a header told, for every address or for the remote's,
// is sent, and a check told off, for every address or for the remote's,
// is off. The control is the same widening told nothing, which sends no
// header and is not answered by a forge whose authority no one told.
func TestWhatASandboxToldOfHTTPHoldsOverWhatAWideningSets(t *testing.T) {
	ctx := context.Background()
	at, asked, _ := served(t, false)
	from := at + "/open/code.git"
	for _, key := range []string{"http.extraHeader", "http." + from + ".extraHeader"} {
		t.Run(key, func(t *testing.T) {
			told(t, [2]string{key, "X-Planted: told"})
			if got := widenedAsking(ctx, t, asked, from); !strings.Contains(got, "header=told") {
				t.Errorf("the header told was not sent: %s", got)
			}
		})
	}
	t.Run("the control: nothing told", func(t *testing.T) {
		if got := widenedAsking(ctx, t, asked, from); strings.Contains(got, "header=told") {
			t.Errorf("told nothing, a header was sent: %s", got)
		}
	})

	secure, _, authority := served(t, true)
	from = secure + "/open/code.git"
	for _, key := range []string{"http.sslVerify", "http." + from + ".sslVerify", ""} {
		t.Run("a check told off by "+key, func(t *testing.T) {
			t.Setenv("GIT_SSL_CAINFO", authority)
			dir := planted(t, from)
			os.Unsetenv("GIT_SSL_CAINFO")
			if key != "" {
				told(t, [2]string{key, "false"})
			}
			err := git.System{}.Widen(ctx, dir, from, []string{"greet"})
			if key != "" && err != nil {
				t.Errorf("the check told off was made: %v", err)
			}
			// The control: told nothing, the check is made.
			if key == "" && err == nil {
				t.Error("told nothing, a forge whose authority no one told was answered")
			}
		})
	}
}

// A widening reaches the remote's host at the address its name has, and
// a repository that names another address for it, for every address or
// for the remote's own, is refused by the setting's name and has nothing
// asked there. The control is git asked plainly, which reaches the forge
// by a name that has no address.
func TestWideningReachesTheRemoteAtTheAddressItsNameHas(t *testing.T) {
	ctx := context.Background()
	at, asked, _ := served(t, false)
	port := at[strings.LastIndex(at, ":")+1:]
	named := "http://forge.invalid:" + port + "/open/code.git"
	resolve := "forge.invalid:" + port + ":127.0.0.1"
	for name, c := range map[string]struct {
		key     string
		refused string
	}{
		"for every address":    {"http.curloptResolve", "http.curloptResolve"},
		"for the remote's own": {"http." + named + ".curloptResolve", "http.<address>.curloptresolve"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := planted(t, at+"/open/code.git", [2]string{c.key, resolve})
			asked.since()
			err := git.System{}.Widen(ctx, dir, named, []string{"greet"})
			if got := asked.since(); len(got) != 0 {
				t.Errorf("widening reached the address the repository named: %v", got)
			}
			if err == nil || !strings.Contains(err.Error(), c.refused) {
				t.Errorf("widening was refused as %v, not by %s", err, c.refused)
			}
			// The control: asked plainly, git reaches the forge by the name.
			plainly(planted(t, at+"/open/code.git", [2]string{c.key, resolve}, [2]string{"remote.origin.url", named}), "sparse-checkout", "add", "greet")
			if got := asked.since(); len(got) == 0 {
				t.Error("asked plainly, git did not reach the forge by the name, so the test above shows nothing")
			}
		})
	}
}

// A push through git given a remote goes to that remote, and not to an
// address the repository says origin pushes to. The control is git
// asked plainly, which pushes there.
func TestAPushGoesOnlyToTheRemoteGiven(t *testing.T) {
	ctx := context.Background()
	at, _, _ := served(t, false)
	from := at + "/open/code.git"
	other, asked := elsewhere(t)
	setting := [2]string{"remote.origin.pushurl", other + "/open/code.git"}
	dir := planted(t, from, setting)
	asked.since()
	// The forge takes no push: what matters is where it is asked.
	_, _ = rungit.In(ctx, dir, rungit.Options{Told: true, Remote: from}).Run(ctx, "push", "origin", "HEAD:refs/heads/elsewhere")
	if got := asked.since(); len(got) != 0 {
		t.Errorf("the push went where the repository says: %v", got)
	}
	plainly(planted(t, from, setting), "push", "origin", "HEAD:refs/heads/elsewhere")
	if got := asked.since(); len(got) == 0 {
		t.Error("asked plainly, git did not push there, so the test above shows nothing")
	}
}
