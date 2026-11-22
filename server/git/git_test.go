package git_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/git"
)

// environment is what git was started with, as a shell it starts (an
// alias that begins with "!") prints it, one variable a line.
func environment(t *testing.T, options git.Options) []string {
	t.Helper()
	out, err := git.In(context.Background(), t.TempDir(), options).Run(context.Background(), "-c", "alias.environment=!env", "environment")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(out, "\n")
}

// What a sandbox tells git of how to reach a remote reaches it where it
// is kept, and nothing else of git's in the environment does; where it
// is not kept, none of it does.
func TestWhatASandboxToldGitReachesItOnlyWhereItIsKept(t *testing.T) {
	// What a person's own configuration says is not the test's.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "told")
	t.Setenv("GIT_SSL_CAINFO", "/etc/proxy-ca.pem")
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	ctx := context.Background()

	name, err := git.In(ctx, t.TempDir(), git.Options{Told: true}).Run(ctx, "config", "--get", "user.name")
	if err != nil || strings.TrimSpace(name) != "told" {
		t.Errorf("kept, the setting told is %q, %v; want told", name, err)
	}
	kept := environment(t, git.Options{Told: true})
	if !slices.Contains(kept, "GIT_SSL_CAINFO=/etc/proxy-ca.pem") {
		t.Errorf("kept, the proxy's authorities did not reach git: %q", kept)
	}
	if slices.Contains(kept, "GIT_DIR=/elsewhere/.git") {
		t.Errorf("kept, another repository reached git: %q", kept)
	}

	// The control: not kept, neither the setting nor the authorities do.
	if name, err := git.In(ctx, t.TempDir(), git.Options{}).Run(ctx, "config", "--get", "user.name"); err == nil {
		t.Errorf("not kept, the setting told reached git: %q", name)
	}
	if plain := environment(t, git.Options{}); slices.Contains(plain, "GIT_SSL_CAINFO=/etc/proxy-ca.pem") {
		t.Errorf("not kept, the proxy's authorities reached git: %q", plain)
	}
}

// What a sandbox tells git comes before the hardening, which so holds:
// a setting told cannot have git run what the hardening turns off.
func TestWhatASandboxToldGitDoesNotUndoTheHardening(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", "/planted/hooks")
	t.Setenv("GIT_CONFIG_KEY_1", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_1", "sh -c planted")
	ctx := context.Background()
	g := git.In(ctx, t.TempDir(), git.Options{Told: true})
	for key, want := range map[string]string{"core.hooksPath": "/dev/null", "core.fsmonitor": "false"} {
		if got, err := g.Run(ctx, "config", "--get", key); err != nil || strings.TrimSpace(got) != want {
			t.Errorf("%s is %q, %v; want %s", key, got, err, want)
		}
	}
}

// Who commits is told to git as the author and as the committer, and a
// name or an address that would end a line is not told at all.
func TestWhoCommitsIsToldAsAuthorAndCommitter(t *testing.T) {
	got := git.Authoring("Someone", "someone@example.com")
	want := map[string]string{
		"GIT_AUTHOR_NAME": "Someone", "GIT_COMMITTER_NAME": "Someone",
		"GIT_AUTHOR_EMAIL": "someone@example.com", "GIT_COMMITTER_EMAIL": "someone@example.com",
	}
	if len(got) != len(want) {
		t.Fatalf("told %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s is %q, want %q", key, got[key], value)
		}
	}
	if told := git.Authoring("Some\none", "some\rone@example.com"); len(told) != 0 {
		t.Errorf("a name and an address that end a line were told: %v", told)
	}
}

