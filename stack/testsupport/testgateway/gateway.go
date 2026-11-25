// Package testgateway gives a test the local stack's OpenShell gateway: its
// address, and what it knows a caller by.
package testgateway

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/stack/local"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
	"github.com/frontseat-dev/frontseat/testsupport/testrun"
)

// Address is where the local stack's gateway answers, on this machine's
// loopback.
var Address = "https://" + Listens

// Compute is the name of the compute driver the local stack's gateway
// makes its sandboxes with, in its one container: what a directory given
// to a sandbox is said to it under
// (stack/image/rootfs/etc/openshell/gateway.template.toml). A test's
// control plane is on this machine and asks this machine's runtime what
// an image is started with, which is another.
const Compute = "podman"

// Listens is the host and port of Address.
var Listens = net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.OpenShell))

// held is the container the gateway runs in, the stack's one, and kept
// where it keeps what it and its callers are known by
// (stack/image/rootfs/etc/openshell/gateway.template.toml).
const (
	held = local.Container
	kept = "/var/lib/frontseat/openshell/tls/"
)

// Identity returns a directory of the test's holding what the gateway is
// known by and knows a caller by: ca.crt, tls.crt and tls.key, copied
// out of its container, readable by the test alone and gone with it. It
// skips the test where no gateway answers, or fails it where the gate
// runs against the stack (testrequired.Missing).
func Identity(t testing.TB) string {
	t.Helper()
	return identity(t, false, t.TempDir())
}

// Required is Identity for a test that was asked to run over the
// gateway: where none answers the test fails, since passing by not
// running is no proof.
func Required(t testing.TB) string {
	t.Helper()
	return identity(t, true, t.TempDir())
}

// RequiredAt copies the required gateway identity into a caller-owned
// directory, for a service whose lifetime extends beyond its first test.
func RequiredAt(t testing.TB, directory string) string {
	t.Helper()
	return identity(t, true, directory)
}

// Files returns where, in a directory Identity, Required or RequiredAt
// returned, the certificate of the gateway's authority is, and the
// certificate and key a caller proves itself with.
func Files(identity string) (authority, certificate, key string) {
	return filepath.Join(identity, "ca.crt"), filepath.Join(identity, "tls.crt"), filepath.Join(identity, "tls.key")
}

func identity(t testing.TB, needed bool, directory string) string {
	t.Helper()
	testrequired.Stack(t)
	conn, err := net.DialTimeout("tcp", Listens, time.Second)
	if err != nil {
		if needed {
			t.Fatalf("no OpenShell gateway answers on %s, and the test was asked to run over one: %v", Listens, err)
		}
		testrequired.Missing(t, "no OpenShell gateway answers on %s: %v", Listens, err)
	}
	_ = conn.Close()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	authority, certificate, key := Files(directory)
	for from, into := range map[string]string{"ca.crt": authority, "client/tls.crt": certificate, "client/tls.key": key} {
		if out, err := exec.Command("docker", "cp", held+":"+kept+from, into).CombinedOutput(); err != nil {
			t.Fatalf("copying %s out of the gateway: %v: %s", from, err, out)
		}
		if err := os.Chmod(into, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

// Runtime runs a command of the container runtime the gateway makes its
// sandboxes with, the stack container's Podman, as the user whose Podman
// it is, and returns what it printed, its error output beside.
func Runtime(ctx context.Context, arguments ...string) ([]byte, error) {
	return local.Podman(ctx, held, arguments...).CombinedOutput()
}

// Laid copies a directory of this machine, with all it holds, to the
// machine the gateway makes its sandboxes on, and returns where it is
// there: a directory of the test's own in the stack's one container,
// which a sandbox may be bound (a host volume). Every sandbox is made
// by that container's Podman, so what a sandbox is bound is a directory
// of that container, and none of this machine.
//
// Everything laid out is open to every user, to read and to write, as
// the grid's pool opens what it binds into a runner sandbox: Podman
// there is rootless, and gives a sandbox's user an id of its own
// choosing on the gateway's machine, which no test knows. What holds a
// sandbox back from a directory so bound is then how it is bound (read
// alone or not), and never whose it is.
//
// The directory is gone when the test ends. It is the test's own to
// delete: its name carries a token drawn here (testrun.ID), and it is
// made here, by a mkdir that fails where anything has the name already.
func Laid(t testing.TB, from string) string {
	t.Helper()
	testrequired.Stack(t)
	own := "/tmp/" + testrun.ID(t, "frontseat-test-laid")
	inside := func(arguments ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "docker", arguments...).CombinedOutput(); err != nil {
			return fmt.Errorf("docker %s, of %s in the gateway's container %s: %w: %s", arguments[0], own, held, err, out)
		}
		return nil
	}
	if err := inside("exec", held, "mkdir", "-m", "0755", "--", own); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := inside("exec", held, "rm", "-rf", "--", own); err != nil {
			t.Error(err)
		}
	})
	// What the directory holds, and not the directory: it is there.
	if err := inside("cp", from+string(filepath.Separator)+".", held+":"+own); err != nil {
		t.Fatal(err)
	}
	if err := inside("exec", held, "chmod", "-R", "a+rwX", "--", own); err != nil {
		t.Fatal(err)
	}
	return own
}
