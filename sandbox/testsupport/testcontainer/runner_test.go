package testcontainer_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/local/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/testcontainer"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
	"github.com/frontseat-dev/frontseat/testsupport/testrun"
)

// A runner sandbox: a network that holds a loopback and nothing else,
// a service beside its first container, and one directory of a host
// directory mounted. The box image is both: it holds bash, curl and
// python3.
func runnerSandbox(t *testing.T, name string, block bool, subpath string) (driver.Sandbox, string) {
	testrequired.Stack(t)
	t.Helper()
	id := testrun.ID(t, name)
	image := testbox.Plain(t)
	host := t.TempDir()
	for _, f := range []struct{ name, content string }{{"mine/f", "mine\n"}, {"other/f", "other\n"}} {
		write(t, host, f.name, f.content)
	}
	s := driver.Sandbox{
		ID: id, Image: image, Egress: driver.Egress{Block: block},
		Arguments: []string{"sleep", "600"},
		Volumes:   []driver.Volume{{Name: "work", Kind: driver.Host, Source: host, Subpath: subpath, Path: "/work", ReadOnly: true}},
		Containers: []driver.SandboxContainer{{
			Name: "web", Image: image, Arguments: []string{"python3", "-m", "http.server", "8000", "--bind", "127.0.0.1"},
		}},
	}
	return s, host
}

// applied has the driver make a sandbox of the test's own, running, and
// end it when the test ends (drivertest.Running).
func applied(t *testing.T, d testcontainer.Driver, s driver.Sandbox) {
	t.Helper()
	drivertest.Running(t, d, s)
}

// execIn runs a command in a sandbox's container until it succeeds or
// ten seconds pass, and returns its last exit and output.
func execIn(t *testing.T, d testcontainer.Driver, id, container string, args ...string) (int, string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		exit, out, _, err := d.Exec(context.Background(), id, container, args, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if exit == 0 || time.Now().After(deadline) {
			return exit, string(out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestASandboxsContainersShareItsLoopbackAndReachNothingElse(t *testing.T) {
	d := testcontainer.Driver{}
	s, _ := runnerSandbox(t, "frontseat-test-loopback", true, "")
	applied(t, d, s)

	exit, out := execIn(t, d, s.ID, "", "curl", "-fsS", "-o", "/dev/null", "-w", "%{http_code}", "http://127.0.0.1:8000/")
	if exit != 0 || out != "200" {
		t.Errorf("the first container reaching its neighbour on the loopback: exit %d, %q; want 200", exit, out)
	}
	// Nothing beyond the loopback: an address outside is not reached.
	exit, said, _, err := d.Exec(context.Background(), s.ID, "", []string{"curl", "-sS", "--max-time", "3", "-o", "/dev/null", "http://1.1.1.1/"}, 1<<20)
	if err != nil || exit == 0 {
		t.Errorf("reaching outside: exit %d, %v, %q; want it refused", exit, err, said)
	}
	// The control: the same sandbox let out reaches outside, so the
	// refusal above is the network's, not curl's.
	open, _ := runnerSandbox(t, "frontseat-test-loopback-open", false, "")
	open.Containers = nil
	applied(t, d, open)
	if exit, out := execIn(t, d, open.ID, "", "curl", "-sS", "--max-time", "5", "-o", "/dev/null", "-w", "%{http_code}", "http://1.1.1.1/"); exit != 0 {
		t.Errorf("the control, let out, reaching outside: exit %d, %q; want it reached", exit, out)
	}
}

func TestASubpathMountsOneDirectoryAndNothingBesideIt(t *testing.T) {
	d := testcontainer.Driver{}
	s, _ := runnerSandbox(t, "frontseat-test-subpath", true, "mine")
	s.Containers = nil
	applied(t, d, s)
	if exit, out := execIn(t, d, s.ID, "", "ls", "-R", "/work"); exit != 0 || strings.Contains(out, "other") || !strings.Contains(out, "f") {
		t.Errorf("a subpath's mount holds %q (exit %d); want mine's file and nothing of other", out, exit)
	}
	// The control: the whole directory holds both.
	whole, _ := runnerSandbox(t, "frontseat-test-subpath-whole", true, "")
	whole.Containers = nil
	applied(t, d, whole)
	if exit, out := execIn(t, d, whole.ID, "", "ls", "/work"); exit != 0 || !strings.Contains(out, "mine") || !strings.Contains(out, "other") {
		t.Errorf("the whole directory holds %q (exit %d); want mine and other", out, exit)
	}
}

func TestExecSaysHowACommandEndedAndWhatItPrinted(t *testing.T) {
	d := testcontainer.Driver{}
	s, _ := runnerSandbox(t, "frontseat-test-exec", true, "")
	applied(t, d, s)
	exit, out, _, err := d.Exec(context.Background(), s.ID, "web", []string{"sh", "-c", "echo printed; echo also >&2; exit 3"}, 1<<20)
	if err != nil || exit != 3 || !strings.Contains(string(out), "printed") || !strings.Contains(string(out), "also") {
		t.Errorf("exec in the sandbox's container: exit %d, %q, %v; want 3 and both streams", exit, out, err)
	}
	_, out, truncated, err := d.Exec(context.Background(), s.ID, "", []string{"sh", "-c", "head -c 5000 /dev/zero"}, 1000)
	if err != nil || len(out) != 1000 || !truncated {
		t.Errorf("what was printed past the limit: %d bytes, truncated %v, %v; want 1000 and truncated", len(out), truncated, err)
	}
}

func TestDeletingASandboxRemovesItsContainers(t *testing.T) {
	d := testcontainer.Driver{}
	s, _ := runnerSandbox(t, "frontseat-test-removed", true, "")
	applied(t, d, s)
	if err := d.Delete(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("docker", "ps", "--all", "--quiet", "--filter", "label=dev.frontseat.sandbox.of="+s.ID).Output()
	if err != nil || strings.TrimSpace(string(out)) != "" {
		t.Errorf("the sandbox's containers after it was deleted: %q, %v; want none", out, err)
	}
}

func TestAPortOfASandboxThatReachesNothingIsRefused(t *testing.T) {
	d := testcontainer.Driver{}
	s, _ := runnerSandbox(t, "frontseat-test-blocked-port", true, "")
	s.Ports = []int{8000}
	var refused *driver.Unsupported
	if _, err := d.Apply(context.Background(), s); err == nil || !asUnsupported(err, &refused) {
		d.Delete(context.Background(), s.ID)
		t.Errorf("a published port of a sandbox that reaches nothing: %v, want it refused", err)
	}
}

func asUnsupported(err error, target **driver.Unsupported) bool {
	u, ok := err.(*driver.Unsupported)
	if ok {
		*target = u
	}
	return ok
}
