// Package e2e holds what is proven only by every part together: a client,
// the sandbox API over the OpenShell gateway, and a box, its supervisor
// and the build daemon a command starts there.
package e2e

import (
	"context"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/testsupport/testidentity"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testbuild"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgateway"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testowner"
	"github.com/frontseat-dev/frontseat/testsupport/backstagetest"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// sdkClient is the SDK's client of a server a test is given.
func sdkClient(t testing.TB, server string, options ...frontseat.Option) *frontseat.Client {
	t.Helper()
	fs, err := frontseat.New(server, options...)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

// madeWorkspace makes a workspace of a control plane, as the client
// given asks for it, and ends its sandbox with the test. It returns the
// workspace as it was once it could be worked in.
func madeWorkspace(t *testing.T, fs *frontseat.Client, create frontseat.WorkspaceSpec) (*frontseat.Workspace, *workspacev1.Workspace) {
	t.Helper()
	// The test's key opens a shell in it, as a person's does in theirs.
	create.AuthorizedKey = testidentity.Authorized()
	// A name of the test's own: the stack's control plane outlives a run,
	// and a name derived from a repository the tests all call alike would
	// be another run's.
	if create.Name == "" {
		create.Name = fmt.Sprintf("test-%d", time.Now().UnixNano())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	made, err := fs.Open(ctx, create)
	if made.Workspace != nil {
		t.Cleanup(func() { ended(made.Workspace) })
	}
	if err != nil {
		t.Fatal(err)
	}
	return made.Workspace, made.Status
}

// ended ends a workspace a test made, and waits until it is gone.
func ended(w *frontseat.Workspace) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = w.Delete(ctx)
}

// buildIn builds a workspace and returns the run as it ended, with the
// labels of its actions. A workspace is built from inside it: the build
// is asked of the build daemon in its sandbox, which a test outside
// reaches as the workspace's owner can (testbuild).
func buildIn(t *testing.T, w *frontseat.Workspace) (*buildv1.Build, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	daemon, err := testbuild.Workspace(w).Daemon(ctx)
	if err != nil {
		t.Fatal(err)
	}
	here, err := daemon.Here()
	if err != nil {
		t.Fatal(err)
	}
	built, err := here.Build(ctx, frontseat.BuildRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ended, err := built.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := frontseat.Collect(built.Actions(ctx))
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, a := range actions {
		labels = append(labels, a.GetLabel())
		if a.GetOutcome() != buildv1.Outcome_OUTCOME_PASSED {
			logs, err := frontseat.Collect(built.Logs(ctx, a.GetLabel()))
			t.Logf("%s %v %s: %v (%v)", a.GetLabel(), a.GetOutcome(), a.GetMessage(), logs, err)
		}
	}
	return ended, strings.Join(labels, " ")
}

// cloned is a workspace to make that clones what a directory holds,
// committed as it is now, from a forge of the test's: a sandbox is given
// no directory of this machine.
func cloned(t *testing.T, directory string) []frontseat.Repository {
	t.Helper()
	testforge.Committed(t, directory)
	return []frontseat.Repository{{Remote: testforge.Serve(t, filepath.Dir(directory)) + "/" + filepath.Base(directory)}}
}

// helloModule is a directory holding a Go module, pinned as this
// repository pins Go.
func helloModule(t *testing.T) string {
	t.Helper()
	checkout := filepath.Join(t.TempDir(), "code")
	testfiles.Tree(t, checkout, map[string]string{
		"hello/go.mod":  "module example.com/hello\n\ngo 1.27\n",
		"hello/main.go": "package main\n\nfunc main() {}\n",
	})
	testpins.Go(t, checkout)
	return checkout
}

func TestAWorkspaceClonesARepositoryAndBuildsIt(t *testing.T) {
	image := testbox.Image(t)
	checkout := helloModule(t)

	at := local(t)
	made, _ := madeWorkspace(t, sdkClient(t, at.URL, at.Owner), frontseat.WorkspaceSpec{Image: image, Repositories: cloned(t, checkout)})

	// It is built from inside it, and is not built through the control
	// plane, which neither serves nor forwards the build API.
	if _, err := made.Build(context.Background(), frontseat.BuildRequest{}); !errors.Is(err, frontseat.ErrBuiltInside) {
		t.Errorf("a build asked of the workspace through its control plane: %v, want %v", err, frontseat.ErrBuiltInside)
	}
	ended, labels := buildIn(t, made)
	if ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || ended.GetCounts().GetPassed() != 3 {
		t.Errorf("the build ended %s with %d actions passed (%s), want SUCCESS and 3", ended.GetExitCode(), ended.GetCounts().GetPassed(), labels)
	}
}

func TestAnEditorOpensTheWorkspaceOverSSHThroughTheSandboxAPI(t *testing.T) {
	image := testbox.Image(t)
	checkout := filepath.Join(t.TempDir(), "code")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "main.go"), []byte("package main\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	at := local(t)
	made, _ := madeWorkspace(t, sdkClient(t, at.URL, at.Owner), frontseat.WorkspaceSpec{Image: image, Repositories: cloned(t, checkout)})

	// What an editor does first: log in, look at the machine, and reach
	// it through a forwarded connection.
	shell, err := testidentity.Shell(context.Background(), made)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.Close()

	session, err := shell.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err := session.Output("pwd && ls code && uname -s")
	if err != nil || strings.Join(strings.Fields(string(out)), " ") != "/work main.go Linux" {
		t.Errorf("a command in the sandbox printed %q (%v), want the workspace, its file and Linux", out, err)
	}

	// Nothing is copied in: the shell serves no transfer of files, and a
	// file is written by a command run in the sandbox.
	asking, err := shell.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := asking.RequestSubsystem("sftp"); err == nil {
		t.Errorf("the sandbox's shell serves SFTP")
	}
	asking.Close()
	session, err = shell.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	session.Stdin = strings.NewReader("written in the sandbox")
	if err := session.Run("cat > /tmp/back-end"); err != nil {
		t.Fatal(err)
	}
	session, err = shell.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := session.Output("cat /tmp/back-end"); err != nil || string(out) != "written in the sandbox" {
		t.Errorf("the file written in the sandbox holds %q (%v)", out, err)
	}

	// The supervisor's source API stands in for a back end listening
	// inside.
	through := &http.Client{Transport: &http.Transport{Dial: shell.Dial}}
	response, err := through.Get(fmt.Sprintf("http://127.0.0.1:%d/", ports.Source))
	if err != nil {
		t.Fatalf("a connection forwarded to a port of the sandbox: %v", err)
	}
	response.Body.Close()
}

// built builds one of frontseat's programs from this checkout, into a
// directory of the test's, and returns where it is.
func built(t *testing.T, program string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), filepath.Base(program))
	if said, err := exec.Command("go", "build", "-o", out, "github.com/frontseat-dev/frontseat/"+program).CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", program, err, said)
	}
	return out
}

