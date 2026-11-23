package local

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/local"
	"github.com/frontseat-dev/frontseat/local/box"
	"github.com/frontseat-dev/frontseat/local/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testforge"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

// The local stack boots as one container of this checkout's image,
// started as the stack starts it, beside whatever stack runs here: every
// service up and staying up, Podman in the cgroup frontseat-local init
// gives frontseat, and the control plane answering its owner alone.
func TestTheStackBootsAsOneContainer(t *testing.T) {
	testrequired.Stack(t)
	own := testbox.Stack(t)
	// What asks the control plane is the workers' own image, which holds
	// curl, at its digest, on this machine wherever the stack is.
	plain := testbox.Plain(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	answered := func(token string) string {
		status, _ := asked(ctx, own, plain, token, workspaces+"ListWorkspaces", "{}")
		return status
	}
	token := ownerOf(ctx, own, answered)
	if got := answered(token); got != "200" {
		logs, _ := exec.Command("docker", "logs", own).CombinedOutput()
		t.Fatalf("the control plane answered its owner %q, want 200\n%s", got, logs)
	}
	time.Sleep(steady * time.Second)
	// The control: the same call without the owner's token is refused.
	if got := answered(""); got != "401" {
		t.Errorf("the control plane answered a call without the token %q, want 401", got)
	}
	// Each service has stayed up a while: one that s6 restarts over and
	// over is up for a moment at a time, never for long.
	for _, service := range []string{"dns", "podman", "gateway", "controlplane", "storage", "scheduler", "frontend", "assets", "worker"} {
		out, err := exec.CommandContext(ctx, "docker", "exec", own, "/command/s6-svstat", "-o", "up,updownfor", "/run/service/"+service).Output()
		fields := strings.Fields(string(out))
		if err != nil || len(fields) != 2 || fields[0] != "true" {
			t.Errorf("%s is not up: %s %v", service, out, err)
			continue
		}
		if up, err := strconv.Atoi(fields[1]); err != nil || up < steady {
			t.Errorf("%s has been up %s seconds, want %d or more: it is restarted over and over", service, fields[1], steady)
		}
	}
	out, err := exec.CommandContext(ctx, "docker", "exec", own, "sh", "-c", "cat /proc/$(pgrep -f 'podman system service' | head -1)/cgroup").Output()
	if err != nil || strings.TrimSpace(string(out)) != "0::/frontseat/podman" {
		t.Errorf("Podman runs in %q, want the cgroup frontseat-local init gives frontseat: %v", out, err)
	}
	// What the container asks for names is its own resolver alone: its
	// resolv.conf names it, and so does what Podman writes for a
	// container of the host's network, as each sandbox's supervisor is,
	// which asks its one nameserver once. One is made here, initialised
	// (which writes its resolv.conf) and removed, of an empty root.
	resolv, err := exec.CommandContext(ctx, "docker", "exec", own, "cat", "/etc/resolv.conf").Output()
	if named := nameservers(string(resolv)); err != nil || len(named) != 1 || named[0] != ports.ResolverAddress {
		t.Errorf("the container asks %v for names, not its own resolver alone: %v", named, err)
	}
	podman := func(arguments ...string) ([]byte, error) {
		return local.Podman(ctx, own, arguments...).CombinedOutput()
	}
	// Its command is a file that is there and may be run, which crun
	// looks for when the container is initialised; it is never started.
	if out, err := exec.CommandContext(ctx, "docker", "exec", "-u", "frontseat", own, "sh", "-c", "mkdir -p /tmp/resolv-check/etc && : > /tmp/resolv-check/true && chmod 755 /tmp/resolv-check/true").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := podman("create", "--name", "resolv-check", "--network", "host", "--rootfs", "/tmp/resolv-check", "/true"); err != nil {
		t.Fatalf("a container of the host's network was not made: %v\n%s", err, out)
	}
	defer podman("rm", "--force", "resolv-check")
	if out, err := podman("init", "resolv-check"); err != nil {
		t.Fatalf("a container of the host's network was not initialised: %v\n%s", err, out)
	}
	at, err := podman("inspect", "--format", "{{.ResolvConfPath}}", "resolv-check")
	if err != nil {
		t.Fatalf("%v\n%s", err, at)
	}
	written, err := exec.CommandContext(ctx, "docker", "exec", own, "cat", strings.TrimSpace(string(at))).Output()
	if named := nameservers(string(written)); err != nil || len(named) != 1 || named[0] != ports.ResolverAddress {
		t.Errorf("a container of the host's network, as a supervisor is, asks %v for names, not the container's own resolver: %v", named, err)
	}
	// Asked of the running resolver itself, for a host the grid's fetches
	// reach: unbound-host told to ask it and nothing else, by a file of the
	// test's own, gone after.
	ask := `f=$(mktemp) && printf 'server:\n\tdo-not-query-localhost: no\nforward-zone:\n\tname: "."\n\tforward-addr: ` + ports.ResolverAddress + `\n' > "$f" && unbound-host -C "$f" -t A dl.google.com; s=$?; rm -f "$f"; exit $s`
	if out, err := exec.CommandContext(ctx, "docker", "exec", own, "sh", "-c", ask).CombinedOutput(); err != nil || !strings.Contains(string(out), "has address") {
		t.Errorf("the container's resolver answered no address of a host the grid's fetches reach: %v\n%s", err, out)
	}
}

// The services of the API the specs of the stack's container ask, as a
// call is named after them.
const (
	workspaces  = "frontseat.workspace.v1alpha1.WorkspaceService/"
	credentials = "frontseat.credential.v1alpha1.CredentialService/"
)

// asked asks the control plane of a stack's container one call of the
// API (a service's name and the call's, as workspaces and credentials
// begin them), as whoever holds the token given, or no one's, and
// returns the status it answered with and what it answered. What asks is
// the workers' own image, which holds curl, in the container's network:
// a test's stack publishes nothing. A call that is not answered within
// ten minutes ends with the status curl gives one, 000.
func asked(ctx context.Context, own, plain, token, call, request string) (status string, answer []byte) {
	arguments := []string{"run", "--rm", "--pull=never", "--network", "container:" + own, "--entrypoint", "curl", plain,
		"-s", "--max-time", "600", "-w", "\n%{http_code}", "-X", "POST", "-H", "Content-Type: application/json", "-d", request}
	if token != "" {
		arguments = append(arguments, "-H", "Authorization: Bearer "+token)
	}
	out, _ := exec.CommandContext(ctx, "docker", append(arguments, "http://127.0.0.1:"+strconv.Itoa(ports.ControlPlane)+"/"+call)...).Output()
	at := strings.LastIndexByte(string(out), '\n')
	if at < 0 {
		return strings.TrimSpace(string(out)), nil
	}
	return strings.TrimSpace(string(out[at:])), out[:at]
}

// ownerOf waits until the control plane of a stack's container answers
// its owner, and returns the owner's token as the stack laid it out;
// answered says what a call with a token is answered with. A control
// plane that does not come to answer is the caller's to report: the
// token returned is then the last read, or none.
func ownerOf(ctx context.Context, own string, answered func(token string) string) string {
	var token string
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		if out, err := exec.CommandContext(ctx, "docker", "exec", own, "cat", "/var/lib/frontseat/controlplane/owner/token").Output(); err == nil {
			if token = strings.TrimSpace(string(out)); answered(token) == "200" {
				break
			}
		}
	}
	return token
}

