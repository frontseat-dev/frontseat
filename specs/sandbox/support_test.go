// Package sandbox_test states what frontseat supports of workspaces in
// sandboxes, as specs a person can read, and proves each one against real
// sandboxes on this machine's container runtime.
//
// A spec acts as a person at their own machine does: through the server
// that makes sandboxes and through what the client is made of. It knows
// the Frontseat API and not what serves it. This file is what the specs
// are written with; the files beside it are the specs.
package sandbox_test

import "github.com/frontseat-dev/frontseat/sdk/go/entity"

import (
	"bufio"
	connect "connectrpc.com/connect/v2"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/core/ports"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/testsupport/testidentity"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"golang.org/x/crypto/ssh"

	sandboxv1 "github.com/frontseat-dev/frontseat/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testbuild"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testsandbox"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1/buildv1alpha1connect"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testowner"
	"github.com/frontseat-dev/frontseat/testsupport/backstagetest"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
	"github.com/frontseat-dev/frontseat/testsupport/testtelemetry"
	"github.com/frontseat-dev/frontseat/testsupport/testwire"
	"github.com/frontseat-dev/frontseat/workspace/server/controlplanetest"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Acceptance scenarios require their infrastructure; absence is a failure.

// planes are the control planes the specs started, by their address.
var planes = map[string]controlplanetest.Served{}

// known keeps a control plane the specs started, to be reached by its
// address.
func known(plane controlplanetest.Served) { planes[plane.URL] = plane }

// planeOf is the control plane an address is of: the plane's own, or a
// way beneath it.
func planeOf(at string) controlplanetest.Served {
	for address, plane := range planes {
		if at == address || strings.HasPrefix(at, address+"/") {
			return plane
		}
	}
	panic("no control plane the specs started is at " + at)
}

// owned presents the token of the owner of the control plane an address
// is of, as its owner's client does.
func owned(at string) frontseat.Option { return planeOf(at).Owner }

// ownedClient is the SDK's client of the control plane an address is
// of, as its owner.
func ownedClient(at string) *frontseat.Client {
	fs, err := frontseat.New(at, owned(at))
	if err != nil {
		panic(err)
	}
	return fs
}

// ownerToken is the token of the owner of the control plane an address
// is of, for a client that is none of the SDK's.
func ownerToken(at string) string {
	raw, err := os.ReadFile(planeOf(at).TokenFile)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(raw))
}

// beneathOf is where the sandbox service beneath the control plane at an
// address is asked: served for the specs alone, as the control plane
// serves it to no one.
func beneathOf(at string) string { return planeOf(at).Sandboxes }

// wire is a connection to the API at an address beneath a control
// plane the specs started, as its owner, for what a spec asks that the
// SDK does not serve: a request's own fields.
func wire(at string) *connect.Client { return testwire.Conn(at, testowner.Client(ownerToken(at), at)) }

// daemon is the build daemon of a workspace, reached from outside its
// sandbox as its owner can (testbuild): a workspace is built from inside
// it, so the daemon is the one the command line starts there, started
// where none serves, and asked over a port the control plane carries.
// A box with none fails the spec, and the spec's end lets go of what
// carried it there.
func (w *Workspace) daemon() *testbuild.Daemon {
	w.scenario.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return testbuild.Workspace(w.sdk()).Daemon(ctx, w.scenario.TB)
}

// here is what a workspace's build daemon builds, as the SDK reaches
// it: the workspace of a client made for that daemon.
func (w *Workspace) here() *frontseat.Workspace {
	w.scenario.Helper()
	here, err := w.daemon().Here()
	if err != nil {
		w.scenario.TB.Fatal(err)
	}
	return here
}

// built is a connection to a workspace's build daemon, over the wire,
// for what a spec asks that the SDK does not serve.
func built(w *Workspace) *connect.Client {
	daemon := w.daemon()
	return testwire.Conn(daemon.Address, daemon.HTTP)
}

// buildsAt, graphAt and runsAt are the builds, the graph and the runs
// of a workspace's build daemon, over the wire.
func buildsAt(w *Workspace) buildv1alpha1connect.BuildServiceClient {
	return buildv1alpha1connect.NewBuildServiceClient(built(w))
}

func graphAt(w *Workspace) buildv1alpha1connect.GraphServiceClient {
	return buildv1alpha1connect.NewGraphServiceClient(built(w))
}

func runsAt(w *Workspace) buildv1alpha1connect.RunServiceClient {
	return buildv1alpha1connect.NewRunServiceClient(built(w))
}

// workspacesAt are the workspaces of the control plane at an address,
// over the wire, as its owner.
func workspacesAt(at string) workspacev1alpha1connect.WorkspaceServiceClient {
	return workspacev1alpha1connect.NewWorkspaceServiceClient(wire(at))
}

// reached is a workspace as a spec reaches it: the control plane that
// keeps it, its name and its sandbox, as the control plane last said
// them.
type reached struct {
	Server, Workspace, Sandbox string
}

