package sandbox_test

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/sdk/go"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/server/testsupport/testidentity"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"
	"github.com/frontseat-dev/frontseat/server/testsupport/testsandbox"
	. "github.com/onsi/gomega"
)

// No credential lives in a sandbox. A repository that asks a password is
// cloned all the same: the control plane is given its owner's credential
// for the host, which is kept outside every sandbox, and a workspace of
// theirs that fetches a repository of that host is let through with it.
// What the sandbox holds is a stand-in, worth nothing anywhere else.
//
// OpenShell's gateway is what holds it.
// Private source in sandboxes / A workspace clones a repository that asks a password, which its sandbox never holds.
func TestAWorkspaceClonesARepositoryThatAsksAPasswordWhichItsSandboxNeverHolds(t *testing.T) {
	spec := newScenario(t)

	const password = "a-password-of-this-specs-own-making"
	root := spec.TB.TempDir()
	for repository, files := range map[string]map[string]string{
		"code":  {"hello/go.mod": "module example.com/hello\n\ngo 1.27\n", "hello/main.go": "package main\n\nfunc main() {}\n"},
		"other": {"README": "another private repository\n"},
	} {
		directory := filepath.Join(root, repository)
		testfiles.Tree(spec.TB, directory, files)
		if repository == "code" {
			testpins.Go(spec.TB, directory)
		}
		testforge.Committed(spec.TB, directory)
		// The forge takes a push from whoever gives the password, so
		// that a push it does not get was stopped before it.
		spec.git(directory, "config", "http.receivepack", "true")
	}
	forge := testforge.ServeGuarded(spec.TB, root, "someone", password)
	// The spec's forge is asked over http, and the credential is given
	// for that.
	host := forge
	credentials := frontseat.New(server, owned(server)).Credentials()
	ctx := context.Background()

	err := credentials.Set(ctx, frontseat.Credential{Host: host, Username: "someone", Secret: password})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Cleanup(func() {
		_ = credentials.Delete(context.Background(), host)
	})
	private := making{Spec: frontseat.Spec{Repositories: []frontseat.Repository{{Remote: forge + "/code"}}, AuthorizedKeys: testidentity.Authorized()}}

	isolated := spec.inASandboxOfItsOwn(private)

	// The daemon cloned it, and it builds.
	spec.Expect(isolated.prints("ls " + code)).To(ContainSubstring("hello"))
	spec.Expect(isolated.build().outcome()).To(Equal("SUCCESS"))
	// Nothing in the sandbox holds the password: not what it is told,
	// not what it wrote.
	spec.Expect(isolated.prints("env; git config --list")).NotTo(ContainSubstring(password))
	spec.Expect(isolated.prints("grep -r -l -F " + password + " /work /tmp /var/lib/frontseat \"$HOME\" 2>/dev/null; echo searched")).To(Equal("searched"))
	// It is let fetch that repository and no other of the forge's, and
	// push nothing.
	spec.Expect(isolated.exits("GIT_TERMINAL_PROMPT=0 git ls-remote " + forge + "/code")).To(Equal(0))
	spec.Expect(isolated.prints("GIT_TERMINAL_PROMPT=0 git ls-remote " + forge + "/other 2>&1")).To(ContainSubstring("403"))
	_, port, err := net.SplitHostPort(strings.TrimPrefix(forge, "http://"))
	spec.Expect(err).NotTo(HaveOccurred())
	spec.git(filepath.Join(root, "code"), "push", "--quiet", "http://someone:"+password+"@127.0.0.1:"+port+"/code", "HEAD:refs/heads/from-outside")
	spec.Expect(isolated.prints("cd " + code + " && git -c user.name=x -c user.email=x@example.com commit -q --allow-empty -m x && GIT_TERMINAL_PROMPT=0 git push origin HEAD:refs/heads/pushed 2>&1")).To(ContainSubstring("403"))
	// The forge holds the branch pushed from outside, the control, and
	// not the sandbox's.
	spec.Expect(spec.git(filepath.Join(root, "code"), "branch", "--list", "--format=%(refname:short)")).To(And(ContainSubstring("from-outside"), Not(ContainSubstring("pushed"))))

	// A credential a sandbox is still let through with is kept; with
	// the sandbox ended it is let go of, and a workspace made anew is
	// turned away by the forge.
	err = credentials.Delete(ctx, host)
	spec.Expect(err).To(BeRefusedSaying("still let through"))
	isolated.delete()
	spec.Eventually(func() error {
		return credentials.Delete(ctx, host)
	}, time.Minute, time.Second).Should(Succeed())
	private.Server, private.Image = server, image
	asking, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	without, _, err := made(asking, private)
	if without.Workspace != "" {
		spec.Cleanup(func() {
			ended(without.Workspace)
		})
	}
	spec.Expect(err).To(BeRefusedSaying("could not read Username"))
}

