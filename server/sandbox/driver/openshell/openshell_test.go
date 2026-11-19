package openshell_test

import (
	"context"
	"errors"
	"github.com/frontseat-dev/frontseat/local/ports"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/testsupport/drivertest"
	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testcommits"
	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/server/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgateway"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
	"github.com/frontseat-dev/frontseat/server/testsupport/testlisten"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrun"

	"github.com/frontseat-dev/frontseat/server/internal/driver/container"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver/openshell"
)

// The port the daemon in a box serves the build API on.
const buildPort = ports.Daemon

// driven returns a driver over the local stack's gateway, and skips the
// test where none answers.
// fetching is what these tests' forge is fetched by, as git asks it and
// written out here in the network's own terms, since the sandbox layer
// knows no version control system: the program in the box that fetches,
// the requests a fetch and a push of a repository make, and how git is
// told a password. That the control plane writes the same of git's own
// answer is the sandbox specs' to prove (specs/sandbox/credentials_test.go).
type fetching struct{}

func (fetching) Requests(repository string) []driver.Request {
	return []driver.Request{
		{Method: "GET", Path: repository + "/info/refs", Query: map[string]string{"service": "git-upload-pack"}},
		{Method: "POST", Path: repository + "/git-upload-pack"},
	}
}

func (fetching) Pushes(repository string) []driver.Request {
	return []driver.Request{
		{Method: "GET", Path: repository + "/info/refs", Query: map[string]string{"service": "git-receive-pack"}},
		{Method: "POST", Path: repository + "/git-receive-pack"},
	}
}

func (fetching) Programs() []string { return []string{"/usr/bin/git"} }

func (fetching) Presenting(told map[string]string, origin, username, variable string) (map[string]string, error) {
	count, _ := strconv.Atoi(told["GIT_CONFIG_COUNT"])
	return map[string]string{
		"GIT_CONFIG_COUNT":                          strconv.Itoa(count + 2),
		"GIT_CONFIG_KEY_" + strconv.Itoa(count):     "credential." + origin + ".username",
		"GIT_CONFIG_VALUE_" + strconv.Itoa(count):   username,
		"GIT_CONFIG_KEY_" + strconv.Itoa(count+1):   "credential." + origin + ".helper",
		"GIT_CONFIG_VALUE_" + strconv.Itoa(count+1): `!f() { printf '%s\n' "password=$` + variable + `"; }; f`,
	}, nil
}