// reachedOf is a workspace a control plane described, as a spec
// reaches it; nothing of one it never described. Its sandbox is the
// sandbox service's to say, beneath the control plane: a workspace's
// status says nothing of it.
func reachedOf(server string, w *workspacev1.Workspace) reached {
	if w == nil {
		return reached{}
	}
	return reached{Server: server, Workspace: w.GetName(), Sandbox: livingIn(server, w.GetName())}
}

// livingIn is the sandbox the workspace of a name lives in, of the
// control plane at an address, as the sandbox service beneath it says:
// the newest the control plane made for it that is not ending; empty
// for none.
func livingIn(server, name string) string {
	listed, err := testsandbox.Client(beneathOf(server)).ListSandboxes(context.Background(), &sandboxv1.ListSandboxesRequest{PageSize: 1000, Labels: map[string]string{"frontseat.dev/workspace": name}})
	if err != nil {
		panic(err)
	}
	var newest *sandboxv1.Sandbox
	for _, each := range listed.GetSandboxes() {
		if each.GetState() == sandboxv1.Sandbox_STATE_ENDING || each.GetSpec().GetLabels()["frontseat.dev/workspace-request"] == "" {
			continue
		}
		if newest == nil || each.GetCreateTime().AsTime().After(newest.GetCreateTime().AsTime()) {
			newest = each
		}
	}
	return newest.GetId()
}

// sdk is the workspace as the SDK reaches it, as the owner of its
// control plane.
func (r reached) sdk() *frontseat.Workspace {
	return ownedClient(r.Server).Workspace(r.Workspace)
}

// making is a workspace to make, of the control plane at Server.
type making struct {
	Server string
	frontseat.WorkspaceSpec
}

// made makes a workspace as the owner of its control plane asks for it,
// waits until it can be worked in, and says how it came to be.
func made(ctx context.Context, m making) (reached, frontseat.OpenResult, error) {
	opened, err := ownedClient(m.Server).Open(ctx, m.WorkspaceSpec)
	return reachedOf(m.Server, opened.Status), opened, err
}

var (
	// server is the server that makes sandboxes, and image what they run.
	server, image string
	// company is a small company's source, which a sandbox clones over
	// HTTP, as from any remote. The entity gateway
	// is in its repository platform, and depends on greet, which is in
	// its repository services.
	company testforge.Forge
	// companyCatalog is the fake Backstage of the company's entities,
	// which every control plane of the specs resolves them through.
	companyCatalog backstagetest.Served
	// sources is where the repositories the specs write are kept on this
	// machine, and forge the address a sandbox clones them from.
	sources, forge string
	// shared is the workspace every spec that only looks shares, since a
	// sandbox takes a while to make; sharedSandboxes are the sandboxes it
	// has lived in.
	shared          *reached
	sharedSandboxes []string
)

// The package runs sequentially. TestMain owns the expensive shared services;
// each scenario owns its assertions and per-test cleanup.
type scenario struct {
	testing.TB
	*WithT
}

var suiteDirectory string
var suiteClosers []func()

func TestMain(m *testing.M) {
	testfiles.Suite("frontseat-sandbox-specs-", func(root string) int {
		suiteDirectory = root
		if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		exit := m.Run()
		for _, id := range sharedSandboxes {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			_, err := testsandbox.Client(beneathOf(server)).DeleteSandbox(ctx, &sandboxv1.DeleteSandboxRequest{SandboxId: id})
			cancel()
			if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
				fmt.Fprintln(os.Stderr, err)
				exit = 1
			}
		}
		// Stop the control plane before the fixture forges it may still use.
		for _, close := range suiteClosers {
			close()
		}
		return exit
	})
}

func newScenario(t *testing.T) *scenario {
	t.Helper()
	tb := testrequired.TB(t)
	if server == "" {
		attempt, err := os.MkdirTemp(suiteDirectory, "fixture-")
		if err != nil {
			t.Fatal(err)
		}
		var closers []func()
		ready := false
		defer func() {
			if ready {
				return
			}
			// Fatal runs deferred cleanup too. An incomplete attempt owns
			// no shared fixture and must leave no server using its state.
			for _, close := range closers {
				close()
			}
			if err := os.RemoveAll(attempt); err != nil {
				t.Error(err)
			}
		}()
		dir, err := os.MkdirTemp(attempt, "company-")
		if err != nil {
			t.Fatal(err)
		}
		remote, closeForge := testforge.Start(tb, dir)
		closers = append(closers, closeForge)
		fixture := testforge.MakeAt(tb, dir, remote)
		// The control plane resolves the company's entities through a fake
		// Backstage of them, as an operator's --backstage has it do.
		catalog, closeCatalog := backstagetest.Start(tb, fixture.Entities(tb))
		closers = append(closers, closeCatalog)
		plane, close := controlplanetest.StartAt(tb, filepath.Join(attempt, "state"), controlplanetest.WithBackstage(tb, catalog))
		closers = append(closers, close)
		address, built := plane.URL, plane.Image
		from, err := os.MkdirTemp(attempt, "sources-")
		if err != nil {
			t.Fatal(err)
		}
		served, closeSources := testforge.Start(tb, from)
		closers = append(closers, closeSources)
		// Nothing becomes available until the whole fixture is ready.
		server, image, company, companyCatalog, sources, forge = address, built, fixture, catalog, from, served
		known(plane)
		suiteClosers = append(suiteClosers, closers...)
		ready = true
	}
	return &scenario{TB: tb, WithT: NewWithT(t)}
}