// localServer is frontseat-server, as a person starts it, on a port of
// its own, with a state of its own and the flags given beside: where it
// is asked, and its state. It is stopped when the test ends.
func localServer(t *testing.T, program string, flags ...string) (string, string) {
	t.Helper()
	known := testgateway.Required(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	state := t.TempDir()
	server := exec.Command(program, append([]string{"--listen", address, "--state", state, "--openshell-identity", known}, flags...)...)
	server.Stdout, server.Stderr = os.Stderr, os.Stderr
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.Process.Signal(os.Interrupt)
		_ = server.Wait()
		endLeft(t, known, state)
	})
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, err := http.Get("http://" + address + "/")
		if err == nil {
			response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("frontseat-server did not answer on %s: %v", address, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "http://" + address, state
}

// A workspace is opened by entity alone where the control plane's
// operator gave it a Backstage: frontseat-server --backstage, here a fake
// Backstage that holds the forge's entities, resolves component:gateway
// to the repository its backstage.io/source-location names, and greet,
// which it depends on, to the other; the box clones those and builds
// them. The control is frontseat-server without --backstage, which
// refuses the same UNIMPLEMENTED, ENTITIES_NOT_SERVED, and makes no
// sandbox.
func TestAWorkspaceOpenedByEntityClonesWhatItsBackstageSays(t *testing.T) {
	testgrid.InSandbox(t)
	image := testbox.Image(t)
	// The daemon in the sandbox clones from the forge over HTTP, as from
	// any remote; the control plane resolves through the Backstage.
	forge := testforge.Served(t)
	catalog := backstagetest.Serve(t, forge.Entities(t))
	server, command := built(t, "cmd/frontseat-server"), built(t, "cli/cmd/frontseat")
	ctx := context.Background()

	// The control: no Backstage, and opening by entity is refused by name.
	unserved, unservedState := localServer(t, server, "--box", image)
	raw, err := os.ReadFile(filepath.Join(unservedState, "owner", "token"))
	if err != nil {
		t.Fatal(err)
	}
	refusing := sdkClient(t, unserved, frontseat.WithToken(strings.TrimSpace(string(raw))))
	_, err = refusing.Open(ctx, frontseat.WorkspaceSpec{Name: fmt.Sprintf("test-%d", time.Now().UnixNano()), Image: image, Entities: []entity.Pattern{{Kind: "component", Name: "gateway"}}})
	if reason, _ := frontseat.Reason(err); connect.CodeOf(err) != connect.CodeUnimplemented || reason != "ENTITIES_NOT_SERVED" {
		t.Errorf("opened by entity without a Backstage: %v (%s), want it refused UNIMPLEMENTED, ENTITIES_NOT_SERVED", err, reason)
	}
	if listed, err := frontseat.Collect(refusing.Workspaces(ctx)); err != nil || len(listed) != 0 {
		t.Errorf("what was refused left %d workspaces (%v)", len(listed), err)
	}

	at, state := localServer(t, server, "--box", image, "--backstage", catalog.URL, "--backstage-token", catalog.TokenFile, "--backstage-loopback")
	token := filepath.Join(state, "owner", "token")
	config := testowner.LayOut(t, token, at)
	name := fmt.Sprintf("test-%d", time.Now().UnixNano())
	raw, err = os.ReadFile(token)
	if err != nil {
		t.Fatal(err)
	}
	owner := sdkClient(t, at, frontseat.WithToken(strings.TrimSpace(string(raw))))
	t.Cleanup(func() { ended(owner.Workspace(name)) })
	open := exec.Command(command, "workspace", "create", "--name", name, "--image", image, "--no-shell", "--entity", "component:gateway")
	open.Env = append(os.Environ(), "XDG_CONFIG_HOME="+config)
	if said, err := open.CombinedOutput(); err != nil {
		t.Fatalf("frontseat workspace create --entity component:gateway: %v\n%s", err, said)
	}
	made := owner.Workspace(name)
	held, err := made.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var holds []string
	for _, r := range held.GetStatus().GetRepositories() {
		holds = append(holds, r.GetName()+":"+strings.Join(r.GetRoots(), ","))
	}
	sort.Strings(holds)
	if got := strings.Join(holds, " "); got != "platform:gateway services:greet" {
		t.Fatalf("the workspace holds %q, want gateway of platform and greet of services", got)
	}
	for _, r := range held.GetStatus().GetRepositories() {
		if want := forge.Repository(r.GetName()); r.GetRemote() != want {
			t.Errorf("%s was cloned from %s, want %s, which the source location names", r.GetName(), r.GetRemote(), want)
		}
	}
	ended, labels := buildIn(t, made)
	if ended.GetExitCode() != buildv1.ExitCode_EXIT_CODE_SUCCESS || ended.GetCounts().GetPassed() != 6 {
		t.Errorf("the build ended %s with %d actions passed, want SUCCESS and 6", ended.GetExitCode(), ended.GetCounts().GetPassed())
	}
	if !strings.Contains(labels, "//platform/gateway:go.compile") || !strings.Contains(labels, "//services/greet:go.compile") {
		t.Errorf("the build ran %s, want the entities' modules, one in each repository", labels)
	}
}