// A workspace asked for with push pushes what was done in it to its own
// repository, and still holds no credential: the gateway presents it.
// The control is the spec above, whose workspace was not asked for so
// and whose push is turned away.
// Pushing from sandboxes / A workspace made to push pushes to its repository and to no other, with a credential its sandbox never holds.
func TestAWorkspaceMadeToPushPushesToItsRepositoryAndToNoOtherWithACredentialItsSandboxNeverHolds(t *testing.T) {
	spec := newScenario(t)

	const password = "a-password-of-this-specs-own-making"
	root := spec.TB.TempDir()
	for _, repository := range []string{"code", "other"} {
		directory := filepath.Join(root, repository)
		spec.Expect(os.MkdirAll(directory, 0o755)).To(Succeed())
		spec.Expect(os.WriteFile(filepath.Join(directory, "README"), []byte("private source\n"), 0o644)).To(Succeed())
		testforge.Committed(spec.TB, directory)
		spec.git(directory, "config", "http.receivepack", "true")
	}
	forge := testforge.ServeGuarded(spec.TB, root, "someone", password)
	credentials := frontseat.New(server, owned(server)).Credentials()
	err := credentials.Set(context.Background(), frontseat.Credential{Host: forge, Username: "someone", Secret: password})
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Cleanup(func() {
		_ = credentials.Delete(context.Background(), forge)
	})

	workspace := spec.inASandboxOfItsOwn(making{Spec: frontseat.Spec{
		Repositories: []frontseat.Repository{{Remote: forge + "/code"}},
		Push:         true,
	}})

	// Its sandbox's record says it may push.
	record, err := testsandbox.Client(beneathOf(server)).DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: workspace.Sandbox})
	spec.Expect(err).NotTo(HaveOccurred())
	// Its sandbox's record says, in the network's terms, that it may
	// push: what a push asks is let through, with the host's credential.
	var pushes bool
	for _, rule := range record.GetSandbox().GetSpec().GetEgress().GetRules() {
		for _, request := range rule.GetRequests() {
			pushes = pushes || (rule.GetCredential() != "" && strings.HasSuffix(request.GetPath(), "/git-receive-pack"))
		}
	}
	spec.Expect(pushes).To(BeTrue())
	// What is committed there is pushed, and the forge holds it.
	spec.Expect(workspace.exits("cd " + code + " && echo done-in-the-sandbox > made && git add made && git -c user.name=x -c user.email=x@example.com commit -q -m made && GIT_TERMINAL_PROMPT=0 git push -q origin HEAD:refs/heads/pushed")).To(Equal(0))
	spec.Expect(spec.git(filepath.Join(root, "code"), "show", "pushed:made")).To(Equal("done-in-the-sandbox\n"))
	// Another repository of the forge takes nothing from it.
	spec.Expect(workspace.prints("cd " + code + " && GIT_TERMINAL_PROMPT=0 git push " + forge + "/other HEAD:refs/heads/pushed 2>&1")).To(ContainSubstring("403"))
	spec.Expect(spec.git(filepath.Join(root, "other"), "branch", "--list", "--format=%(refname:short)")).NotTo(ContainSubstring("pushed"))
	// Nothing in the sandbox holds the password.
	spec.Expect(workspace.prints("env; git config --list")).NotTo(ContainSubstring(password))
	spec.Expect(workspace.prints("grep -r -l -F " + password + " /work /tmp /var/lib/frontseat \"$HOME\" 2>/dev/null; echo searched")).To(Equal("searched"))
}

// git runs git in a directory of the spec's forge and returns what it
// printed: the forge's own keeping, not the workspace's.
func (spec *scenario) git(directory string, args ...string) string {
	spec.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	out, err := cmd.CombinedOutput()
	spec.Expect(err).NotTo(HaveOccurred(), string(out))
	return string(out)
}