// aRepository returns a new directory for a spec to write a repository
// in. Whatever a spec writes there, a workspace clones as "code".
func (spec *scenario) aRepository() string {
	spec.Helper()
	parent, err := os.MkdirTemp(sources, "source")
	spec.Expect(err).NotTo(HaveOccurred())
	directory := filepath.Join(parent, "code")
	spec.Expect(os.Mkdir(directory, 0o755)).To(Succeed())
	return directory
}

// commitsIn runs git in a repository of the spec's, as a person who
// commits there.
func (spec *scenario) commitsIn(directory string, args ...string) {
	spec.Helper()
	spec.git(directory, append([]string{"-c", "user.name=a spec", "-c", "user.email=a-spec@example.com"}, args...)...)
}

// cloning is a workspace to make that clones what a spec wrote in a
// directory aRepository gave it, committed as it is now. A sandbox is
// given no directory of this machine: its supervisor clones, as from any
// remote.
func (spec *scenario) cloning(directory string) making {
	spec.Helper()
	testforge.Committed(spec.TB, directory)
	at, err := filepath.Rel(sources, directory)
	spec.Expect(err).NotTo(HaveOccurred())
	return making{WorkspaceSpec: frontseat.WorkspaceSpec{Repositories: []frontseat.Repository{{Remote: forge + "/" + filepath.ToSlash(at)}}, AuthorizedKey: testidentity.Authorized()}}
}

// ready follows a workspace, for five minutes at most, until the control
// plane says no more of it, holds that it is then ready, and returns it
// as it is then.
func (spec *scenario) ready(ctx context.Context, workspaces workspacev1alpha1connect.WorkspaceServiceClient, name string) *workspacev1.Workspace {
	spec.Helper()
	waiting, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	waited, err := workspaces.WaitWorkspace(waiting, &workspacev1.WaitWorkspaceRequest{Name: name})
	spec.Expect(err).NotTo(HaveOccurred())
	last := waited.GetWorkspace()
	spec.Expect(last.GetStatus().GetState()).To(Equal(workspacev1.WorkspaceStatus_STATE_READY), last.GetStatus().GetFailure())
	return last
}

// code is where a sandbox holds the repository a spec wrote.
const code = "/work/code"

// BeRefusedSaying holds of a refusal that says a thing.
func BeRefusedSaying(claim string) OmegaMatcher { return MatchError(ContainSubstring(claim)) }

// Workspace is a workspace in a sandbox, as this machine knows it: the
// server that made the sandbox, and the sandbox.
type Workspace struct {
	*scenario
	reached
}

// theWorkspace returns a workspace that cloned a repository holding one
// Go module, hello. Every spec that only looks shares it.
// It is running: made the first time, made again if a spec deleted it,
// resumed if one left it paused.
func (spec *scenario) theWorkspace() *Workspace {
	spec.Helper()
	ctx := context.Background()
	sandboxes := testsandbox.Client(beneathOf(server))
	if shared != nil {
		now, err := sandboxes.DescribeSandbox(ctx, &sandboxv1.DescribeSandboxRequest{SandboxId: shared.Sandbox})
		switch {
		case err != nil:
			shared = nil
		case now.GetSandbox().GetState() == sandboxv1.Sandbox_STATE_PAUSED:
			(&Workspace{scenario: spec, reached: *shared}).resume()
		}
	}
	if shared == nil {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		create := spec.cloning(spec.aRepositoryOfHello())
		create.Server, create.Image, create.Name = server, image, aName()
		got, _, err := made(ctx, create)
		if got.Sandbox != "" {
			sharedSandboxes = append(sharedSandboxes, got.Sandbox)
		}
		spec.Expect(err).NotTo(HaveOccurred())
		shared = &got
	}
	return &Workspace{scenario: spec, reached: *shared}
}

// aRepositoryOfHello is a repository a spec wrote that holds one Go
// module, hello.
func (spec *scenario) aRepositoryOfHello() string {
	spec.Helper()
	checkout := spec.aRepository()
	testfiles.Tree(spec.TB, checkout, map[string]string{
		"hello/go.mod":  "module example.com/hello\n\ngo 1.27\n",
		"hello/main.go": "package main\n\nfunc main() {}\n",
	})
	testpins.Go(spec.TB, checkout)
	return checkout
}

const sandboxCatalog = "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: app}\nspec: {type: service, lifecycle: production, owner: team}\n---\napiVersion: backstage.io/v1alpha1\nkind: Group\nmetadata: {name: team}\nspec: {type: team, children: []}\n"

