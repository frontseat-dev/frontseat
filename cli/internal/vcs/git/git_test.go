package git_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/cli/internal/vcs"
	"github.com/frontseat-dev/frontseat/cli/internal/vcs/git"
)

// run runs git in a directory, as no one in particular, and fails the
// test where it fails.
func run(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=a test", "GIT_AUTHOR_EMAIL=a-test@example.com", "GIT_COMMITTER_NAME=a test", "GIT_COMMITTER_EMAIL=a-test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// cloned is a checkout of a repository that has a remote holding its one
// branch, main, with one commit: as a clone leaves one. The remote is a
// directory of the test's; the checkout is then told it is somewhere
// else, as the test asks.
func cloned(t *testing.T, written string) string {
	t.Helper()
	// What a person's own configuration says is not the test's.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	run(t, root, "init", "--quiet", "--bare", "--initial-branch", "main", remote)
	checkout := filepath.Join(root, "checkout")
	run(t, root, "clone", "--quiet", remote, checkout)
	if err := os.WriteFile(filepath.Join(checkout, "README"), []byte("a repository\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, checkout, "add", ".")
	run(t, checkout, "commit", "--quiet", "-m", "one")
	run(t, checkout, "push", "--quiet", "-u", "origin", "HEAD:main")
	run(t, checkout, "config", "remote.origin.url", written)
	return checkout
}

const forge = "https://forge.example/acme/code.git"

// A checkout says the repository it is of as a sandbox clones it: over
// http or https as it is written, its port with it, and over https from
// the same host and path where it is written for ssh, which is said. It
// says what repository that is whatever the spelling, and its branch.
func TestACheckoutSaysWhatAWorkspaceOfItClones(t *testing.T) {
	for written, want := range map[string]struct {
		remote, repository string
		converted          bool
	}{
		"https://forge.example/acme/code.git":       {"https://forge.example/acme/code.git", "forge.example/acme/code", false},
		"https://forge.example/acme/code":           {"https://forge.example/acme/code", "forge.example/acme/code", false},
		"https://Forge.Example/acme/code.git/":      {"https://forge.example/acme/code.git", "forge.example/acme/code", false},
		"https://forge.example:8443/acme/code":      {"https://forge.example:8443/acme/code", "forge.example:8443/acme/code", false},
		"http://forge.example:3000/acme/code.git":   {"http://forge.example:3000/acme/code.git", "forge.example:3000/acme/code", false},
		"https://forge.example/acme/a%20b.git":      {"https://forge.example/acme/a%20b.git", "forge.example/acme/a%20b", false},
		"git@forge.example:acme/code.git":           {"https://forge.example/acme/code.git", "forge.example/acme/code", true},
		"ssh://git@forge.example/acme/code.git":     {"https://forge.example/acme/code.git", "forge.example/acme/code", true},
		"ssh://git@forge.example:2222/acme/code":    {"https://forge.example/acme/code", "forge.example/acme/code", true},
		"git+ssh://git@forge.example/acme/code.git": {"https://forge.example/acme/code.git", "forge.example/acme/code", true},
		// Whoever is named before the host is left out, as two forges
		// write a clone's address by default; a token written there is
		// left out the same, and goes nowhere.
		"https://someone@forge.example/acme/code":               {"https://forge.example/acme/code", "forge.example/acme/code", true},
		"https://a-secret-of-the-tests@forge.example/acme/code": {"https://forge.example/acme/code", "forge.example/acme/code", true},
		"HTTPS://FORGE.example/acme/code":                       {"https://forge.example/acme/code", "forge.example/acme/code", false},
	} {
		got, err := git.System{}.Standing(context.Background(), cloned(t, written))
		if err != nil {
			t.Errorf("%s: %v", written, err)
			continue
		}
		if got.Remote != want.remote || got.Repository != want.repository || got.Converted != want.converted {
			t.Errorf("%s: cloned from %s, the repository %s, converted %v; want %s, %s, %v", written, got.Remote, got.Repository, got.Converted, want.remote, want.repository, want.converted)
		}
		if strings.Contains(got.Remote+got.Repository, "a-secret-of-the-tests") {
			t.Errorf("%s: what is cloned repeats what was written before the host: %+v", written, got)
		}
		if got.Branch != "main" || got.Unpushed != 0 || got.Uncommitted != 0 {
			t.Errorf("%s: on %s with %d unpushed and %d uncommitted, want main with none", written, got.Branch, got.Unpushed, got.Uncommitted)
		}
	}
}

// What the remote does not hold is counted, and is no refusal: commits
// not pushed, and files changed and not committed, each file once
// whatever a person's settings say of files git does not follow.
func TestWhatTheRemoteDoesNotHoldIsCounted(t *testing.T) {
	checkout := cloned(t, forge)
	for _, name := range []string{"two", "three"} {
		if err := os.WriteFile(filepath.Join(checkout, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, checkout, "add", ".")
		run(t, checkout, "commit", "--quiet", "-m", name)
	}
	if err := os.WriteFile(filepath.Join(checkout, "README"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Three files git does not follow, in a directory of their own, in a
	// checkout told to say nothing of such files.
	within := filepath.Join(checkout, "within")
	if err := os.Mkdir(within, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(within, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(t, checkout, "config", "status.showUntrackedFiles", "no")
	// From a directory within it, as a person stands in one.
	got, err := git.System{}.Standing(context.Background(), within)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unpushed != 2 || got.Uncommitted != 4 {
		t.Errorf("%d unpushed and %d uncommitted, want 2 and 4", got.Unpushed, got.Uncommitted)
	}
}

// The branch is the one the checkout is on, and its workspace is of the
// remote's branch of that name: a branch made from the remote's main
// follows main and is not main, and is refused until it is pushed. The
// remote is the one the branch follows, of several.
func TestTheBranchIsTheOneTheCheckoutIsOn(t *testing.T) {
	ctx := context.Background()
	checkout := cloned(t, forge)
	run(t, checkout, "checkout", "--quiet", "-b", "feature", "origin/main")
	if followed := run(t, checkout, "config", "--get", "branch.feature.merge"); followed != "refs/heads/main" {
		t.Fatalf("the fixture's branch follows %q, want the remote's main", followed)
	}
	if got, err := (git.System{}).Standing(ctx, checkout); !errors.Is(err, vcs.ErrNotOnRemote) {
		t.Errorf("a branch made from the remote's main and not pushed: %+v, %v; want it refused", got, err)
	}

	// Two remotes: the branch follows the second, which is not origin.
	two := cloned(t, forge)
	run(t, two, "remote", "add", "theirs", "https://forge.example/other/code.git")
	run(t, two, "update-ref", "refs/remotes/theirs/main", "HEAD")
	run(t, two, "config", "branch.main.remote", "theirs")
	got, err := git.System{}.Standing(ctx, two)
	if err != nil || got.Remote != "https://forge.example/other/code.git" {
		t.Errorf("a branch that follows another remote: %+v, %v; want that remote", got, err)
	}
}

// Who commits in the checkout is said, and told to a sandbox through its
// environment; a checkout that names no one tells nothing.
func TestWhoCommitsIsSaid(t *testing.T) {
	checkout := cloned(t, forge)
	none, err := git.System{}.Standing(context.Background(), checkout)
	if err != nil {
		t.Fatal(err)
	}
	if none.Author != (vcs.Author{}) || len(git.System{}.Authoring(none.Author)) != 0 {
		t.Errorf("a checkout that names no one says %+v", none.Author)
	}
	run(t, checkout, "config", "user.name", "Someone Here")
	run(t, checkout, "config", "user.email", "someone@example.com")
	got, err := git.System{}.Standing(context.Background(), checkout)
	if err != nil {
		t.Fatal(err)
	}
	told := git.System{}.Authoring(got.Author)
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		if told[name] != "Someone Here" {
			t.Errorf("%s = %q", name, told[name])
		}
	}
	for _, name := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		if told[name] != "someone@example.com" {
			t.Errorf("%s = %q", name, told[name])
		}
	}
	if len(told) != 4 {
		t.Errorf("a sandbox is told %v", told)
	}
	// A name of two lines is not one, and is not told.
	if told := (git.System{}).Authoring(vcs.Author{Name: "one\ntwo", Email: "someone@example.com"}); told["GIT_AUTHOR_NAME"] != "" || len(told) != 2 {
		t.Errorf("a name of two lines was told: %v", told)
	}
}

// What gives no workspace, each by its reason: a directory in no
// checkout, a checkout on no branch, a branch the remote does not hold,
// a branch that follows no remote there is, and a remote no sandbox can
// clone, which is never repeated as it is written.
func TestWhatGivesNoWorkspaceSaysWhy(t *testing.T) {
	ctx := context.Background()
	if _, err := (git.System{}).Standing(ctx, t.TempDir()); !errors.Is(err, vcs.ErrNoCheckout) {
		t.Errorf("a directory in no checkout: %v", err)
	}

	detached := cloned(t, forge)
	run(t, detached, "checkout", "--quiet", "--detach")
	if _, err := (git.System{}).Standing(ctx, detached); !errors.Is(err, vcs.ErrNoBranch) {
		t.Errorf("a checkout on no branch: %v", err)
	}

	fresh := cloned(t, forge)
	run(t, fresh, "checkout", "--quiet", "-b", "not-pushed")
	if _, err := (git.System{}).Standing(ctx, fresh); !errors.Is(err, vcs.ErrNotOnRemote) {
		t.Errorf("a branch the remote does not hold: %v", err)
	}

	gone := cloned(t, forge)
	run(t, gone, "remote", "remove", "origin")
	if _, err := (git.System{}).Standing(ctx, gone); !errors.Is(err, vcs.ErrNoRemote) {
		t.Errorf("a branch that follows no remote there is: %v", err)
	}

	const secret = "a-secret-of-the-tests"
	// A branch that follows an address and no remote's name, as git push
	// -u with an address writes it, is refused without the address. The
	// control is a remote of a name, which is said by it.
	unnamed := cloned(t, forge)
	run(t, unnamed, "config", "branch.main.remote", "https://someone:"+secret+"@forge.example/acme/code.git")
	if _, err := (git.System{}).Standing(ctx, unnamed); !errors.Is(err, vcs.ErrNoRemote) || strings.Contains(err.Error(), secret) {
		t.Errorf("a branch that follows an address: %v; want it refused as no remote, without the address", err)
	}
	named := cloned(t, forge)
	run(t, named, "config", "branch.main.remote", "upstream")
	if _, err := (git.System{}).Standing(ctx, named); !errors.Is(err, vcs.ErrNoRemote) || !strings.Contains(err.Error(), `"upstream"`) {
		t.Errorf("a branch that follows a remote of a name there is not: %v; want it refused by the name", err)
	}
	for name, written := range map[string]string{
		"a directory of this machine":        filepath.Join(t.TempDir(), "elsewhere.git"),
		"a file URL":                         "file:///somewhere/code.git",
		"a password in it":                   "https://someone:" + secret + "@forge.example/acme/code.git",
		"a token and no repository":          "https://" + secret + "@forge.example/",
		"a password and a port that is none": "https://someone:" + secret + "@forge.example:notaport/acme/code",
		"a password over ssh":                "ssh://git:" + secret + "@forge.example/acme/code.git",
		"an address and no name":             "https://192.0.2.7/acme/code.git",
		"an address of the other kind":       "https://[::1]:8443/acme/code.git",
		"a question in it":                   "https://forge.example/acme/code?x=1",
		"a part of a page":                   "https://forge.example/acme/code#main",
		"a path that climbs":                 "https://forge.example/acme/../other/code",
		"a home of someone's":                "git@forge.example:~/code.git",
		"no repository":                      "https://forge.example/",
		"another transport":                  "ext::sh -c x",
		"a comma in the path":                "git@forge.example:acme/co,de.git",
		"an at in the path":                  "git@forge.example:acme/code@v2.git",
		"an escape in the path":              "git@forge.example:acme/a%20b.git",
		"a letter of another alphabet":       "git@forge.example:acme/cod\u00e9.git",
		"a password before a colon form":     "someone:" + secret + "@forge.example:acme/code.git",
		"a host in brackets":                 "[forge.example]:acme/code.git",
		"an address written in sixteens":     "https://0x7f.1/acme/code.git",
		"an address written as one number":   "https://0x7f000001/acme/code.git",
		"a transport of git's own":           "git://forge.example/acme/code.git",
	} {
		_, err := (git.System{}).Standing(ctx, cloned(t, written))
		if !errors.Is(err, vcs.ErrNoRemote) {
			t.Errorf("%s: %v, want it refused as no remote for a sandbox", name, err)
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("%s: the refusal repeats what the remote holds: %v", name, err)
		}
	}
}

// A remote of several addresses is fetched from one and would be read
// here as another, so it is refused; and two remotes are the same
// repository whatever their spelling, and not where they are not.
func TestWhichRepositoryARemoteIsOf(t *testing.T) {
	several := cloned(t, forge)
	run(t, several, "remote", "set-url", "--add", "origin", "https://mirror.example/other/place.git")
	if got, err := (git.System{}).Standing(context.Background(), several); !errors.Is(err, vcs.ErrNoRemote) {
		t.Errorf("a remote of two addresses: %+v, %v; want it refused", got, err)
	}
	for _, c := range []struct {
		one, other string
		same       bool
	}{
		{"https://forge.example/acme/code.git", "https://forge.example/acme/code", true},
		{"https://forge.example/acme/code.git", "git@forge.example:acme/code.git", true},
		{"https://Forge.Example/acme/code/", "https://forge.example/acme/code.git", true},
		{"https://forge.example/acme/code.git", "https://forge.example/acme/other.git", false},
		{"https://forge.example/acme/code.git", "https://elsewhere.example/acme/code.git", false},
		{"https://forge.example:8443/acme/code", "https://forge.example/acme/code", false},
		{"not a remote at all", "not a remote at all", true},
		{"not a remote at all", "https://forge.example/acme/code", false},
	} {
		if got := (git.System{}).Same(c.one, c.other); got != c.same {
			t.Errorf("%s and %s: the same repository %v, want %v", c.one, c.other, got, c.same)
		}
	}
}

// A person's own setting that rewrites a remote, which is where a token
// is sometimes kept, is not applied: the remote is read as the checkout
// writes it.
func TestARemoteIsReadAsTheCheckoutWritesIt(t *testing.T) {
	checkout := cloned(t, forge)
	run(t, checkout, "config", "url.https://someone:a-token@forge.example/.insteadOf", "https://forge.example/")
	got, err := git.System{}.Standing(context.Background(), checkout)
	if err != nil || got.Remote != forge {
		t.Errorf("with a rewriting setting: %+v, %v; want %s", got, err, forge)
	}
}

// Reading a checkout runs nothing the checkout's configuration names:
// a filter that would be run to say whether a file changed is not, and a
// file monitor is not. The control is git itself, told nothing, which
// runs each.
func TestReadingACheckoutRunsNothingItNames(t *testing.T) {
	checkout := cloned(t, forge)
	ran := filepath.Join(t.TempDir(), "ran")
	// Two files git follows, each of which it must look at again: a
	// filter is run to say whether a file it follows changed.
	if err := os.WriteFile(filepath.Join(checkout, "unnamed"), []byte("a file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, checkout, "add", ".")
	run(t, checkout, "commit", "--quiet", "-m", "two files")
	// A filter of a name, for every file; a filter of the empty name,
	// which is one too, for one file; and a file monitor.
	if err := os.WriteFile(filepath.Join(checkout, ".gitattributes"), []byte("* filter=planted\nunnamed filter=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, checkout, "config", "filter.planted.clean", "sh -c 'echo filter >> "+ran+"; cat'")
	run(t, checkout, "config", "filter..clean", "sh -c 'echo unnamed-filter >> "+ran+"; cat'")
	run(t, checkout, "config", "core.fsmonitor", "sh -c 'echo monitor >> "+ran+"'")
	// Each changed to something of the same length, so that only its
	// content, which the filter is run over, says it changed.
	for name, content := range map[string]string{"README": "A REPOSITORY\n", "unnamed": "A FILE\n"} {
		if err := os.WriteFile(filepath.Join(checkout, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (git.System{}).Standing(context.Background(), checkout); err != nil {
		t.Fatal(err)
	}
	if planted, err := os.ReadFile(ran); err == nil {
		t.Fatalf("reading the checkout ran what it names: %s", planted)
	}
	// The control: asked plainly, git runs each of them.
	run(t, checkout, "status", "--porcelain")
	planted, err := os.ReadFile(ran)
	if err != nil {
		t.Fatalf("the fixture's filters are none git runs, so the test above shows nothing: %v", err)
	}
	for _, each := range []string{"filter", "unnamed-filter", "monitor"} {
		if !strings.Contains("\n"+string(planted), "\n"+each+"\n") {
			t.Errorf("asked plainly, git did not run %q, so the test above shows nothing of it: %s", each, planted)
		}
	}
}

// What the environment this runs in says of git does not reach it: a
// repository it is pointed at is not the checkout that is read.
func TestTheCheckoutReadIsTheOneOfTheDirectory(t *testing.T) {
	checkout := cloned(t, forge)
	other := cloned(t, "https://forge.example/other/elsewhere.git")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	got, err := git.System{}.Standing(context.Background(), checkout)
	if err != nil || got.Remote != forge {
		t.Errorf("pointed at another repository: %+v, %v; want the checkout's own, %s", got, err, forge)
	}
}