func driven(t *testing.T) *openshell.Driver {
	t.Helper()
	identity := testgateway.Identity(t)
	// The gateway makes its sandboxes with this machine's container
	// runtime, which is asked what an image is started with.
	started := func(ctx context.Context, image string) (openshell.Started, error) {
		entrypoint, command, environment, err := container.Started(ctx, "docker", image)
		return openshell.Started{Entrypoint: entrypoint, Command: command, Environment: environment}, err
	}
	d, err := openshell.New(openshell.Config{Address: testgateway.Address, Identity: identity, Image: started, Writable: []string{"/work", "/var/lib/frontseat"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// box is a sandbox the driver can make, of an id of the name given that
// is this run's own (drivertest.ID): the box image, let reach the grid
// it is told of and nothing else.
func box(t *testing.T, name string) driver.Sandbox {
	t.Helper()
	grid := testgrid.InSandbox(t)
	return drivertest.Labelled(driver.Sandbox{
		ID:        testrun.ID(t, name),
		Image:     testbox.Image(t),
		Arguments: grid.Arguments(),
		Ports:     []int{buildPort},
		// What a test adds to its environment, a credential's stand-in
		// among it, is added here.
		Environment: map[string]string{},
		Egress:      driver.Egress{Listed: true, Rules: []driver.Rule{{Host: grid.Address}, {Host: grid.Assets}}},
	})
}

func TestTheOpenShellDriverConforms(t *testing.T) {
	drivertest.Conforms(t, captureFailures(t, driven(t)), box(t, "frontseat-conformance-openshell"))
}

// A sandbox that is to reach anything is refused: nothing is reached
// under OpenShell that is not named.
func TestASandboxThatReachesAnythingIsRefused(t *testing.T) {
	testrequired.Stack(t)
	d := driven(t)
	open := box(t, "frontseat-openshell-open")
	open.Egress = driver.Egress{}
	t.Cleanup(func() { _ = d.Delete(context.Background(), open.ID) })
	if _, err := d.Apply(context.Background(), open); err == nil {
		t.Fatal("a sandbox that reaches anything was made")
	}
	if _, err := d.Observe(context.Background(), open.ID); err != driver.ErrNotFound {
		t.Fatalf("the refused sandbox is there: %v", err)
	}
}

// What a box serves is asked over a port reached through the gateway,
// one request after another, as the sandbox API asks a daemon.
func TestABoxIsAskedThroughTheGateway(t *testing.T) {
	d := driven(t)
	record := box(t, "frontseat-openshell-asked")
	running(t, d, record)
	through := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return d.Dial(ctx, record.ID, buildPort) },
	}}
	deadline := time.Now().Add(time.Minute)
	for asked := 0; asked < 3; {
		response, err := through.Get("http://sandbox/")
		if err != nil {
			if time.Now().After(deadline) {
				t.Fatalf("the daemon in the box does not answer: %v", err)
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		asked++
	}
}

// password is what the tests' private forges are guarded by: of the
// tests' own making, and guarding nothing but their fixtures.
const password = "a-password-of-this-tests-own-making"

// private makes, beneath a directory of the test's, a repository of each
// name holding a README of private source, and returns the directory.
func private(t *testing.T, repositories ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, repository := range repositories {
		directory := filepath.Join(root, repository)
		testfiles.Write(t, filepath.Join(directory, "README"), "private source\n")
		testforge.Committed(t, directory)
	}
	return root
}

// pushable is private, with each repository taking a push from whoever
// gives the forge's password.
func pushable(t *testing.T, repositories ...string) string {
	t.Helper()
	root := private(t, repositories...)
	for _, repository := range repositories {
		testcommits.Git(t, filepath.Join(root, repository), "config", "http.receivepack", "true")
	}
	return root
}

// shell returns what runs a command of the shell in a sandbox, failing
// the test where it cannot be run, and gives its exit and what it
// printed.
func shell(t *testing.T, d *openshell.Driver, id string) func(command string) (int, string) {
	return func(command string) (int, string) {
		t.Helper()
		exit, printed, _, err := d.Exec(context.Background(), id, "", []string{"sh", "-c", command}, 1<<16)
		if err != nil {
			t.Fatal(err)
		}
		return exit, string(printed)
	}
}

// answering serves every request on a port of this machine that a
// sandbox reaches at host.docker.internal, and returns that address.
func answering(t *testing.T) string {
	t.Helper()
	listener, err := testlisten.Own("tcp")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "reached\n")
	})}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return net.JoinHostPort("host.docker.internal", port)
}

// A sandbox reaches the hosts it names and no other: two servers of
// this machine, alike but for one being named.
func TestASandboxReachesWhatItNamesAndNothingElse(t *testing.T) {
	d := driven(t)
	named, unnamed := answering(t), answering(t)
	record := box(t, "frontseat-openshell-reach")
	record.Egress.Rules = append(record.Egress.Rules, driver.Rule{Host: named})
	running(t, d, record)
	asked := func(host string) (int, string) {
		t.Helper()
		// git is what a box holds that speaks HTTP: reached, it is told the
		// answer is no repository; not reached, that it could not connect.
		exit, printed, _, err := d.Exec(context.Background(), record.ID, "", []string{"git", "ls-remote", "http://" + host + "/any"}, 4096)
		if err != nil {
			t.Fatal(err)
		}
		return exit, string(printed)
	}
	if _, printed := asked(named); !strings.Contains(printed, "not found") && !strings.Contains(printed, "not valid") && !strings.Contains(printed, "not appear to be a git repository") {
		t.Errorf("the host it names was not reached: %s", printed)
	}
	if exit, printed := asked(unnamed); exit == 0 || strings.Contains(printed, "not found") || strings.Contains(printed, "not valid") || strings.Contains(printed, "not appear to be a git repository") {
		t.Errorf("a host it does not name was reached (exit %d): %s", exit, printed)
	}
}

// What runs in a sandbox writes where it is let and nowhere else: a
// directory any user of the image may write, and is not let, beside its
// workspace.
func TestWhatASandboxMayWriteIsHeld(t *testing.T) {
	d := driven(t)
	record := box(t, "frontseat-openshell-write")
	running(t, d, record)
	wrote := func(path string) int {
		t.Helper()
		exit, _, _, err := d.Exec(context.Background(), record.ID, "", []string{"touch", path}, 1024)
		if err != nil {
			t.Fatal(err)
		}
		return exit
	}
	if exit := wrote("/work/written"); exit != 0 {
		t.Fatalf("its workspace could not be written: exit %d", exit)
	}
	if exit := wrote("/mise/written"); exit == 0 {
		t.Error("a directory it is not let write was written")
	}
}