// generatedSandbox is a workspace in a sandbox of its own that cloned a
// repository whose Go module is compiled with what Buf generates.
func (spec *scenario) generatedSandbox() *Workspace {
	spec.Helper()
	checkout := spec.aRepository()
	testforge.GeneratedGo(spec.TB, checkout)
	return spec.inASandboxOfItsOwn(spec.cloning(checkout))
}

// catalogWorkspace is an isolated checkout for catalog discovery and repair.
func (spec *scenario) catalogWorkspace() *Workspace {
	spec.Helper()
	checkout := spec.aRepository()
	testpins.Go(spec.TB, checkout)
	testfiles.Tree(spec.TB, checkout, map[string]string{
		"frontseat.yaml":        "plugins: {go: {}, backstage: {}}\n",
		"app/catalog-info.yaml": sandboxCatalog,
		"app/go.mod":            "module example.com/app\n\ngo 1.27\n",
		"app/main.go":           "package main\n\nfunc main() {}\n",
	})
	return spec.inASandboxOfItsOwn(spec.cloning(checkout))
}

// inASandboxOfItsOwn makes a workspace in a sandbox of its own that
// clones from the company's source, and ends the sandbox with the spec.
func (spec *scenario) inASandboxOfItsOwn(create making) *Workspace {
	spec.Helper()
	if create.Server == "" {
		create.Server = server
	}
	if create.Image == "" {
		create.Image = image
	}
	// The spec's key opens a shell in it, as a person's does in theirs.
	if create.AuthorizedKey == "" {
		create.AuthorizedKey = testidentity.Authorized()
	}
	// A name of the spec's own: specs clone repositories of one name, and
	// each is to have its own workspace.
	if create.Name == "" {
		create.Name = aName()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// Register the spec-owned name before creation: a lost reply still cleans it.
	spec.Cleanup(func() { endedAt(create.Server, create.Name) })
	got, _, err := made(ctx, create)
	spec.Expect(err).NotTo(HaveOccurred())
	return &Workspace{spec, got}
}

// workspaceInASandboxOf returns a workspace made, in a sandbox of its
// own, from an entity of the company's catalog.
func (spec *scenario) workspaceInASandboxOf(described string) *Workspace {
	spec.Helper()
	return spec.inASandboxOfItsOwn(making{WorkspaceSpec: frontseat.WorkspaceSpec{
		Entities: []entity.Pattern{entity.Pattern(entity.New("", "", described))},
	}})
}

// shell logs in to the sandbox over SSH, as an editor does.
func (w *Workspace) shell() *ssh.Client {
	w.scenario.Helper()
	shell, err := testidentity.Shell(context.Background(), w.sdk())
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return shell
}

// run runs a command in the sandbox over SSH and returns what it
// printed, on one line, and how it exited.
func (w *Workspace) run(command string) (printed string, exit int) {
	w.scenario.Helper()
	shell := w.shell()
	defer shell.Close()
	session, err := shell.NewSession()
	w.scenario.Expect(err).NotTo(HaveOccurred())
	out, err := session.Output(command)
	printed = strings.Join(strings.Fields(string(out)), " ")
	var exited *ssh.ExitError
	if errors.As(err, &exited) {
		return printed, exited.ExitStatus()
	}
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return printed, 0
}

// ranBeside is what a command prints that the server runs in the
// sandbox itself, beside the box's supervisor and started neither by it
// nor by a command that builds, on one line.
func (w *Workspace) ranBeside(arguments ...string) string {
	w.scenario.Helper()
	got, err := testsandbox.Client(beneathOf(server)).ExecSandbox(context.Background(), &sandboxv1.ExecSandboxRequest{SandboxId: w.Sandbox, Arguments: arguments})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return strings.Join(strings.Fields(string(got.GetOutput())), " ")
}

// prints is what a command in the sandbox prints, on one line.
func (w *Workspace) prints(command string) string {
	w.scenario.Helper()
	printed, _ := w.run(command)
	return printed
}

// exits is how a command in the sandbox exits.
func (w *Workspace) exits(command string) int {
	w.scenario.Helper()
	_, exit := w.run(command)
	return exit
}

// holds is what a file of the sandbox holds, as a command in it reads
// it: every byte, and not on one line.
func (w *Workspace) holds(path string) string {
	w.scenario.Helper()
	shell := w.shell()
	defer shell.Close()
	session, err := shell.NewSession()
	w.scenario.Expect(err).NotTo(HaveOccurred())
	content, err := session.Output("cat -- " + quoted(path))
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return string(content)
}

// quoted is a word as a shell takes it whole.
func quoted(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// session is a session of the sandbox's shell, and what closes it and
// the shell.
func (w *Workspace) session() (*ssh.Session, func()) {
	w.scenario.Helper()
	shell := w.shell()
	session, err := shell.NewSession()
	if err != nil {
		shell.Close()
	}
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return session, func() {
		session.Close()
		shell.Close()
	}
}

// expectReclaimed holds that a shell comes, within seconds, to be able
// to forward an address that another let go of.
func (spec *scenario) expectReclaimed(next *ssh.Client, address string) {
	spec.Helper()
	spec.Eventually(func() error {
		reclaimed, err := next.Listen("tcp", address)
		if err != nil {
			return err
		}
		return reclaimed.Close()
	}, 5*time.Second, 20*time.Millisecond).Should(Succeed(), address)
}

// servesFileTransfer is whether the sandbox's shell serves SFTP, which is
// how an editor or scp copies a file to a machine.
func (w *Workspace) servesFileTransfer() bool {
	w.scenario.Helper()
	session, done := w.session()
	defer done()
	return session.RequestSubsystem("sftp") == nil
}

// boundingCapabilities are the capabilities what runs in the sandbox
// could ever hold, as the kernel says them: a bit for each.
func (w *Workspace) boundingCapabilities() uint64 {
	w.scenario.Helper()
	printed, exit := w.run("grep ^CapBnd: /proc/self/status")
	w.scenario.Expect(exit).To(Equal(0))
	fields := strings.Fields(printed)
	w.scenario.Expect(fields).To(HaveLen(2))
	held, err := strconv.ParseUint(fields[1], 16, 64)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return held
}

// copyIn has a file of the sandbox hold a content, written there by a
// command in it, as a person or an agent working in the sandbox writes
// one: nothing is copied into a sandbox, which serves no transfer of
// files.
func (w *Workspace) copyIn(path, content string) *Workspace {
	w.scenario.Helper()
	session, done := w.session()
	defer done()
	session.Stdin = strings.NewReader(content)
	w.scenario.Expect(session.Run("cat > " + quoted(path))).To(Succeed())
	return w
}

// answersOnPort is whether a connection forwarded from this machine to a
// port of the sandbox is answered.
func (w *Workspace) answersOnPort(port int) bool {
	w.scenario.Helper()
	shell := w.shell()
	defer shell.Close()
	through := &http.Client{Transport: &http.Transport{Dial: shell.Dial}, Timeout: 30 * time.Second}
	response, err := through.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		return false
	}
	response.Body.Close()
	return true
}

// letsInAKeyItWasNotGiven is whether the sandbox lets in someone who
// logs in with a key it was not given.
func (w *Workspace) letsInAKeyItWasNotGiven() bool {
	w.scenario.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	strangers, err := ssh.NewSignerFromKey(private)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	conn, err := testsandbox.Tunnel(context.Background(), beneathOf(w.Server), w.Sandbox, ports.Shell)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	defer conn.Close()
	config := &ssh.ClientConfig{User: "stranger", Auth: []ssh.AuthMethod{ssh.PublicKeys(strangers)}, HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	_, _, _, err = ssh.NewClientConn(conn, w.Sandbox, config)
	return err == nil
}

// answersTo runs a program in the sandbox and tells it one line after
// another over one stream, which stays open between them: each line is
// answered before the next is sent. It returns the answers.
func (w *Workspace) answersTo(program string, lines ...string) []string {
	w.scenario.Helper()
	session, done := w.session()
	defer done()
	told, err := session.StdinPipe()
	w.scenario.Expect(err).NotTo(HaveOccurred())
	answers, err := session.StdoutPipe()
	w.scenario.Expect(err).NotTo(HaveOccurred())
	w.scenario.Expect(session.Start(program)).To(Succeed())
	reader := bufio.NewReader(answers)
	var answered []string
	for _, line := range lines {
		_, err := fmt.Fprintln(told, line)
		w.scenario.Expect(err).NotTo(HaveOccurred())
		answer, err := reader.ReadString('\n')
		w.scenario.Expect(err).NotTo(HaveOccurred())
		answered = append(answered, strings.TrimSpace(answer))
	}
	told.Close()
	w.scenario.Expect(session.Wait()).To(Succeed())
	return answered
}

// pause pauses the workspace's sandbox.
func (w *Workspace) pause() *Workspace {
	w.scenario.Helper()
	_, err := testsandbox.Client(beneathOf(w.Server)).PauseSandbox(context.Background(), &sandboxv1.PauseSandboxRequest{SandboxId: w.Sandbox})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return w
}

// resume picks a paused sandbox up again.
func (w *Workspace) resume() *Workspace {
	w.scenario.Helper()
	_, err := testsandbox.Client(beneathOf(w.Server)).ResumeSandbox(context.Background(), &sandboxv1.ResumeSandboxRequest{SandboxId: w.Sandbox})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return w
}

// delete deletes the workspace, sandbox and all.
func (w *Workspace) delete() *Workspace {
	w.scenario.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	w.scenario.Expect(w.sdk().Delete(ctx)).To(Succeed())
	return w
}

// state is what the workspace's sandbox is doing now.
func (w *Workspace) state() string {
	w.scenario.Helper()
	now, err := testsandbox.Client(beneathOf(w.Server)).DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: w.Sandbox})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return strings.ToLower(strings.TrimPrefix(now.GetSandbox().GetState().String(), "STATE_"))
}