// A stack whose container is made anew, as bringing it up always makes
// it, still lets a workspace through with a credential kept before: the
// gateway's key, which it encrypts the credentials it keeps with, is on
// the stack's volume beside what it encrypts, and not in the
// container's home, where a new container found none and made another,
// and OpenShell refused every sandbox let through with a credential
// ("encrypted with a different key-encryption key").
//
// The credential is kept through Frontseat's own call, for a forge that
// serves no one without it, so that a workspace that clones from the
// forge is made only where the gateway decrypts it. The control is the
// same stack with its key gone, as it went with a container that held it
// in its home: the workspace asked of it then is not ready, and the
// stack says why, the key.
func TestAStackMadeAnewOpensAWorkspaceAsItDidBefore(t *testing.T) {
	testrequired.Stack(t)
	own := testbox.Stack(t)
	plain := testbox.Plain(t)
	image := testbox.Image(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	// A forge that serves no one without the password, which is the
	// test's own making.
	root := t.TempDir()
	code := filepath.Join(root, "code")
	testfiles.Tree(t, code, map[string]string{"README": "private source\n"})
	testforge.Committed(t, code)
	const password = "a-password-of-the-tests-own"
	forge := testforge.ServeGuarded(t, root, "someone", password)

	// token is the owner's, once the stack as it is now answers them.
	token := func() string {
		t.Helper()
		// The box a workspace's sandbox runs is handed to the container's
		// Podman, as bringing the stack up hands it.
		if err := box.GiveTo(ctx, own, image); err != nil {
			t.Fatal(err)
		}
		return ownerOf(ctx, own, func(token string) string {
			status, _ := asked(ctx, own, plain, token, workspaces+"ListWorkspaces", "{}")
			return status
		})
	}
	// opened asks the stack as it is now for a workspace of a name that
	// clones from the forge, and returns where it came to: its state,
	// and what it says went wrong. One the control plane does not take,
	// or does not say the end of, is in no state.
	opened := func(owner, name string) (state, failure string) {
		t.Helper()
		create, err := json.Marshal(map[string]any{"name": name, "spec": map[string]any{
			"image":        image,
			"repositories": []map[string]string{{"remote": forge + "/code"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if status, answer := asked(ctx, own, plain, owner, workspaces+"CreateWorkspace", string(create)); status != "200" {
			return "", "not taken (" + status + "): " + string(answer)
		}
		wait, err := json.Marshal(map[string]string{"name": name})
		if err != nil {
			t.Fatal(err)
		}
		status, answer := asked(ctx, own, plain, owner, workspaces+"WaitWorkspace", string(wait))
		var waited struct {
			Workspace struct {
				Status struct{ State, Failure string }
			}
		}
		if err := json.Unmarshal(answer, &waited); status != "200" || err != nil {
			return "", "waiting ended " + status + ": " + string(answer)
		}
		return waited.Workspace.Status.State, waited.Workspace.Status.Failure
	}
	// keys are the gateway's keys beneath a directory of the container.
	keys := func(beneath string) []string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "exec", own, "find", beneath, "-name", "key-encryption-key.bin").Output()
		if err != nil {
			t.Fatalf("looking for the gateway's key beneath %s: %v", beneath, err)
		}
		return strings.Fields(string(out))
	}
	logs := func() string {
		out, _ := exec.Command("docker", "logs", "--tail", "400", own).CombinedOutput()
		return string(out)
	}

	owner := token()
	set, err := json.Marshal(map[string]any{"credential": map[string]string{"host": forge, "username": "someone", "secret": password}})
	if err != nil {
		t.Fatal(err)
	}
	if status, answer := asked(ctx, own, plain, owner, credentials+"SetCredential", string(set)); status != "200" {
		t.Fatalf("the credential for the forge was not kept (%s): %s", status, answer)
	}
	if state, failure := opened(owner, "before"); state != "STATE_READY" {
		t.Fatalf("the workspace asked of the stack as it was started is %s (%s), want it ready\n%s", state, failure, logs())
	}
	kept := keys("/var/lib/frontseat")
	if inHome := keys("/home"); len(kept) != 1 || len(inHome) != 0 {
		t.Fatalf("the gateway keeps its key at %v beneath the stack's state and %v beneath a home, want one on the volume and none in the container", kept, inHome)
	}

	testbox.Anew(t, own)
	if state, failure := opened(token(), "after"); state != "STATE_READY" {
		t.Errorf("the workspace asked of the stack made anew is %s (%s), want it let through with the credential kept before\n%s", state, failure, logs())
	}

	// The control: the key gone, as with a container that held it. It is
	// the test's own stack's, on the test's own volume.
	if out, err := exec.CommandContext(ctx, "docker", "exec", own, "rm", kept[0]).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	testbox.Anew(t, own)
	state, failure := opened(token(), "keyless")
	if state == "STATE_READY" {
		t.Error("a workspace asked of a stack whose key is gone is ready: the spec would not see a key kept in the container")
	}
	if said := failure + "\n" + logs(); !strings.Contains(said, "key-encryption key") {
		t.Errorf("the workspace asked of a stack whose key is gone is %s, and nothing says the key is why (%s)", state, said)
	}
}

// nameservers are the nameservers a resolv.conf names, in order.
func nameservers(resolv string) []string {
	var named []string
	for _, line := range strings.Split(resolv, "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "nameserver" {
			named = append(named, fields[1])
		}
	}
	return named
}

// steady is how long, in seconds, a service has stayed up to be held
// running, rather than restarted over and over by s6.
const steady = 10