// A label a caller gives does not stand in for a sandbox's id: the
// sandbox is still found by its id, and ended by it.
func TestACallersLabelDoesNotStandInForASandboxsId(t *testing.T) {
	testrequired.Stack(t)
	d := driven(t)
	ctx := context.Background()
	record := box(t, "frontseat-openshell-label")
	record.Labels = map[string]string{"frontseat.dev/sandbox": "another", "probe": record.ID}
	running(t, d, record)
	if err := d.Delete(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Observe(ctx, record.ID); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("the sandbox outlived its ending: %v", err)
	}
	if left, err := d.List(ctx, map[string]string{"probe": record.ID}); err != nil || len(left) != 0 {
		t.Fatalf("the gateway still holds it: %+v, %v", left, err)
	}
}

// Two ids the gateway would name alike are not one sandbox: the second
// is not made over the first, is not found as it, and its ending leaves
// the first as it was.
func TestTwoIdsOfOneNameAreNotOneSandbox(t *testing.T) {
	testrequired.Stack(t)
	d := driven(t)
	ctx := context.Background()
	// Short enough that each is spelled as the gateway's name, and the
	// two are spelled alike.
	first := box(t, "os")
	second := first
	first.ID = strings.Replace(first.ID, "-", "_", 1)
	running(t, d, first)
	if _, err := d.Apply(ctx, second); err == nil {
		t.Error("a sandbox of another id was made over the first, or taken for it")
	}
	if _, err := d.Observe(ctx, second.ID); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("the second id was found as a sandbox: %v", err)
	}
	if _, err := d.Dial(ctx, second.ID, buildPort); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("the second id reached a port of the first: %v", err)
	}
	if err := d.Delete(ctx, second.ID); err != nil {
		t.Errorf("ending the second id: %v", err)
	}
	if got, err := d.Observe(ctx, first.ID); err != nil || got.State != driver.Running {
		t.Errorf("the first is no longer running after the second was ended: %+v, %v", got, err)
	}
}

// What the gateway cannot hold a sandbox to is refused by name, beside
// a size it can.
func TestWhatCannotBeSizedIsRefusedByName(t *testing.T) {
	testrequired.Stack(t)
	d := driven(t)
	ctx := context.Background()
	for what, change := range map[string]func(*driver.Sandbox){
		"a disk size": func(s *driver.Sandbox) { s.DiskBytes = 1 << 30 },
	} {
		record := box(t, "frontseat-openshell-sized")
		change(&record)
		var refused *driver.Unsupported
		if _, err := d.Apply(ctx, record); !errors.As(err, &refused) || refused.Capability != what {
			_ = d.Delete(ctx, record.ID)
			t.Errorf("asking for %s: %v, want it refused for that", what, err)
		}
	}
	sized := box(t, "frontseat-openshell-sized")
	sized.MilliCPUs, sized.MemoryBytes = 1500, 1<<30
	running(t, d, sized)
	// The limits are the sandbox's own, as the container runtime the
	// gateway made it with says them: what runs in it may not read its
	// control group, which is the policy's doing. The workload is held to
	// them (its supervisor is too, with Podman's driver, and is not with
	// Docker's); another run's sandbox is no part of it.
	listed, err := containersOf(ctx, gatewayClient(t), sized.ID)
	if err != nil || len(listed) < 2 {
		t.Fatalf("the sandbox is run in %v (%v), want its workload and its supervisor", listed, err)
	}
	workloads := 0
	for _, name := range listed {
		if supervisor(name) {
			continue
		}
		workloads++
		out, err := testgateway.Runtime(ctx, "inspect", "--format", "{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}", name)
		if err != nil || strings.TrimSpace(string(out)) != "1500000000 1073741824" {
			t.Errorf("the workload %s is held to %q (%v), want one and a half processors and a gibibyte", name, out, err)
		}
	}
	if workloads != 1 {
		t.Errorf("the sandbox is run in %v: %d workloads, want one", listed, workloads)
	}
}