// askingAfterItsSandbox is why nothing could be said of the workspace's
// sandbox; nil where something could.
func (w *Workspace) askingAfterItsSandbox() error {
	_, err := testsandbox.Client(beneathOf(w.Server)).DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: w.Sandbox})
	return err
}

// Build is a build that has ended, and the workspace it was asked of.
type Build struct {
	*scenario

	of  *Workspace
	run *buildv1.Build
}

// asking is how a build is asked for.
type asking func(*buildv1.CreateBuildRequest)

// build asks the workspace's build daemon, in its sandbox, for a build,
// and follows it to its end.
func (w *Workspace) build(how ...asking) *Build {
	w.scenario.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	request := askedFor(how)
	started, err := w.here().Build(ctx, frontseat.BuildRequest{Targets: request.GetTargets(), Only: request.GetOnly(), Profile: request.GetProfile(), Fix: request.GetFix(), Tests: request.GetTests()})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	ended, err := started.Wait(ctx)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return &Build{scenario: w.scenario, of: w, run: ended}
}

// askedFor is the request a build is asked for with.
func askedFor(how []asking) *buildv1.CreateBuildRequest {
	request := &buildv1.CreateBuildRequest{}
	for _, each := range how {
		each(request)
	}
	return request
}

// buildWith is build, asked of a client of the wire's own.
func (w *Workspace) buildWith(runs buildv1alpha1connect.BuildServiceClient, how ...asking) *Build {
	w.scenario.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	request := askedFor(how)
	created, err := runs.CreateBuild(ctx, request)
	w.scenario.Expect(err).NotTo(HaveOccurred())
	ended, err := runs.WaitBuild(ctx, &buildv1.WaitBuildRequest{BuildId: created.GetBuild().GetId()})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return &Build{scenario: w.scenario, of: w, run: ended.GetBuild()}
}

