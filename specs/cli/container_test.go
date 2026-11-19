package e2e

import (
	"context"
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
		arguments := []string{"run", "--rm", "--pull=never", "--network", "container:" + own, "--entrypoint", "curl", plain,
			"-s", "-o", "/dev/null", "-w", "%{http_code}", "-X", "POST", "-H", "Content-Type: application/json", "-d", "{}"}
		if token != "" {
			arguments = append(arguments, "-H", "Authorization: Bearer "+token)
		}
		out, _ := exec.CommandContext(ctx, "docker", append(arguments, "http://127.0.0.1:7700/frontseat.workspace.v1alpha1.WorkspaceService/ListWorkspaces")...).Output()
		return strings.TrimSpace(string(out))
	}
	var token string
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		if out, err := exec.CommandContext(ctx, "docker", "exec", own, "cat", "/var/lib/frontseat/controlplane/owner/token").Output(); err == nil {
			if token = strings.TrimSpace(string(out)); answered(token) == "200" {
				break
			}
		}
	}
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