// initialized is a repository with nothing in it.
func initialized(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := git.In(context.Background(), dir, git.Options{Offline: true}).Run(context.Background(), "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A program for asking a remote, which git takes before any setting
// given after it, cannot be turned off: where a remote may be reached a
// directory whose configuration names one is refused, by the setting's
// name, and where none may be, it is read as any other.
func TestARemoteProgramIsRefusedWhereARemoteMayBeReached(t *testing.T) {
	ctx := context.Background()
	dir := initialized(t)
	if _, err := git.In(ctx, dir, git.Options{Told: true}).Run(ctx, "status", "--porcelain"); err != nil {
		t.Fatalf("a repository that names no program was refused, so the test below shows nothing: %v", err)
	}
	// A remote's name is the repository's to write, and may be an address
	// that holds a token: the refusal names the setting and not the remote.
	for key, variable := range map[string]string{"remote.origin.uploadpack": "uploadpack", "remote.https://x:tok3n@elsewhere.example/.receivepack": "receivepack"} {
		if _, err := git.In(ctx, dir, git.Options{Offline: true}).Run(ctx, "config", key, "sh -c planted"); err != nil {
			t.Fatal(err)
		}
		_, err := git.In(ctx, dir, git.Options{Told: true}).Run(ctx, "status", "--porcelain")
		if err == nil || !strings.Contains(err.Error(), "remote.<name>."+variable) {
			t.Errorf("a repository that names %s is asked, or refused as %v", variable, err)
		}
		if err != nil && (strings.Contains(err.Error(), "tok3n") || strings.Contains(err.Error(), "origin")) {
			t.Errorf("the refusal repeats the remote's name: %v", err)
		}
		if _, err := git.In(ctx, dir, git.Options{Offline: true}).Run(ctx, "status", "--porcelain"); err != nil {
			t.Errorf("offline, a repository that names %s is refused: %v", key, err)
		}
		if _, err := git.In(ctx, dir, git.Options{Offline: true}).Run(ctx, "config", "--unset", key); err != nil {
			t.Fatal(err)
		}
	}
}

// Where a remote may be reached, a repository whose own configuration
// rewrites an address is refused by the setting's name, and nothing of
// the address is repeated; what the machine's settings and what git is
// told as it starts say of one is theirs, and is not refused, nor is the
// repository where no remote may be reached.
func TestOnlyTheRepositorysOwnRewriteOfAnAddressIsRefused(t *testing.T) {
	ctx := context.Background()
	dir := initialized(t)
	const key = "url.https://token@elsewhere.example/.insteadOf"
	global := filepath.Join(t.TempDir(), "global")
	if err := os.WriteFile(global, []byte("[url \"https://token@elsewhere.example/\"]\n\tinsteadOf = https://forge.example/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", key)
	t.Setenv("GIT_CONFIG_VALUE_0", "https://other.example/")
	if _, err := git.In(ctx, dir, git.Options{Told: true}).Run(ctx, "status", "--porcelain"); err != nil {
		t.Fatalf("what the machine and the sandbox say of an address was refused: %v", err)
	}
	if _, err := git.In(ctx, dir, git.Options{Offline: true}).Run(ctx, "config", key, "https://forge.example/"); err != nil {
		t.Fatal(err)
	}
	_, err := git.In(ctx, dir, git.Options{Told: true}).Run(ctx, "status", "--porcelain")
	if err == nil || !strings.Contains(err.Error(), "url.<address>.insteadof") {
		t.Errorf("a repository that rewrites an address is asked, or refused as %v", err)
	} else if strings.Contains(err.Error(), "token") {
		t.Errorf("the refusal repeats the address: %v", err)
	}
	if _, err := git.In(ctx, dir, git.Options{Offline: true}).Run(ctx, "status", "--porcelain"); err != nil {
		t.Errorf("offline, a repository that rewrites an address is refused: %v", err)
	}
}

// plain runs git as a person would, in a directory, with the
// environment of the test and none of the hardening: the control.
func plain(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// committed is a repository whose history is two commits, of a file
// that says "one" and then "two", left as the second has it.
func committed(t *testing.T) (dir, first, second string) {
	t.Helper()
	dir = t.TempDir()
	plain(t, dir, "init", "--quiet", "--initial-branch", "main")
	for _, content := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(dir, "f"), []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		plain(t, dir, "add", "f")
		plain(t, dir, "commit", "--quiet", "-m", content)
	}
	return dir, plain(t, dir, "rev-parse", "HEAD~1"), plain(t, dir, "rev-parse", "HEAD")
}

// What a repository's history is, is what its commits say: a
// replacement of one, a graft of another parent onto one, and a graph of
// commits written to say another tree is HEAD's do not make a change look
// committed, nor the history look other than it is. The control is git
// asked plainly, which each deceives.
func TestAHistoryIsWhatItsCommitsSay(t *testing.T) {
	ctx := context.Background()
	for name, plant := range map[string]func(t *testing.T, dir, first, second string){
		// HEAD is replaced by a commit of the first's tree.
		"a replacement": func(t *testing.T, dir, first, second string) {
			replacement := plain(t, dir, "commit-tree", "-p", first, "-m", "two", first+"^{tree}")
			plain(t, dir, "update-ref", "refs/replace/"+second, replacement)
		},
		// HEAD is grafted onto no parent: its history is itself.
		"a graft": func(t *testing.T, dir, first, second string) {
			if err := os.WriteFile(filepath.Join(dir, ".git/info/grafts"), []byte(second+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		// The graph says HEAD's tree is the first commit's.
		"a graph of commits": func(t *testing.T, dir, first, second string) {
			plain(t, dir, "commit-graph", "write", "--reachable")
			forgeGraph(t, filepath.Join(dir, ".git/objects/info/commit-graph"), second, plain(t, dir, "rev-parse", first+"^{tree}"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, first, second := committed(t)
			plant(t, dir, first, second)
			// The file and the index as the first commit has them: changed
			// from what HEAD is, and as what is planted says HEAD is.
			if err := os.WriteFile(filepath.Join(dir, "f"), []byte("one\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			plain(t, dir, "add", "f")
			g := git.In(ctx, dir, git.Options{Offline: true})
			status, err := g.Run(ctx, "status", "--porcelain")
			if err != nil || strings.TrimSpace(status) != "M  f" {
				t.Errorf("the change is said to be %q, %v; want M  f", status, err)
			}
			if logged, err := g.Run(ctx, "log", "--format=%s"); err != nil || strings.TrimSpace(logged) != "two\none" {
				t.Errorf("the history is said to be %q, %v; want two, one", logged, err)
			}
			// The control: asked plainly, git is deceived.
			if status, logged := plain(t, dir, "status", "--porcelain"), plain(t, dir, "log", "--format=%s"); status == "M  f" && logged == "two\none" {
				t.Errorf("asked plainly, git says %q and %q, so the test above shows nothing", status, logged)
			}
		})
	}
}

// forgeGraph rewrites a commit's root tree in a graph of commits, and
// the graph's checksum, as anyone who may write the repository can.
func forgeGraph(t *testing.T, graph, commit, tree string) {
	t.Helper()
	data, err := os.ReadFile(graph)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := hex.DecodeString(commit)
	root, _ := hex.DecodeString(tree)
	chunks := map[string]int{}
	for i := range int(data[6]) + 1 {
		at := 8 + 12*i
		chunks[string(data[at:at+4])] = int(binary.BigEndian.Uint64(data[at+4 : at+12]))
	}
	count := int(binary.BigEndian.Uint32(data[chunks["OIDF"]+255*4:]))
	found := false
	for i := range count {
		if bytes.Equal(data[chunks["OIDL"]+20*i:chunks["OIDL"]+20*i+20], id) {
			copy(data[chunks["CDAT"]+36*i:], root)
			found = true
		}
	}
	if !found {
		t.Fatalf("the graph holds no commit %s", commit)
	}
	sum := sha1.Sum(data[:len(data)-20])
	copy(data[len(data)-20:], sum[:])
	// Git writes it read-only, which is no protection from its owner.
	if err := os.Chmod(graph, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(graph, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A repository whose configuration puts its working tree somewhere else
// than the directory that holds it is refused, wherever in it git is
// asked, and the refusal does not say where; a linked working tree,
// whose .git is a file, is read as any other. The control is git asked
// plainly, which reads the other tree and says nothing of the change.
func TestAWorkingTreeIsTheDirectoryThatHoldsTheRepository(t *testing.T) {
	ctx := context.Background()
	dir, _, _ := committed(t)
	if err := os.MkdirAll(filepath.Join(dir, "within"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := git.In(ctx, filepath.Join(dir, "within"), git.Options{Offline: true}).Run(ctx, "status", "--porcelain"); err != nil {
		t.Fatalf("a repository as it is made was refused, so the test below shows nothing: %v", err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	plain(t, dir, "worktree", "add", "--quiet", linked)
	if _, err := git.In(ctx, linked, git.Options{Offline: true}).Run(ctx, "status", "--porcelain"); err != nil {
		t.Errorf("a linked working tree was refused: %v", err)
	}
	elsewhere := filepath.Join(t.TempDir(), "a-secret-of-the-tests")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "f"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain(t, dir, "config", "core.worktree", elsewhere)
	for _, at := range []string{dir, filepath.Join(dir, "within")} {
		for _, options := range []git.Options{{Offline: true}, {Told: true}} {
			out, err := git.In(ctx, at, options).Run(ctx, "status", "--porcelain")
			if err == nil {
				t.Errorf("in %s, a working tree put elsewhere was read: %q", at, out)
			} else if strings.Contains(err.Error(), "a-secret-of-the-tests") {
				t.Errorf("the refusal says where the working tree was put: %v", err)
			}
		}
	}
	// The control: asked plainly, git reads the other tree.
	if status := plain(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("asked plainly, git says %q, so the test above shows nothing", status)
	}
}

// A setting that cannot be listed, for whatever reason, is one that
// cannot be turned off: the directory is refused, and nothing it names
// is run. A git that fails only the listing stands in for any such
// reason; the control is the same git listing as it does, under which
// the directory is read and the filter is not run.
func TestADirectoryWhoseSettingsCannotBeListedIsRefused(t *testing.T) {
	ctx := context.Background()
	dir, _, _ := committed(t)
	ran := filepath.Join(t.TempDir(), "ran")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("* filter=planted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain(t, dir, "config", "filter.planted.clean", "sh -c 'echo filter >> "+ran+"; cat'")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("TWO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	failing := t.TempDir()
	wrapper := "#!/bin/sh\nif [ -e " + filepath.Join(failing, "fail") + " ]; then for a; do [ \"$a\" = --get-regexp ] && exit 128; done; fi\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(failing, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", failing+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The control: listed, the directory is read and runs nothing.
	if _, err := git.In(ctx, dir, git.Options{Offline: true}).Run(ctx, "status", "--porcelain"); err != nil {
		t.Fatalf("listed, the directory was refused, so the test below shows nothing: %v", err)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("listed, the filter ran, so the test below shows nothing")
	}
	if err := os.WriteFile(filepath.Join(failing, "fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, options := range []git.Options{{Offline: true}, {Told: true}} {
		if out, err := git.In(ctx, dir, options).Run(ctx, "status", "--porcelain"); err == nil {
			t.Errorf("not listed, the directory was read: %q", out)
		}
	}
	if planted, err := os.ReadFile(ran); err == nil {
		t.Errorf("not listed, what the directory names ran: %s", planted)
	}
}

// Git asked over a remote that never answers stops when what asked is
// cancelled, and so does every program it started: the helper that
// speaks http holds the connection, and lets it go. The control is git
// started as a context alone stops it, which leaves the helper holding
// the connection and is not done until it is let go.
func TestGitAndWhatItStartedStopWhenCancelled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 16)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				close(accepted)
				return
			}
			accepted <- conn
		}
	}()
	remote := "http://" + listener.Addr().String() + "/code.git"
	dir := t.TempDir()
	const bound = 3 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	if _, err := git.In(ctx, dir, git.Options{Told: true}).Run(ctx, "ls-remote", remote); err == nil {
		t.Fatal("a remote that never answers was answered")
	}
	if took := time.Since(started); took > time.Second+bound {
		t.Errorf("git stopped %s after it was asked, %s after it was cancelled", took, took-time.Second)
	}
	held := connection(t, accepted)
	if err := held.SetReadDeadline(time.Now().Add(bound)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, held); err != nil {
		t.Errorf("the helper still holds the connection %s after git was cancelled: %v", bound, err)
	}
	held.Close()

	// The control: started as a context alone stops it.
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", remote)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	done := make(chan struct{})
	go func() {
		_ = cmd.Run()
		close(done)
	}()
	held = connection(t, accepted)
	select {
	case <-done:
		t.Errorf("started as a context alone stops it, git was done within %s, so the test above shows nothing", time.Second+bound)
	case <-time.After(time.Second + bound):
	}
	// Let go, the helper ends, and so the control is done.
	held.Close()
	<-done
}

// connection is the next connection made to the listener, which git
// makes as soon as it is asked.
func connection(t *testing.T, accepted <-chan net.Conn) net.Conn {
	t.Helper()
	select {
	case conn, open := <-accepted:
		if !open {
			t.Fatal("the listener stopped")
		}
		return conn
	case <-time.After(10 * time.Second):
		t.Fatal("git made no connection")
		return nil
	}
}

// A hook the repository's configuration names (hook.<name>.command),
// which a newer git than some runs whatever its directory of hooks is
// (2.56 does), is not run. The control is git asked plainly: where it runs no such hook,
// this git has none, and there is nothing to hold.
func TestAHookTheConfigurationNamesIsNotRun(t *testing.T) {
	ctx := context.Background()
	dir, _, second := committed(t)
	ran := filepath.Join(t.TempDir(), "ran")
	plain(t, dir, "config", "hook.planted.command", "echo hook >> "+ran)
	plain(t, dir, "config", "hook.planted.event", "reference-transaction")
	if _, err := git.In(ctx, dir, git.Options{Offline: true}).Run(ctx, "update-ref", "refs/tags/hardened", second); err != nil {
		t.Fatal(err)
	}
	if planted, err := os.ReadFile(ran); err == nil {
		t.Fatalf("writing a reference ran the hook the configuration names: %s", planted)
	}
	plain(t, dir, "update-ref", "refs/tags/plainly", second)
	if _, err := os.ReadFile(ran); err != nil {
		t.Skipf("asked plainly, git (%s) runs no hook its configuration names, so there is nothing to hold here", plain(t, dir, "--version"))
	}
}

// A push goes to the remote git was given, sends what it was asked to
// send, and says and runs nothing the repository adds: its settings name
// no other address for the remote (by the remote's own address as its
// name, as git reads one, or as origin's), send no option to the server,
// sign with no program, and mirror nothing. The control is git asked
// plainly, beside it, which each of them steers.
func TestAPushGoesToTheRemoteGivenAndSaysNothingTheRepositoryAdds(t *testing.T) {
	ctx := context.Background()
	source, _, second := committed(t)
	// A remote that takes options and signed pushes, and notes what a
	// push told it; and a sink, where a push that is steered ends.
	bare := func() string {
		dir := filepath.Join(t.TempDir(), "remote.git")
		plain(t, filepath.Dir(dir), "clone", "--quiet", "--bare", source, dir)
		plain(t, dir, "config", "receive.advertisePushOptions", "true")
		plain(t, dir, "config", "receive.certNonceSeed", "seed")
		hook := "#!/bin/sh\necho \"options=${GIT_PUSH_OPTION_COUNT:-0} signed=${GIT_PUSH_CERT:+yes}\" >> \"$GIT_DIR/told\"\ncat > /dev/null\n"
		if err := os.WriteFile(filepath.Join(dir, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	told := func(dir string) string {
		out, _ := os.ReadFile(filepath.Join(dir, "told"))
		return strings.TrimSpace(string(out))
	}
	has := func(dir, tag string) bool {
		cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/tags/"+tag)
		cmd.Dir = dir
		return cmd.Run() == nil
	}
	remote, sink := bare(), bare()
	address := "file://" + remote
	clone := filepath.Join(t.TempDir(), "clone")
	plain(t, filepath.Dir(clone), "clone", "--quiet", address, clone)
	ran := filepath.Join(t.TempDir(), "ran")
	signer := filepath.Join(t.TempDir(), "signer")
	if err := os.WriteFile(signer, []byte("#!/bin/sh\necho signer >> "+ran+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plant := func() {
		for _, setting := range [][2]string{
			{"remote." + address + ".pushurl", sink},
			{"remote.origin.pushurl", sink},
			{"push.pushOption", "ci.skip"},
		} {
			plain(t, clone, "config", setting[0], setting[1])
		}
	}
	plant()
	plain(t, clone, "tag", "hardened", second)
	plain(t, clone, "tag", "plainly", second)
	hardened := git.In(ctx, clone, git.Options{Told: true, Remote: address})
	if _, err := hardened.Run(ctx, "push", "--atomic", "origin", "refs/tags/hardened:refs/tags/hardened"); err != nil {
		t.Fatalf("a push to the remote given was refused: %v", err)
	}
	if !has(remote, "hardened") || has(sink, "hardened") || told(remote) != "options=0 signed=" {
		t.Errorf("the remote has the tag: %v, the sink: %v, and the remote was told %q; want the remote alone, told no option", has(remote, "hardened"), has(sink, "hardened"), told(remote))
	}
	// Control: asked plainly, by the address or by origin, it goes to
	// the sink, with the option.
	plain(t, clone, "push", "--quiet", address, "refs/tags/plainly:refs/tags/plainly")
	if !has(sink, "plainly") || has(remote, "plainly") || told(sink) != "options=1 signed=" {
		t.Fatalf("asked plainly, the push went to the remote: %v, the sink: %v, told %q: there is nothing to hold here", has(remote, "plainly"), has(sink, "plainly"), told(sink))
	}
	// A push the repository says to sign runs no program to sign it
	// with, and one it says mirrors sends what was asked alone.
	plain(t, clone, "config", "push.gpgSign", "true")
	plain(t, clone, "config", "gpg.program", signer)
	plain(t, clone, "config", "remote.origin.mirror", "true")
	plain(t, clone, "tag", "signed", second)
	if _, err := hardened.Run(ctx, "push", "--atomic", "origin", "refs/tags/signed:refs/tags/signed"); err != nil {
		t.Fatalf("a push the repository says to sign and to mirror was refused: %v", err)
	}
	if planted, err := os.ReadFile(ran); err == nil || !has(remote, "signed") || has(remote, "plainly") {
		t.Errorf("the push ran the program the repository names to sign with (%q), or the remote has the tag: %v, and what was not asked: %v", planted, has(remote, "signed"), has(remote, "plainly"))
	}
	cmd := exec.Command("git", "push", "--quiet", "origin", "refs/tags/signed:refs/tags/signed")
	cmd.Dir = clone
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	_ = cmd.Run()
	if _, err := os.ReadFile(ran); err != nil {
		t.Skipf("asked plainly, git (%s) runs no program to sign a push with here, so that part holds nothing", plain(t, clone, "--version"))
	}
}