// checkedThenFixed asks for a format check of a target, which fails and
// leaves the file at path as it was, unformatted; then for the same with
// fixing, which succeeds. It returns the actions of the run that fixed.
func (w *Workspace) checkedThenFixed(target, path, unformatted string) *buildv1.ListBuildActionsResponse {
	w.scenario.Helper()
	checked := w.build(func(r *buildv1.CreateBuildRequest) {
		r.Targets, r.Only = []string{target}, []string{"format"}
	})
	w.scenario.Expect(checked.outcome()).To(Equal("BUILD_FAILURE"), checked.run.String())
	w.scenario.Expect(w.holds(path)).To(Equal(unformatted), "a check wrote the checkout")

	fixed := w.build(func(r *buildv1.CreateBuildRequest) {
		r.Targets, r.Only, r.Fix = []string{target}, []string{"format"}, true
	})
	actions, err := buildsAt(w).ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: fixed.run.GetId()})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	w.scenario.Expect(fixed.outcome()).To(Equal("SUCCESS"), actions.String())
	return actions
}

// outcome is how the build ended, in Bazel's names.
func (b *Build) outcome() string {
	return strings.TrimPrefix(b.run.GetExitCode().String(), "EXIT_CODE_")
}

// passed is how many of its actions passed.
func (b *Build) passed() int { return int(b.run.GetCounts().GetPassed()) }

// madeBy is the driver that made the workspace's sandbox.
func (w *Workspace) madeBy() string {
	w.scenario.Helper()
	now, err := testsandbox.Client(beneathOf(w.Server)).DescribeSandbox(context.Background(), &sandboxv1.DescribeSandboxRequest{SandboxId: w.Sandbox})
	w.scenario.Expect(err).NotTo(HaveOccurred())
	return now.GetSandbox().GetDriver()
}

// askingForASandboxThatReachesAnything is why a sandbox that may reach
// anything was not given; nil where it was.
func askingForASandboxThatReachesAnything() error {
	created, err := testsandbox.Client(beneathOf(server)).CreateSandbox(context.Background(), &sandboxv1.CreateSandboxRequest{
		Spec: &sandboxv1.SandboxSpec{
			Image:  image,
			Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_ALL},
		},
	})
	if err == nil {
		_, _ = testsandbox.Client(beneathOf(server)).DeleteSandbox(context.Background(), &sandboxv1.DeleteSandboxRequest{SandboxId: created.GetSandbox().GetId()})
	}
	return err
}

// Sandbox is a sandbox that was asked for directly, as a system that
// sends agents asks for one.
type Sandbox struct {
	*scenario
	id string
}

// wanting is what is asked of a sandbox.
type wanting func(*sandboxv1.CreateSandboxRequest)

// labelled says what a sandbox is for: labels, each written name=value.
func labelled(labels ...string) wanting {
	return func(r *sandboxv1.CreateSandboxRequest) {
		r.Spec.Labels = map[string]string{}
		for _, each := range labels {
			name, value, _ := strings.Cut(each, "=")
			r.Spec.Labels[name] = value
		}
	}
}

// sentAs names the request, as whoever may send it twice names it.
func sentAs(request string) wanting {
	return func(r *sandboxv1.CreateSandboxRequest) { r.RequestId = request }
}