// A sandbox fetches a repository that asks a password, which only the
// gateway holds: the sandbox is given a stand-in, the forge is given
// the password, and nothing in the sandbox holds it. It fetches that
// repository and no other of the forge's, and pushes nothing; without
// the credential it does not fetch.
func TestASandboxFetchesAPrivateRepositoryAndNeverHoldsItsPassword(t *testing.T) {
	d := driven(t)
	ctx := context.Background()
	// The forge takes a push from whoever gives the password, so that a
	// push it does not get was stopped before it.
	root := pushable(t, "code", "other")
	forge := testforge.ServeGuarded(t, root, "someone", password)
	at := forge // the test's forge is asked over http, and the credential kept for that
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(forge, "http://"))
	outside := "http://someone:" + password + "@127.0.0.1:" + port
	id := testrun.ID(t, "fetch")
	t.Cleanup(func() { _ = d.Forget(context.Background(), id) })
	// A credential replaces the one before: a wrong one first.
	if err := d.Credential(ctx, id, at, "someone", "not-the-password"); err != nil {
		t.Fatal(err)
	}
	if err := d.Credential(ctx, id, at, "someone", password); err != nil {
		t.Fatal(err)
	}
	// It is never moved to another host, which a sandbox let through with
	// it would send the new secret to; the control is the one above.
	if err := d.Credential(ctx, id, "elsewhere.example", "someone", password); !errors.Is(err, driver.ErrInvalid) {
		t.Errorf("a credential moved to another host: %v, want it refused", err)
	}
	record := box(t, "frontseat-openshell-fetch")
	fetches(t, d, &record, forge, id, []string{"/code"})
	running(t, d, record)
	in := shell(t, d, record.ID)
	if exit, printed := in("cd /tmp && rm -rf fetched && GIT_TERMINAL_PROMPT=0 git clone --quiet " + forge + "/code fetched && cat fetched/README"); exit != 0 || !strings.Contains(printed, "private source") {
		t.Fatalf("the repository was not fetched (exit %d): %s", exit, printed)
	}
	// Nothing the sandbox is told or has written holds the password:
	// its environment, its git's settings, and every file it may write.
	if _, printed := in("env; git config --list --show-origin; cat /tmp/fetched/.git/config"); strings.Contains(printed, password) {
		t.Error("the sandbox is told the password")
	}
	if exit, printed := in("grep -r -l -F " + password + " /tmp /work /var/lib/frontseat \"$HOME\" 2>/dev/null; true"); exit != 0 || strings.TrimSpace(printed) != "" {
		t.Errorf("a file of the sandbox holds the password: %s", printed)
	}
	// Another repository of the forge, and anything else of this one, is
	// turned away before the forge: by the proxy, which says 403. Each
	// is asked by the program the rule names, or by one it starts (an
	// alias of its own), since what the rule holds of a request is only
	// held for a program it lets through at all.
	started := func(request string) string {
		return "git -c alias.asked='!curl -s -o /dev/null -w \"answered %{http_code}\" " + request + "' asked; false"
	}
	for what, command := range map[string]string{
		"another repository":       "cd /tmp && GIT_TERMINAL_PROMPT=0 git clone --quiet " + forge + "/other another",
		"a file of the repository": started(forge + "/code/HEAD"),
		"its packs, read plainly":  started(forge + "/code/objects/info/packs"),
	} {
		if exit, printed := in(command + " 2>&1"); exit == 0 || !strings.Contains(printed, "403") {
			t.Errorf("%s was not turned away by the proxy (exit %d): %s", what, exit, printed)
		}
	}
	// The control of those: what a fetch itself asks, asked the same
	// way, is let through, and answered by the forge, which asks who is
	// calling, as it asks anyone who presents no password (401).
	if _, printed := in(started("'"+forge+"/code/info/refs?service=git-upload-pack'") + " 2>&1"); !strings.Contains(printed, "answered 401") {
		t.Errorf("what a fetch asks, asked by a program the fetching program started, was not let through: %s", printed)
	}
	// A program the rule does not name, and that none it names started,
	// does not reach the forge at all, for what a fetch asks no more
	// than for anything else.
	if _, printed := in("curl -s -o /dev/null -w 'answered %{http_code}' '" + forge + "/code/info/refs?service=git-upload-pack' 2>&1; true"); !strings.Contains(printed, "answered 000") {
		t.Errorf("a program the fetch's rule does not name reached the forge: %s", printed)
	}
	// A push from outside, with the password, is taken by the forge: the
	// control. The sandbox's push never reaches it.
	testcommits.Git(t, filepath.Join(root, "code"), "push", "--quiet", outside+"/code", "HEAD:refs/heads/from-outside")
	if exit, printed := in("cd /tmp/fetched && git -c user.name=x -c user.email=x@example.com commit --quiet --allow-empty -m x && GIT_TERMINAL_PROMPT=0 git push origin HEAD:refs/heads/pushed 2>&1"); exit == 0 || !strings.Contains(printed, "403") {
		t.Errorf("the sandbox's push was not turned away by the proxy (exit %d): %s", exit, printed)
	}
	branches := testcommits.Git(t, filepath.Join(root, "code"), "branch", "--list", "--format=%(refname:short)")
	if !strings.Contains(branches, "from-outside") || strings.Contains(branches, "pushed") {
		t.Errorf("the forge's repository holds the branches %q, want the one pushed from outside and not the sandbox's", branches)
	}

	// A credential a sandbox is still let through with is not let go of.
	if err := d.Forget(ctx, id); !errors.Is(err, driver.ErrInUse) {
		t.Fatalf("letting go of a credential in use: %v, want it kept", err)
	}
	// The control: with that sandbox ended and the credential let go of,
	// a sandbox made anew is let reach the repository and is turned away
	// by the forge.
	if err := d.Delete(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.Forget(ctx, id); err != nil {
		t.Fatal(err)
	}
	without := box(t, "frontseat-openshell-nofetch")
	fetches(t, d, &without, forge, "", []string{"/code"})
	running(t, d, without)
	exit, printed, _, err := d.Exec(ctx, without.ID, "", []string{"sh", "-c", "cd /tmp && GIT_TERMINAL_PROMPT=0 git clone --quiet " + forge + "/code fetched"}, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	if exit == 0 || !strings.Contains(string(printed), "could not read Username") {
		t.Errorf("without the credential the clone ended %d: %s", exit, printed)
	}
}

// A sandbox let push to a repository pushes to it, with the password
// only the gateway holds, and to no other it fetches; what a push asks
// is let through for the fetching program and what it starts, and for
// no other. The control of the push that is taken is the one beside it
// that is not, and the sandbox that is let push nowhere
// (TestASandboxFetchesAPrivateRepositoryAndNeverHoldsItsPassword).
func TestASandboxPushesToTheRepositoryItIsLetAndToNoOther(t *testing.T) {
	d := driven(t)
	ctx := context.Background()
	// The forge takes a push to either from whoever gives the password.
	root := pushable(t, "code", "other")
	forge := testforge.ServeGuarded(t, root, "someone", password)
	id := testrun.ID(t, "push")
	t.Cleanup(func() { _ = d.Forget(context.Background(), id) })
	if err := d.Credential(ctx, id, forge, "someone", password); err != nil {
		t.Fatal(err)
	}
	record := box(t, "frontseat-openshell-push")
	fetches(t, d, &record, forge, id, []string{"/code", "/other"}, "/code")
	running(t, d, record)
	in := shell(t, d, record.ID)
	const committed = "git -c user.name=x -c user.email=x@example.com commit --quiet --allow-empty -m x"
	for _, repository := range []string{"code", "other"} {
		if exit, printed := in("cd /tmp && GIT_TERMINAL_PROMPT=0 git clone --quiet " + forge + "/" + repository + " " + repository + " && cd " + repository + " && " + committed + " 2>&1"); exit != 0 {
			t.Fatalf("%s was not fetched (exit %d): %s", repository, exit, printed)
		}
	}
	if exit, printed := in("cd /tmp/code && GIT_TERMINAL_PROMPT=0 git push origin HEAD:refs/heads/pushed 2>&1"); exit != 0 {
		t.Errorf("the push to the repository the sandbox is let push to ended %d: %s", exit, printed)
	}
	if exit, printed := in("cd /tmp/other && GIT_TERMINAL_PROMPT=0 git push origin HEAD:refs/heads/pushed 2>&1"); exit == 0 || !strings.Contains(printed, "403") {
		t.Errorf("the push to a repository only fetched was not turned away by the proxy (exit %d): %s", exit, printed)
	}
	for repository, held := range map[string]bool{"code": true, "other": false} {
		branches := testcommits.Git(t, filepath.Join(root, repository), "branch", "--list", "--format=%(refname:short)")
		if strings.Contains(branches, "pushed") != held {
			t.Errorf("the forge's %s holds the branches %q; that it holds the sandbox's: want %v", repository, branches, held)
		}
	}
	// What a push asks is the fetching program's to ask: a program the
	// rule does not name does not reach the forge with it.
	if _, printed := in("curl -s -o /dev/null -w 'answered %{http_code}' '" + forge + "/code/info/refs?service=git-receive-pack' 2>&1; true"); !strings.Contains(printed, "answered 000") {
		t.Errorf("a program the rule does not name asked what a push asks: %s", printed)
	}
	// Nothing the sandbox is told or wrote holds the password.
	if _, printed := in("env; git config --list --show-origin; cat /tmp/code/.git/config"); strings.Contains(printed, password) {
		t.Error("the sandbox is told the password")
	}
}

// A sandbox reaches what its rules say and no more, and the rules are in
// the network's terms alone: a host, the programs that may ask it, the
// requests let through, and whether the credential kept for the host is
// attached. Nothing here says repository, and none of it comes from a
// version control system: what a clone asks is written out as requests,
// and git is told to present the stand-in by the sandbox's own
// environment, as whoever asks for the sandbox would write both. The
// controls are beside each refusal: the request the rule names, the
// program it names, and the host with its credential.
func TestASandboxReachesWhatItsRulesSayAndNoMore(t *testing.T) {
	d := driven(t)
	ctx := context.Background()
	root := pushable(t, "code", "other")
	forge := testforge.ServeGuarded(t, root, "someone", password)
	id := testrun.ID(t, "rules")
	t.Cleanup(func() { _ = d.Forget(context.Background(), id) })

	// A rule that asks for a credential where none is kept makes no
	// sandbox: it is refused, by name.
	rule := driver.Rule{
		Host:     forge,
		Programs: []string{"/usr/bin/git"},
		Requests: []driver.Request{
			{Method: "GET", Path: "/code/info/refs", Query: map[string]string{"service": "git-upload-pack"}},
			{Method: "POST", Path: "/code/git-upload-pack"},
		},
		CredentialID: id,
	}
	if _, _, _, err := d.Kept(ctx, id); !errors.Is(err, driver.ErrNotFound) {
		t.Fatalf("a credential that was never given is said to be kept: %v", err)
	}
	// Whatever becomes of them, the sandboxes these refusals must not make
	// are ended: their ids are this run's own.
	without := box(t, "frontseat-openshell-norule")
	t.Cleanup(func() { _ = d.Delete(context.Background(), without.ID) })
	without.Egress.Rules = []driver.Rule{rule}
	if _, err := d.Apply(ctx, without); !errors.Is(err, driver.ErrInvalid) || !strings.Contains(err.Error(), "no credential") {
		t.Fatalf("a rule with the credential of a host that has none: %v, want it refused by name", err)
	}
	// A host is named by one rule.
	twice := box(t, "frontseat-openshell-twice")
	t.Cleanup(func() { _ = d.Delete(context.Background(), twice.ID) })
	twice.Egress.Rules = []driver.Rule{{Host: "forge.example"}, {Host: "forge.example:443", Programs: []string{"/usr/bin/git"}}}
	if _, err := d.Apply(ctx, twice); !errors.Is(err, driver.ErrInvalid) {
		t.Fatalf("one host named by two rules: %v, want it refused", err)
	}

	if err := d.Credential(ctx, id, forge, "someone", password); err != nil {
		t.Fatal(err)
	}
	_, username, variable, err := d.Kept(ctx, id)
	if err != nil || username != "someone" || variable == "" || strings.Contains(variable, password) {
		t.Fatalf("the credential kept is said to be of %q under %q (%v)", username, variable, err)
	}
	// Git is told to present the stand-in as whoever asks for the sandbox
	// writes it: nothing of this is the driver's.
	record := box(t, "frontseat-openshell-rules")
	told, err := fetching{}.Presenting(nil, forge, username, variable)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range told {
		record.Environment[name] = value
	}
	record.Egress.Rules = []driver.Rule{rule}
	running(t, d, record)
	in := shell(t, d, record.ID)
	if exit, printed := in("cd /tmp && GIT_TERMINAL_PROMPT=0 git clone --quiet " + forge + "/code fetched && cat fetched/README"); exit != 0 || !strings.Contains(printed, "private source") {
		t.Fatalf("what the rule lets through did not get through (exit %d): %s", exit, printed)
	}
	if _, printed := in("env; git config --list --show-origin"); strings.Contains(printed, password) {
		t.Error("the sandbox is told the password")
	}
	// A request the rule does not name, of the same host by the same
	// program, is turned away by the proxy; a push is such a request.
	for what, command := range map[string]string{
		"another path of the host": "cd /tmp && GIT_TERMINAL_PROMPT=0 git clone --quiet " + forge + "/other another",
		"a request not named":      "cd /tmp/fetched && git -c user.name=x -c user.email=x@example.com commit --quiet --allow-empty -m x && GIT_TERMINAL_PROMPT=0 git push origin HEAD:refs/heads/pushed",
	} {
		if exit, printed := in(command + " 2>&1"); exit == 0 || !strings.Contains(printed, "403") {
			t.Errorf("%s was not turned away by the proxy (exit %d): %s", what, exit, printed)
		}
	}
	// A program the rule does not name does not reach the host at all.
	if _, printed := in("curl -s -o /dev/null -w 'answered %{http_code}' '" + forge + "/code/info/refs?service=git-upload-pack' 2>&1; true"); !strings.Contains(printed, "answered 000") {
		t.Errorf("a program the rule does not name reached the host: %s", printed)
	}
	if branches := testcommits.Git(t, filepath.Join(root, "code"), "branch", "--list", "--format=%(refname:short)"); strings.Contains(branches, "pushed") {
		t.Errorf("the forge holds the sandbox's push: %s", branches)
	}
}

// The gateway answers whoever proves a certificate of its authority and
// no one else: whoever it answers may have any credential it keeps sent
// where they choose.
func TestTheGatewayAnswersNoOneWithoutItsCertificate(t *testing.T) {
	identity := testgateway.Identity(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// A caller that knows the gateway by its authority, and proves no
	// certificate of its own: asked through OpenShell's own client, so
	// that it is the gateway that says no.
	authority, _, _ := testgateway.Files(identity)
	stranger, err := sdk.NewClient(sdk.Config{Address: testgateway.Address, TLS: &sdk.TLSConfig{CAFile: authority}})
	if err != nil {
		t.Fatal(err)
	}
	defer stranger.Close()
	if listed, err := stranger.Sandboxes().ListAll(ctx, "default"); err == nil {
		t.Errorf("with no certificate the gateway listed its sandboxes: %v", listed)
	} else if !sdk.IsUnauthenticated(err) && !sdk.IsPermissionDenied(err) {
		t.Errorf("with no certificate the gateway failed otherwise than by refusing the caller: %v", err)
	}
	if listed, err := stranger.Providers().ListAll(ctx, "default"); err == nil {
		t.Errorf("with no certificate the gateway listed its credentials: %v", listed)
	}

	// The control: with the certificate it answers.
	d, err := openshell.New(openshell.Config{Address: testgateway.Address, Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.List(ctx, nil); err != nil {
		t.Errorf("with its certificate the gateway did not answer: %v", err)
	}
}

// A gap in the gateway, held here so that it is known when it closes:
// a credential kept for a host over https is traded for the stand-in in
// a request sent to that host and port in the clear, since the proxy
// takes what speaks plain http to an endpoint it inspects and the
// gateway has no rule that asks for TLS (0.1.8 refuses one: "unknown
// tls value"). What is in a sandbox can therefore have the credential
// sent unencrypted to the host it was kept for, where whoever is on the
// way reads it. When this test fails the gap is closed: say so in
// AGENTS.md, and turn the test about.
func TestTheGatewayStillSendsACredentialInTheClearWhereASandboxAsks(t *testing.T) {
	d := driven(t)
	ctx := context.Background()
	root := private(t, "code")
	// The forge speaks plain http; the credential is kept for the same
	// host and port over https, where nothing answers.
	forge := testforge.ServeGuarded(t, root, "someone", password)
	at := strings.TrimPrefix(forge, "http://")
	id := testrun.ID(t, "clear")
	t.Cleanup(func() { _ = d.Forget(context.Background(), id) })
	if err := d.Credential(ctx, id, at, "someone", password); err != nil {
		t.Fatal(err)
	}
	record := box(t, "frontseat-openshell-clear")
	fetches(t, d, &record, at, id, []string{"/code"})
	running(t, d, record)
	// The stand-in, as Basic authentication, over plain http.
	exit, printed, _, err := d.Exec(ctx, record.ID, "", []string{"sh", "-c",
		`standin=$(env | sed -n 's/^FRONTSEAT_CRED_[0-9A-F]*=//p'); test -n "$standin" || { echo "no stand-in"; exit 9; }; ` +
			`GIT_TERMINAL_PROMPT=0 git -c credential.helper= -c http.extraHeader="Authorization: Basic $(printf 'someone:%s' "$standin" | base64 | tr -d '\n')" ls-remote ` + forge + `/code 2>&1`}, 1<<14)
	if err != nil {
		t.Fatal(err)
	}
	if exit == 9 {
		t.Fatalf("the sandbox holds no stand-in to try: %s", printed)
	}
	if exit != 0 {
		t.Errorf("the gateway no longer trades a stand-in sent in the clear (exit %d: %s): the gap this test holds is closed, so say so in AGENTS.md and turn the test about", exit, printed)
	}
}

// gatewayClient is a client of the local stack's gateway, as the test's
// own identity, closed when the test ends.
func gatewayClient(t *testing.T) *sdk.Client {
	t.Helper()
	authority, certificate, key := testgateway.Files(testgateway.Identity(t))
	gateway, err := sdk.NewClient(sdk.Config{Address: testgateway.Address, TLS: &sdk.TLSConfig{CAFile: authority, CertFile: certificate, KeyFile: key}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gateway.Close() })
	return gateway
}

// What the gateway keeps is read as anything from outside is: a
// credential whose record does not say this server kept it for the host
// is not bound, and the sandbox is not made.
func TestACredentialTheGatewayHoldsForElsewhereIsNotBound(t *testing.T) {
	testrequired.Stack(t)
	d := driven(t)
	ctx := context.Background()
	gateway := gatewayClient(t)
	const host = "http://planted.example:8080"
	id := testrun.ID(t, "planted")
	t.Cleanup(func() { _ = d.Forget(context.Background(), id) })
	if err := d.Credential(ctx, id, host, "someone", "a-made-up-secret"); err != nil {
		t.Fatal(err)
	}
	// Whoever may write at the gateway says it was kept for elsewhere.
	providers, err := gateway.Providers().ListAll(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for _, held := range providers {
		if held.Spec.Config["id"] == id {
			held.Spec.Config["sent"] = "http://elsewhere.example:8080"
			held.Spec.Credentials = nil
			if _, err := gateway.Providers().Update(ctx, "default", held); err != nil {
				t.Fatal(err)
			}
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("%d credentials were kept under the id, want one", changed)
	}
	record := box(t, "frontseat-openshell-planted")
	record.Egress.Rules = append(record.Egress.Rules, driver.Rule{Host: host, Programs: fetching{}.Programs(), Requests: (fetching{}.Requests("/acme/code")), CredentialID: id})
	t.Cleanup(func() { _ = d.Delete(context.Background(), record.ID) })
	if _, err := d.Apply(ctx, record); err == nil || !strings.Contains(err.Error(), "kept for another host") {
		t.Errorf("a sandbox was let through with a credential kept for elsewhere: %v", err)
	}
	if _, err := d.Observe(ctx, record.ID); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("the refused sandbox is there: %v", err)
	}
}

// A driver says whether its gateway answers it, which a server asks
// before it serves anyone: the gateway that knows its certificate does,
// and one that is not there, or that is asked by a caller it does not
// know, does not.
func TestADriverSaysWhetherItsGatewayAnswersIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := driven(t).Answers(ctx); err != nil {
		t.Fatalf("the gateway that knows this driver's certificate: %v", err)
	}
	for name, config := range map[string]openshell.Config{
		"a gateway that is not there": {Address: "https://127.0.0.1:1", Identity: testgateway.Identity(t)},
		"a caller it does not know":   {Address: testgateway.Address},
	} {
		d, err := openshell.New(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Answers(ctx); err == nil || !strings.Contains(err.Error(), "does not answer this server") {
			t.Errorf("%s: %v, want it said that the gateway does not answer", name, err)
		}
		_ = d.Close()
	}
}

// fetches lets a sandbox fetch the repositories of a forge, as whoever
// asks for a sandbox writes it, here as the control plane would: one
// rule for the forge, for git, with what a fetch of each repository
// asks and a push of those pushed to, and the forge's credential where
// it has one, which git is told to present by the sandbox's environment.
func fetches(t *testing.T, d *openshell.Driver, record *driver.Sandbox, forge string, credential string, repositories []string, pushed ...string) {
	t.Helper()
	rule := driver.Rule{Host: forge, Programs: fetching{}.Programs(), CredentialID: credential}
	for _, repository := range repositories {
		rule.Requests = append(rule.Requests, (fetching{}.Requests(repository))...)
		if slices.Contains(pushed, repository) {
			rule.Requests = append(rule.Requests, (fetching{}.Pushes(repository))...)
		}
	}
	record.Egress.Rules = append(record.Egress.Rules, rule)
	if credential == "" {
		return
	}
	_, username, variable, err := d.Kept(context.Background(), credential)
	if err != nil {
		t.Fatal(err)
	}
	origin := strings.TrimPrefix(forge, "http://")
	scheme := "https"
	if strings.HasPrefix(forge, "http://") {
		scheme = "http"
	}
	if !strings.Contains(origin, ":") {
		origin += map[string]string{"http": ":80", "https": ":443"}[scheme]
	}
	told, err := fetching{}.Presenting(record.Environment, scheme+"://"+origin, username, variable)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range told {
		record.Environment[name] = value
	}
}
