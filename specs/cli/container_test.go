package e2e

import (
	"context"
	"encoding/json"
	"github.com/frontseat-dev/frontseat/local/box"
	"github.com/frontseat-dev/frontseat/local/ports"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
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
		status, _ := asked(ctx, own, plain, token, "ListWorkspaces", "{}")
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
		return exec.CommandContext(ctx, "docker", append([]string{"exec", "-u", "frontseat", "-e", "XDG_RUNTIME_DIR=/run/user/1000", own, "podman"}, arguments...)...).CombinedOutput()
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

// asked asks the control plane of a stack's container one call of the
// workspace API, as whoever holds the token given, or no one's, and
// returns the status it answered with and what it answered. What asks is
// the workers' own image, which holds curl, in the container's network:
// a test's stack publishes nothing.
func asked(ctx context.Context, own, plain, token, call, request string) (status string, answer []byte) {
	arguments := []string{"run", "--rm", "--pull=never", "--network", "container:" + own, "--entrypoint", "curl", plain,
		"-s", "-w", "\n%{http_code}", "-X", "POST", "-H", "Content-Type: application/json", "-d", request}
	if token != "" {
		arguments = append(arguments, "-H", "Authorization: Bearer "+token)
	}
	out, _ := exec.CommandContext(ctx, "docker", append(arguments, "http://127.0.0.1:"+port+"/frontseat.workspace.v1alpha1.WorkspaceService/"+call)...).Output()
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
// it, opens a workspace as it did before: the gateway's key, which it
// encrypts what it keeps with, is on the stack's volume beside what it
// encrypts, and not in the container's home, where a new container
// found none and made another, and OpenShell's supervisor refused every
// workspace's sandbox ("encrypted with a different key-encryption key").
//
// The control is the same stack with its key gone, as it went with the
// container that held it: a workspace asked of it then does not come to
// be worked in.
func TestAStackMadeAnewOpensAWorkspaceAsItDidBefore(t *testing.T) {
	testrequired.Stack(t)
	own := testbox.Stack(t)
	plain := testbox.Plain(t)
	image := testbox.Image(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	remote := cloned(t, helloModule(t))[0].Remote

	// opened asks the stack as it is now for a workspace of a name and
	// returns where it came to: its state, and what it says went wrong.
	opened := func(name string) (state, failure string) {
		t.Helper()
		// The box a workspace's sandbox runs is handed to the container's
		// Podman, as bringing the stack up hands it.
		if err := box.GiveTo(ctx, own, image); err != nil {
			t.Fatal(err)
		}
		token := ownerOf(ctx, own, func(token string) string {
			status, _ := asked(ctx, own, plain, token, "ListWorkspaces", "{}")
			return status
		})
		create, err := json.Marshal(map[string]any{"name": name, "spec": map[string]any{
			"image":        image,
			"repositories": []map[string]string{{"remote": remote}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if status, answer := asked(ctx, own, plain, token, "CreateWorkspace", string(create)); status != "200" {
			logs, _ := exec.Command("docker", "logs", "--tail", "200", own).CombinedOutput()
			t.Fatalf("the workspace %s was not taken (%s): %s\n%s", name, status, answer, logs)
		}
		wait, err := json.Marshal(map[string]string{"name": name})
		if err != nil {
			t.Fatal(err)
		}
		status, answer := asked(ctx, own, plain, token, "WaitWorkspace", string(wait))
		var waited struct {
			Workspace struct {
				Status struct{ State, Failure string }
			}
		}
		if err := json.Unmarshal(answer, &waited); status != "200" || err != nil {
			t.Fatalf("waiting for the workspace %s ended %s: %s %v", name, status, answer, err)
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

	if state, failure := opened("before"); state != "STATE_READY" {
		t.Fatalf("the workspace asked of the stack as it was started is %s (%s), want it ready", state, failure)
	}
	kept := keys("/var/lib/frontseat")
	if inHome := keys("/home"); len(kept) != 1 || len(inHome) != 0 {
		t.Fatalf("the gateway keeps its key at %v beneath the stack's state and %v beneath a home, want one on the volume and none in the container", kept, inHome)
	}

	testbox.Anew(t, own)
	if state, failure := opened("after"); state != "STATE_READY" {
		t.Errorf("the workspace asked of the stack made anew is %s (%s), want it ready as the one before", state, failure)
	}

	// The control: the key gone, as with the container that held it. It
	// is the test's own stack's, on the test's own volume.
	if out, err := exec.CommandContext(ctx, "docker", "exec", own, "rm", kept[0]).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	testbox.Anew(t, own)
	if state, _ := opened("keyless"); state == "STATE_READY" {
		t.Error("a workspace asked of a stack whose key is gone is ready: the spec would not see a key kept in the container")
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