// aRequestName names a request.
func aRequestName() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	raw[6], raw[8] = raw[6]&0x0f|0x40, raw[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

// askForASandbox asks for a sandbox, and ends it with the spec.
func (spec *scenario) askForASandbox(what ...wanting) *Sandbox {
	spec.Helper()
	request := &sandboxv1.CreateSandboxRequest{Spec: &sandboxv1.SandboxSpec{Image: image, Egress: &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_LISTED}}}
	for _, each := range what {
		each(request)
	}
	created, err := testsandbox.Client(beneathOf(server)).CreateSandbox(context.Background(), request)
	spec.Expect(err).NotTo(HaveOccurred())
	id := created.GetSandbox().GetId()
	spec.Cleanup(func() {
		_, _ = testsandbox.Client(beneathOf(server)).DeleteSandbox(context.Background(), &sandboxv1.DeleteSandboxRequest{SandboxId: id})
	})
	return &Sandbox{scenario: spec, id: id}
}

// sandboxesLabelled are the sandboxes found by what they are for: each
// label written name=value.
func (spec *scenario) sandboxesLabelled(labels ...string) []string {
	spec.Helper()
	selected := map[string]string{}
	for _, label := range labels {
		name, value, _ := strings.Cut(label, "=")
		selected[name] = value
	}
	listed, err := testsandbox.Client(beneathOf(server)).ListSandboxes(context.Background(), &sandboxv1.ListSandboxesRequest{PageSize: 1000, Labels: selected})
	spec.Expect(err).NotTo(HaveOccurred())
	var ids []string
	for _, found := range listed.GetSandboxes() {
		ids = append(ids, found.GetId())
	}
	return ids
}

// aName is a workspace's name no other spec gives.
func aName() string {
	var raw [6]byte
	_, _ = rand.Read(raw[:])
	return fmt.Sprintf("spec-%x", raw[:])
}

// ended ends a workspace a spec made and waits until it is gone, so that
// its name is free for the spec that comes after: ending is done by the
// control plane, after the call that asks for it.
func ended(name string) { endedAt(server, name) }

func endedAt(address, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = ownedClient(address).Workspace(name).Delete(ctx)
}

// The test supplies operator telemetry settings to a private control plane.
// A collector listens on a supported SSH reverse forward inside each sandbox;
// no host is added to that sandbox's egress policy.
func (spec *scenario) retainedPlane() string {
	spec.Helper()
	plane := controlplanetest.StartWithEnvironment(spec.TB, map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
		"OTEL_TRACES_EXPORTER":        "otlp",
		"OTEL_METRICS_EXPORTER":       "none",
		"OTEL_LOGS_EXPORTER":          "none",
		"OTEL_BSP_SCHEDULE_DELAY":     "10",
	}, controlplanetest.WithBackstage(spec.TB, companyCatalog))
	known(plane)
	return plane.URL
}

// anotherPlane starts a control plane beside the specs' own, with a
// state and an owner of its own, over the same gateway and grid, and
// returns its address: another person's.
func (spec *scenario) anotherPlane() string {
	spec.Helper()
	plane := controlplanetest.Start(spec.TB, controlplanetest.WithBackstage(spec.TB, companyCatalog))
	known(plane)
	return plane.URL
}

func (spec *scenario) retainedTool(at string) *Workspace {
	spec.Helper()
	w := spec.inASandboxOfItsOwn(making{Server: at, WorkspaceSpec: frontseat.WorkspaceSpec{
		Entities: entitiesForRetainedTool(),
	}})
	spec.Expect(w.Server).To(Equal(at))
	return w
}

func entitiesForRetainedTool() []entity.Pattern {
	return []entity.Pattern{{Kind: "component", Namespace: "default", Name: "tool"}}
}

func (w *Workspace) observeRetained() *testtelemetry.Collector {
	w.Helper()
	shell := w.shell()
	w.Cleanup(func() { _ = shell.Close() })
	listener, err := shell.Listen("tcp", "127.0.0.1:4318")
	w.Expect(err).NotTo(HaveOccurred())
	return testtelemetry.StartHTTPOn(w.TB, listener)
}

const retainedCompile = "//lab/tool:go.compile"

func retainedCompileOnly(r *buildv1.CreateBuildRequest) {
	r.Targets = []string{retainedCompile}
}

func (w *Workspace) uniqueRetainedProgram() {
	w.Helper()
	w.copyIn("/work/lab/tool/main.go", "package main\n\nimport \"github.com/spf13/cobra\"\n\nfunc main() { _ = (&cobra.Command{Use: \""+rand.Text()+"\"}).Execute() }\n")
}

func (w *Workspace) retainedToolProject() *buildv1.Project {
	w.Helper()
	listed, err := graphAt(w).ListProjects(context.Background(), &buildv1.ListProjectsRequest{PageSize: 1000})
	w.Expect(err).NotTo(HaveOccurred())
	var matching []*buildv1.Project
	for _, p := range listed.GetProjects() {
		if p.GetRef().GetRepository() == "lab" && p.GetRef().GetRoot() == "tool" && p.GetRef().GetPlugin() == "go" {
			matching = append(matching, p)
		}
	}
	w.Expect(matching).To(HaveLen(1))
	return matching[0]
}

func (b *Build) retainedCompileResult() *buildv1.ActionResult {
	b.Helper()
	listed, err := buildsAt(b.of).ListBuildActions(context.Background(), &buildv1.ListBuildActionsRequest{PageSize: 1000, BuildId: b.run.GetId()})
	b.Expect(err).NotTo(HaveOccurred())
	if b.outcome() != "SUCCESS" {
		b.Logf("Run failure: %s\n", b.run.GetFailure())
		for _, action := range listed.GetActions() {
			logs, err := frontseat.Collect(b.of.here().BuildByID(b.run.GetId()).Logs(context.Background(), action.GetLabel()))
			b.Logf("Action: %s\nLogs: %s; error: %v\n", action, logs, err)
		}
	}
	b.Expect(b.outcome()).To(Equal("SUCCESS"), b.run.GetFailure())
	var matching []*buildv1.ActionResult
	for _, action := range listed.GetActions() {
		if action.GetLabel() == retainedCompile {
			matching = append(matching, action)
		}
	}
	b.Expect(matching).To(HaveLen(1))
	return matching[0]
}

type workspaceStages struct{ evaluate, plan *tracepb.Span }

func (b *Build) retainedStages(c *testtelemetry.Collector, retained, planned, didAsk bool) workspaceStages {
	b.Helper()
	stages := b.stages(c)
	b.Expect(testtelemetry.RequireBool(b.TB, stages.evaluate, "retained")).To(Equal(retained))
	b.Expect(testtelemetry.RequireBool(b.TB, stages.plan, "retained")).To(Equal(planned))
	if didAsk {
		b.Expect(testtelemetry.RequireInt(b.TB, stages.evaluate, "asked")).To(BeNumerically(">", 0))
	} else {
		b.Expect(testtelemetry.RequireInt(b.TB, stages.evaluate, "asked")).To(BeZero())
	}
	again := testtelemetry.RequireInt(b.TB, stages.evaluate, "again")
	if retained {
		b.Expect(again).To(BeZero())
	}
	return stages
}

// stages returns a build's evaluation and its plan, as its own collector
// was told them: one of each, of one trace and one parent.
func (b *Build) stages(c *testtelemetry.Collector) workspaceStages {
	b.Helper()
	var stages workspaceStages
	b.Eventually(func(g Gomega) {
		spans := c.TraceSpansFor("cicd.pipeline.run.id", b.run.GetId())
		g.Expect(spans).NotTo(BeEmpty(), "this sandbox's run must export to its own collector")
		stages = workspaceStages{}
		for _, span := range spans {
			switch span.GetName() {
			case "evaluate":
				g.Expect(stages.evaluate).To(BeNil())
				stages.evaluate = span
			case "plan":
				g.Expect(stages.plan).To(BeNil())
				stages.plan = span
			}
		}
		g.Expect(stages.evaluate).NotTo(BeNil())
		g.Expect(stages.plan).NotTo(BeNil())
	}, 15*time.Second).Should(Succeed())
	b.Expect(stages.evaluate.TraceId).To(Equal(stages.plan.TraceId))
	b.Expect(stages.evaluate.ParentSpanId).To(Equal(stages.plan.ParentSpanId))
	return stages
}

func (w *Workspace) retainedModule(name string) {
	w.Helper()
	file := "/work/lab/tool/go.mod"
	before := w.holds(file)
	line, rest, found := strings.Cut(before, "\n")
	w.Expect(found).To(BeTrue())
	w.Expect(line).To(HavePrefix("module "))
	w.copyIn(file, "module "+name+"\n"+rest)
}

// A process ID can repeat after resume, so also observe its kernel start
// time. The daemon is the build daemon the command line starts in the
// box, which is there once a command that builds has run: one is run
// first, so that a sandbox picked up again, whose daemon went with it,
// has the one that follows.
func (w *Workspace) retainedDaemonIdentity() string {
	w.Helper()
	w.Expect(w.exits("frontseat build list")).To(Equal(0))
	identity := w.prints(buildDaemons + ` | while read p; do awk '{print $1, $22}' "$p/stat"; done`)
	w.Expect(strings.Fields(identity)).To(HaveLen(2))
	return identity
}

// buildDaemons prints, in a sandbox, the /proc directory of each build
// daemon there: a process of the command line's program started as
// `frontseat daemon`. supervisors prints that of each box's supervisor.
const (
	buildDaemons = `for p in /proc/[0-9]*; do if [ "$(readlink "$p/exe" 2>/dev/null)" = /opt/frontseat/bin/frontseat ] && [ "$(tr '\0' '\n' < "$p/cmdline" | sed -n 2p)" = daemon ]; then echo "$p"; fi; done`
	supervisors  = `for p in /proc/[0-9]*; do if [ "$(readlink "$p/exe" 2>/dev/null)" = /opt/frontseat/bin/frontseat-box ]; then echo "$p"; fi; done`
)
